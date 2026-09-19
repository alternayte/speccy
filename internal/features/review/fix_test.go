package review_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// T-092: a suggested fix changes nothing until the author accepts it.
func TestSuggestFix_RequiresAccept(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			pe := newPipeline(t, e, map[string]string{"pay/SPEC.md": groundedSDD}, "fake-large")
			run, fs, _ := pe.run(t, "pay")
			var target pgdb.Finding
			for _, f := range fs {
				if f.CheckSlug == review.GroundingContradicted {
					target = f
				}
			}
			if target.ID == [16]byte{} {
				t.Fatal("the fixture has no contradicted claim")
			}
			a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews, Change: pe.bundles.Change}
			q := pe.bundles.DB.Queries()
			before, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(pe.dir, "pay", "SPEC.md")
			ask := api.SuggestFixRequestObject{RunId: run.ID, FindingId: target.ID}
			res, err := a.SuggestFix(ctx, ask)
			if err != nil {
				t.Fatal(err)
			}
			s := res.(api.SuggestFix200JSONResponse)
			if s.Old != "at 999 kilobytes for every endpoint" || s.VersionId != before.CurrentVersionID.UUID {
				t.Fatalf("suggestion = %+v", s)
			}
			after, _ := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
			disk, _ := os.ReadFile(path)
			if after.CurrentVersionID != before.CurrentVersionID || string(disk) != groundedSDD {
				t.Fatal("a suggestion changed the doc before an accept")
			}

			acc := api.AcceptFixRequestObject{RunId: run.ID, FindingId: target.ID}
			out, err := a.AcceptFix(ctx, acc)
			if err != nil {
				t.Fatal(err)
			}
			w := out.(api.AcceptFix200JSONResponse)
			disk, _ = os.ReadFile(path)
			if !w.Changed || w.Version.Id == after.CurrentVersionID.UUID || !strings.Contains(string(disk), "at 1 megabyte for every endpoint") {
				t.Fatalf("accept: %+v; file:\n%s", w, disk)
			}
			// The patch is used once.
			if _, err := a.AcceptFix(ctx, acc); err == nil {
				t.Fatal("a second accept applied the patch again")
			} else if ke, ok := kernel.AsError(err); !ok || ke.Code != "no_suggestion" {
				t.Fatalf("second accept: %v", err)
			}
		})
	}
}
