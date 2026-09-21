package review_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// codeSDD is an SDD that stands alone and names the code that implements it.
const codeSDD = "---\ntype: sdd\ntitle: Refunds design\nlinks:\n  - kind: implemented-by\n    target: github:acme/app#internal/pay\n" +
	"---\n\n# Refunds design\n\n## Design\n\nThe refund worker pays in one step.\n"

const standaloneAck = "standalone:\n  reason: Internal change to storage.\n  acknowledged_by: nathan\n"

// commitsAPI serves the one GitHub call the drift check makes, with the commit date it is given.
func commitsAPI(t *testing.T, at time.Time) func(context.Context, string) (*github.Client, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/commits" || r.URL.Query().Get("path") != "internal/pay" {
			t.Errorf("unexpected GitHub call: %s?%s", r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `[{"sha":"2b9f0c1d4e5a6b7c8d9e0f1a2b3c4d5e6f708192","commit":{"committer":{"date":%q}}}]`,
			at.Format(time.RFC3339))
	}))
	t.Cleanup(srv.Close)
	return func(context.Context, string) (*github.Client, error) {
		return &github.Client{API: srv.URL, Token: "test"}, nil
	}
}

// TestExternal_CodeDriftWarns pins that a code target that moved after the version warns, and
// does not block the verdict.
func TestExternal_CodeDriftWarns(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{
				"refunds-sdd/SPEC.md":          codeSDD,
				ackPath("refunds-sdd/SPEC.md"): standaloneAck,
				"refunds-sdd/.keep":            "",
			})
			en.reviews.GitHub = commitsAPI(t, time.Now().Add(time.Hour))
			relint(t, en, "refunds-sdd")
			_, fs, v := en.latest(t, "refunds-sdd")
			drift := findingsOf(fs, review.CodeDriftSlug)
			if len(drift) != 1 || drift[0].Level != "SHOULD" || !strings.Contains(drift[0].Message, "2b9f0c1") {
				t.Fatalf("drift findings = %+v, want one SHOULD naming the commit", drift)
			}
			if strings.Contains(string(v.BlockingFindingIds), drift[0].ID.String()) {
				t.Errorf("drift blocks the verdict: %s", v.BlockingFindingIds)
			}
			states, err := en.bundles.DB.Queries().ListLinkStates(ctx, bundleRow(t, en, "refunds-sdd").ID)
			if err != nil || len(states) != 1 || states[0].State != "drifted" {
				t.Fatalf("link states = %+v, %v", states, err)
			}
			// The same doc, with the commit from before the version: no finding, state aligned.
			en.reviews.GitHub = commitsAPI(t, time.Now().Add(-48*time.Hour))
			relint(t, en, "refunds-sdd")
			_, fs, _ = en.latest(t, "refunds-sdd")
			if got := findingsOf(fs, review.CodeDriftSlug); len(got) != 0 {
				t.Errorf("an older commit still drifts: %+v", got)
			}
			states, err = en.bundles.DB.Queries().ListLinkStates(ctx, bundleRow(t, en, "refunds-sdd").ID)
			if err != nil || len(states) != 1 || states[0].State != "aligned" {
				t.Fatalf("link states = %+v, %v", states, err)
			}
		})
	}
}

// TestExternal_BadTargetIsMust pins that a target with no pattern fails lint.
func TestExternal_BadTargetIsMust(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			doc := strings.Replace(codeSDD, "github:acme/app#internal/pay", "jira:PAY-412", 1)
			doc = strings.Replace(doc, "implemented-by", "references", 1)
			en := newEnv(t, e, map[string]string{
				"refunds-sdd/SPEC.md":          doc,
				ackPath("refunds-sdd/SPEC.md"): standaloneAck,
			})
			_, fs, _ := en.latest(t, "refunds-sdd")
			bad := findingsOf(fs, review.ExternalTargetSlug)
			if len(bad) != 1 || bad[0].Level != "MUST" || !strings.Contains(bad[0].Message, "link_patterns.jira") {
				t.Fatalf("external target findings = %+v, want one MUST naming the pattern", bad)
			}
		})
	}
}

func bundleRow(t *testing.T, en *env, slug string) (id pgdb.Bundle) {
	t.Helper()
	b, err := en.bundles.DB.Queries().GetBundleBySlug(context.Background(),
		pgdb.GetBundleBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// relint runs the lint pass again, so a new GitHub answer reaches the findings.
func relint(t *testing.T, en *env, slug string) {
	t.Helper()
	b := bundleRow(t, en, slug)
	if _, err := en.reviews.Lint(context.Background(), b, b.CurrentVersionID.UUID); err != nil {
		t.Fatal(err)
	}
}
