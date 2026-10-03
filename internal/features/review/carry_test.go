package review_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
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

// The AI findings carry however many saves follow the full review. They left the verdict with
// no sign on the 51st save, because the search for the full review read one page of runs.
func TestCarrySurvivesManySaves(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	pe.run(t, "pay")
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", "with the order ID and item 0", 1))
	first, _ := pe.current(t, "pay")
	for i := 1; i <= 55; i++ {
		pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", fmt.Sprintf("with the order ID and item %d", i), 1))
	}
	last, v := pe.current(t, "pay")
	if len(last) != len(first) || v.AiVersionNumber == nil {
		t.Errorf("after 56 saves the rail has %d findings and AI version %v, want the %d findings of the first save", len(last), v.AiVersionNumber, len(first))
	}
}

// current returns the finding IDs the rail lists for the bundle's verdict, and the verdict.
func (pe *pipelineEnv) current(t *testing.T, slug string) ([]string, api.BundleVerdict) {
	t.Helper()
	ctx := context.Background()
	q := pe.bundles.DB.Queries()
	b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: slug})
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

// docs/specs/carry-repo-files.md: a spec doc that links to a repo file above its folder gets
// that file into its bundle, and the link is not broken. A link to a spec doc of another bundle
// is not broken. A link to a file that git ignores stays a broken-link MUST that says so, and a
// link above the root stays a broken link. Grounding reads the carried file, and a change to
// it makes a new version.
func TestCarry_RepoFilesAboveTheFolder(t *testing.T) {
	sdd := groundedSDD + "\n## References\n\nSee the [limits](../shared/limits.md), the [PRD](../prd/PRD.md), the [keys](../config/prod-keys.yaml) and the [notes](../../outside.md).\n"
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{
		".gitignore":            "config/prod-*.yaml\n",
		"pay/SPEC.md":           sdd,
		"shared/limits.md":      limitsFile,
		"prd/PRD.md":            "---\ntype: prd\ntitle: Pay\n---\n# Pay\n\n## Goals\n\nPeople pay.\n",
		"config/prod-keys.yaml": "key: secret\n",
	}, "fake-1")
	run, fs, _ := pe.run(t, "pay")
	var broken []string
	for _, f := range fs {
		if f.CheckSlug == lint.BrokenLink {
			broken = append(broken, f.Level+": "+f.Message)
		}
	}
	want := []string{
		"MUST: The link to ../config/prod-keys.yaml points to a file that git ignores, so Speccy does not take it into the bundle.",
		"MUST: The link to ../../outside.md points outside the bundle.",
	}
	if strings.Join(broken, " | ") != strings.Join(want, " | ") {
		t.Errorf("broken links:\n%s\nwant:\n%s", strings.Join(broken, "\n"), strings.Join(want, "\n"))
	}
	if _, c := groundingOf(t, pe, run, fs, "Stripe allows 100"); c.Label != "verified" || !strings.Contains(string(c.Sources), "@repo/shared/limits.md") {
		t.Errorf("the claim that the repo file states: %s from %s, want verified by @repo/shared/limits.md", c.Label, c.Sources)
	}
	ctx := context.Background()
	q := pe.bundles.DB.Queries()
	b, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	before := b.CurrentVersionID.UUID
	pe.write(t, "shared/limits.md", limitsFile+"\nThe provider keeps each log line for 30 days.\n")
	b, _ = q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	if b.CurrentVersionID.UUID == before {
		t.Error("a change to the carried file made no new version")
	}
}
