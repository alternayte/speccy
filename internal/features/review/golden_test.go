package review_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/bundle"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store/storetest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/bundles")

type golden struct {
	Verdict  string          `json:"verdict"`
	Score    int64           `json:"score"`
	Findings []goldenFinding `json:"findings"`
}

type goldenFinding struct {
	Check string `json:"check"`
	Level string `json:"level"`
	Quote string `json:"quote"`
}

// The lint findings and the verdict of each fixture bundle (BUILD §5, golden layer).
// Run `go test ./internal/features/review -run Golden -update` after an intended change.
func TestGolden_FixtureBundles(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join("..", "..", "..", "testdata", "bundles")
	e := storetest.Engines()[0]
	db := e.Open(t)
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]profile.Loaded{}
	for _, l := range loaded {
		byKey[l.Profile.Key] = l
	}
	versions, err := profile.Record(ctx, db, ws, byKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	r, err := local.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	bundles := &bundle.Service{DB: db, Workspace: ws, Local: r}
	reviews := &review.Service{DB: db, Workspace: ws, Profiles: func() map[string]profile.Versioned { return versions }, Repo: bundles.RepoConfig, Decisions: bundles.Decisions}
	bundles.AfterChange = reviews.EnsureLinted
	if err := bundles.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := db.Queries().ListSpecDocs(ctx, pgdb.ListSpecDocsParams{WorkspaceID: ws, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 4 {
		t.Fatalf("found %d fixture bundles, want at least 4", len(list))
	}
	for _, b := range list {
		t.Run(b.Slug, func(t *testing.T) {
			q := db.Queries()
			run, err := q.LatestRun(ctx, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			v, err := q.GetVerdict(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			fs, err := q.ListFindings(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := golden{Verdict: v.Result, Score: v.Score, Findings: []goldenFinding{}}
			for _, f := range sortByStart(fs) {
				var a struct {
					Quote string `json:"quote"`
				}
				_ = json.Unmarshal(f.Anchor, &a)
				got.Findings = append(got.Findings, goldenFinding{Check: f.CheckSlug, Level: f.Level, Quote: a.Quote})
			}
			path := filepath.Join(root, b.Slug+".golden.json")
			out, _ := json.MarshalIndent(got, "", "  ")
			out = append(out, '\n')
			if *update {
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v. Run with -update to create it.", err)
			}
			if string(want) != string(out) {
				t.Errorf("%s differs from the run. Got:\n%s", path, out)
			}
		})
	}
}

// sortByStart orders findings by position, then by check, so the golden file is stable.
func sortByStart(fs []pgdb.Finding) []pgdb.Finding {
	start := func(f pgdb.Finding) int {
		var a struct {
			Start int `json:"start"`
		}
		_ = json.Unmarshal(f.Anchor, &a)
		return a.Start
	}
	out := append([]pgdb.Finding(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := start(out[i]), start(out[j])
		if si != sj {
			return si < sj
		}
		return out[i].CheckSlug < out[j].CheckSlug
	})
	return out
}
