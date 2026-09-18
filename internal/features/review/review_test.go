package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/bundle"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// env is a local folder with a bundle service and a review service wired as in main.
type env struct {
	dir     string
	bundles *bundle.Service
	reviews *Service
}

func newEnv(t *testing.T, e storetest.Engine, files map[string]string) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	db := e.Open(t)
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.LoadLocal(filepath.Join(dir, ".speccy", "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	versions, err := profile.Record(ctx, db, ws, loaded, "local")
	if err != nil {
		t.Fatal(err)
	}
	root, err := local.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	en := &env{dir: dir}
	en.bundles = &bundle.Service{DB: db, Workspace: ws, Local: root}
	en.reviews = &Service{DB: db, Workspace: ws,
		Profiles: func() map[string]profile.Versioned { return versions }, Repo: en.bundles.RepoConfig}
	en.bundles.AfterChange = en.reviews.EnsureLinted
	if err := en.bundles.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	return en
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// latest returns the latest run of the bundle with slug, and its findings and verdict.
func (en *env) latest(t *testing.T, slug string) (pgdb.ReviewRun, []pgdb.Finding, pgdb.Verdict) {
	t.Helper()
	ctx := context.Background()
	q := en.bundles.DB.Queries()
	b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: slug})
	if err != nil {
		t.Fatalf("bundle %s: %v", slug, err)
	}
	run, err := q.LatestRun(ctx, b.ID)
	if err != nil {
		t.Fatalf("run of %s: %v", slug, err)
	}
	fs, err := q.ListFindings(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := q.GetVerdict(ctx, run.ID)
	if err != nil && run.Status == "complete" {
		t.Fatal(err)
	}
	return run, fs, v
}

const cleanPRD = "# Refunds\n\n## Problem\n\nThe support team refunds 40 orders a day by hand.\n\n## Users\n\nSupport agents.\n\n## Goals\n\n- **G-1:** Manual refunds fall to 5 a day.\n\n## Non-goals\n\n- Refunds for gift cards.\n\n## Requirements\n\n- **REQ-001:** An agent MUST refund an order in one step.\n\n## Dependencies\n\nNone.\n\n## Open questions\n\nNone.\n"

// T-093
func TestConfig_PathMapping(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			en := newEnv(t, e, map[string]string{
				".speccy.yaml":         "map:\n  - glob: docs/**/prd-*.md\n    profile: prd\n",
				"docs/prd-refunds.md":  cleanPRD,
				"docs/x/prd-typed.md":  "---\ntype: sdd\nstandalone:\n  reason: Internal.\n---\n" + cleanPRD,
				"docs/notes-random.md": "# Notes\n",
			})
			run, fs, v := en.latest(t, "docs/prd-refunds")
			if run.ProfileKey != "prd" || run.ProfileVersion != 1 || run.Status != "complete" {
				t.Errorf("mapped file: run = %+v, want profile prd v1, complete", run)
			}
			for _, f := range fs {
				if f.Level == "MUST" {
					t.Errorf("mapped clean PRD has a MUST finding: %s %s", f.CheckSlug, f.Message)
				}
			}
			if v.Result != "build_ready" {
				t.Errorf("mapped clean PRD verdict = %s", v.Result)
			}
			if run, _, _ := en.latest(t, "docs/x/prd-typed"); run.ProfileKey != "sdd" {
				t.Errorf("frontmatter type must win over the mapping: profile = %s", run.ProfileKey)
			}
		})
	}
}

// T-095
func TestAdoption_RelaxedCheck(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			sdd := "---\ntype: sdd\n---\n# Pay\n\nThe limit is TBD.\n"
			en := newEnv(t, e, map[string]string{
				".speccy.yaml": "adoption:\n  relaxed: [links.has-upstream, lint.placeholder, lint.required-headings]\n",
				"pay/SPEC.md":  sdd,
			})
			_, fs, v := en.latest(t, "pay")
			relaxedSeen := map[string]bool{}
			for _, f := range fs {
				if f.Relaxed {
					relaxedSeen[f.CheckSlug] = true
					if f.Level != "INFO" {
						t.Errorf("relaxed %s reports at %s, want INFO", f.CheckSlug, f.Level)
					}
				}
			}
			if !relaxedSeen["lint.placeholder"] || !relaxedSeen["links.has-upstream"] {
				t.Errorf("relaxed checks must still appear as findings; saw %v", relaxedSeen)
			}
			if v.Result != "build_ready" || v.RelaxedCount != 3 {
				t.Errorf("verdict = %s with %d relaxed, want build_ready with 3", v.Result, v.RelaxedCount)
			}

			// Removing a check from the list restores its level.
			writeFile(t, en.dir, ".speccy.yaml", "adoption:\n  relaxed: [links.has-upstream, lint.required-headings]\n")
			writeFile(t, en.dir, "pay/SPEC.md", sdd+"\nMore.\n")
			if err := en.bundles.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			_, fs, v = en.latest(t, "pay")
			for _, f := range fs {
				if f.CheckSlug == "lint.placeholder" && (f.Level != "MUST" || f.Relaxed) {
					t.Errorf("placeholder after removal: level %s, relaxed %v", f.Level, f.Relaxed)
				}
			}
			if v.Result != "not_build_ready" || v.RelaxedCount != 2 {
				t.Errorf("verdict = %s with %d relaxed, want not_build_ready with 2", v.Result, v.RelaxedCount)
			}
		})
	}
}

// REQ-012: a run records the profile version it used, and a profile edit makes a new version.
func TestRun_RecordsProfileVersion(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{"pay/SPEC.md": "---\ntype: adr\n---\n# A\n"})
			if run, _, _ := en.latest(t, "pay"); run.Status != "failed" || run.Error == "" {
				t.Errorf("an unknown profile: run = %+v, want failed with an error", run)
			}
			writeFile(t, en.dir, ".speccy/profiles/t.md", "# ADR\n\n## Decision <!-- required -->\n")
			writeFile(t, en.dir, ".speccy/profiles/adr.yaml", "key: adr\nname: ADR\ntemplate: t.md\nchecks: []\n")
			loaded, err := profile.LoadLocal(filepath.Join(en.dir, ".speccy", "profiles"))
			if err != nil {
				t.Fatal(err)
			}
			versions, err := profile.Record(ctx, en.bundles.DB, en.bundles.Workspace, loaded, "local")
			if err != nil {
				t.Fatal(err)
			}
			en.reviews.Profiles = func() map[string]profile.Versioned { return versions }
			if err := en.reviews.EnsureLinted(ctx); err != nil {
				t.Fatal(err)
			}
			run, fs, _ := en.latest(t, "pay")
			if run.Status != "complete" || run.ProfileVersion != 1 || len(fs) != 1 || fs[0].CheckSlug != "lint.required-headings" {
				t.Fatalf("run = %+v, findings %d", run, len(fs))
			}
			writeFile(t, en.dir, ".speccy/profiles/adr.yaml", "key: adr\nname: ADR v2\ntemplate: t.md\nchecks: []\n")
			loaded, _ = profile.LoadLocal(filepath.Join(en.dir, ".speccy", "profiles"))
			versions, err = profile.Record(ctx, en.bundles.DB, en.bundles.Workspace, loaded, "local")
			if err != nil {
				t.Fatal(err)
			}
			if err := en.reviews.EnsureLinted(ctx); err != nil {
				t.Fatal(err)
			}
			if run, _, _ := en.latest(t, "pay"); run.ProfileVersion != 2 {
				t.Errorf("after a profile edit the run used version %d, want 2", run.ProfileVersion)
			}
		})
	}
}
