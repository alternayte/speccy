package review_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// An edit keeps the AI findings of the sections it did not touch: they count in the verdict
// and keep their IDs. An edit to a finding's own section drops it (#60).
func TestAnEditCarriesTheAIFindingsOfUnchangedSections(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			pe := newPipeline(t, e, map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
			_, findings, _ := pe.run(t, "pay")
			limits := []string{"Payments", "Limits"}
			var inLimits []string
			for _, f := range findings {
				var an anchor.Anchor
				_ = json.Unmarshal(f.Anchor, &an)
				if f.Stage == review.StageGrounding && slices.Equal(an.HeadingPath, limits) {
					inLimits = append(inLimits, f.ID.String())
				}
			}
			if len(inLimits) == 0 {
				t.Fatal("the full run has no grounding finding in Limits to carry")
			}

			// Edit Risks: the findings in Limits stay, with their IDs.
			pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", "with the order ID and the time", 1))
			ids, v := pe.current(t, "pay")
			for _, id := range inLimits {
				if !slices.Contains(ids, id) {
					t.Errorf("finding %s in Limits did not carry after an edit to Risks", id)
				}
			}
			if v.Kind != api.BundleVerdictKindLint || v.AiVersionNumber == nil || *v.AiVersionNumber != 1 || v.SectionsChanged == nil || *v.SectionsChanged != 1 {
				t.Errorf("verdict = kind %s, ai version %v, sections changed %v; want lint, 1, 1", v.Kind, v.AiVersionNumber, v.SectionsChanged)
			}

			// Edit Limits: its findings drop out.
			pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "999 kilobytes", "998 kilobytes", 1))
			ids, _ = pe.current(t, "pay")
			for _, id := range inLimits {
				if slices.Contains(ids, id) {
					t.Errorf("finding %s carried although its section changed", id)
				}
			}
		})
	}
}

// current returns the finding IDs the rail lists for the bundle's verdict, and the verdict.
func (pe *pipelineEnv) current(t *testing.T, slug string) ([]string, api.BundleVerdict) {
	t.Helper()
	ctx := context.Background()
	q := pe.bundles.DB.Queries()
	b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := review.Summary(ctx, q, b)
	if err != nil || v == nil {
		t.Fatalf("summary: %v %v", v, err)
	}
	a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews}
	res, err := a.ListFindings(ctx, api.ListFindingsRequestObject{RunId: v.RunId})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range res.(api.ListFindings200JSONResponse).Items {
		ids = append(ids, f.Id.String())
	}
	return ids, *v
}
