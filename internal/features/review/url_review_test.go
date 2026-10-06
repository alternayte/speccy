package review_test

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// repoAtCommit serves one commit of a repo as the GitHub API does: the commit, its tree, its
// blobs, and one pull request that changes the files in changed.
func repoAtCommit(t *testing.T, files map[string]string, changed []string) *httptest.Server {
	t.Helper()
	blob := func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/repos/acme/specs")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case p == "/pulls/7":
			_, _ = w.Write([]byte(`{"head":{"ref":"feature","sha":"c0ffee0","repo":{"full_name":"acme/specs"}},"base":{"ref":"main"}}`))
		case p == "/pulls/7/files":
			var out []map[string]string
			for _, f := range changed {
				out = append(out, map[string]string{"filename": f, "status": "modified", "patch": "@@ -1 +1 @@\n+x"})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/commits/"):
			_, _ = w.Write([]byte(`{"sha":"c0ffee0","commit":{"tree":{"sha":"7ree"}}}`))
		case p == "/git/trees/7ree":
			// GitHub lists the folders of a tree as entries too.
			var tree []map[string]any
			dirs := map[string]bool{}
			for path, content := range files {
				tree = append(tree, map[string]any{"path": path, "type": "blob", "mode": "100644", "sha": blob(content), "size": len(content)})
				for d := path; strings.Contains(d, "/"); {
					d = d[:strings.LastIndex(d, "/")]
					if !dirs[d] {
						dirs[d] = true
						tree = append(tree, map[string]any{"path": d, "type": "tree", "mode": "040000", "sha": blob(d)})
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": tree})
		case strings.HasPrefix(p, "/git/blobs/"):
			for _, content := range files {
				if blob(content) == strings.TrimPrefix(p, "/git/blobs/") {
					_ = json.NewEncoder(w).Encode(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// #92: the review of a pull request URL takes the spec docs that the pull request changes,
// reads the repo's own .speccy.yaml at that commit, resolves a link to another spec doc of
// the same commit, and saves no bundle.
func TestReviewURL_PullRequest(t *testing.T) {
	files := map[string]string{
		".speccy.yaml":           "adoption:\n  relaxed: [lint.placeholder]\n",
		"docs/prd/PRD.md":        "---\ntype: prd\ntitle: Pay\n---\n# Pay\n\n## Requirements\n\n- **REQ-001:** A customer pays once.\n",
		"docs/sdd/SPEC.md":       "---\ntype: sdd\ntitle: Pay design\nlinks:\n  - kind: implements\n    target: ../prd/PRD.md\n---\n# Pay design\n\n## Context\n\nThe limit is TBD.\n",
		"docs/sdd/assets/api.md": "An asset.\n",
		"docs/other/SPEC.md":     "---\ntype: sdd\ntitle: Other\n---\n# Other\n",
	}
	gh := repoAtCommit(t, files, []string{"docs/sdd/SPEC.md", "README.md"})
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{"local/SPEC.md": "---\ntype: sdd\n---\n# Local\n"})
			en.reviews.GitHub = func(context.Context, string) (*github.Client, error) {
				return &github.Client{API: gh.URL, Token: "t"}, nil
			}
			res, err := en.reviews.ReviewURL(ctx, "https://github.com/acme/specs/pull/7", review.Stages{})
			if err != nil {
				t.Fatal(err)
			}
			// The pull request changes one SDD: that SDD is reviewed, and no other doc.
			if res.Commit != "c0ffee0" || res.Pull != 7 || len(res.Docs) != 1 || res.Docs[0].Path != "docs/sdd/SPEC.md" || res.Docs[0].Err != nil {
				t.Fatalf("review = %+v", res)
			}
			slugs := map[string]string{}
			for _, f := range res.Docs[0].Result.Findings {
				slugs[f.CheckSlug] = string(f.Level)
			}
			// The link to the PRD of the same commit resolves, so the SDD has its upstream doc and
			// the coverage check reads the PRD's requirement.
			if _, missing := slugs[review.HasUpstreamSlug]; missing || slugs[review.CoverageSlug] == "" {
				t.Errorf("findings %v: want no has-upstream finding, and a coverage finding for REQ-001", slugs)
			}
			// The repo's own config relaxes the placeholder check.
			if slugs["lint.placeholder"] != "INFO" {
				t.Errorf("lint.placeholder is %q; the repo's .speccy.yaml relaxes it to INFO", slugs["lint.placeholder"])
			}
			all, err := en.bundles.DB.Queries().ListSpecDocs(ctx, pgdb.ListSpecDocsParams{WorkspaceID: en.bundles.Workspace, PageSize: 100})
			if err != nil || len(all) != 1 {
				t.Errorf("%d saved spec docs after the review, err %v; want the 1 local doc", len(all), err)
			}

			// A folder URL names the spec docs under it.
			res, err = en.reviews.ReviewURL(ctx, "https://github.com/acme/specs/tree/feature/docs", review.Stages{})
			if err != nil || len(res.Docs) != 3 {
				t.Fatalf("a folder: %d docs, err %v; want 3", len(res.Docs), err)
			}
		})
	}
}

// #131: a repo with no .speccy.yaml takes the .speccy.yaml of the served folder, so a map entry
// there finds a spec doc that names no type. The repo's own file wins when it has one.
func TestPlanURL_LocalConfigWhenTheRepoHasNone(t *testing.T) {
	ctx := context.Background()
	doc := "<!-- size: feature -->\n# Pay\n\n## Context\n\nText.\n"
	local := func() (source.RepoConfig, string, bool, error) {
		cfg, err := source.ParseRepoConfig([]byte("map:\n  - glob: \"**/SDD - *.md\"\n    profile: sdd\npr:\n  levels: [must, should]\n"))
		return cfg, "/home/me/specs/.speccy.yaml", true, err
	}
	plan := func(files map[string]string) (review.URLPlan, error) {
		gh := repoAtCommit(t, files, []string{"docs/SDD - Pay.md"})
		s := &review.Service{LocalConfig: local, GitHub: func(context.Context, string) (*github.Client, error) {
			return &github.Client{API: gh.URL, Token: "t"}, nil
		}}
		return s.PlanURL(ctx, "https://github.com/acme/specs/pull/7")
	}
	p, err := plan(map[string]string{"docs/SDD - Pay.md": doc})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Review.Config
	if len(p.Items) != 1 || c.Source != review.ConfigLocal || c.Path != "/home/me/specs/.speccy.yaml" || len(c.PR.Levels) != 2 {
		t.Fatalf("items %d, config %+v; want the doc, and the local file with its levels", len(p.Items), c)
	}
	_, err = plan(map[string]string{"docs/SDD - Pay.md": doc, ".speccy.yaml": "mode: standalone\n"})
	if ke, ok := kernel.AsError(err); !ok || ke.Code != "no_spec_doc" {
		t.Fatalf("with a repo .speccy.yaml: err %v; want no_spec_doc, because the repo's file wins", err)
	}
}
