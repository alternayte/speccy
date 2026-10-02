package review_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/lint"
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
			before, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(pe.dir, "pay", "SPEC.md")
			// A contradicted claim is an answer finding: with no fact from the author, the model
			// would write a placeholder, so Speccy asks for the answer first.
			ask := api.SuggestFixRequestObject{RunId: run.ID, FindingId: target.ID}
			if _, err := a.SuggestFix(ctx, ask); err == nil {
				t.Fatal("an answer finding got a fix with no answer")
			} else if ke, ok := kernel.AsError(err); !ok || ke.Code != "answer_needed" {
				t.Fatalf("no answer: %v", err)
			}
			answer := "1 megabyte"
			ask.Body = &api.SuggestFixRequest{Answer: &answer}
			res, err := a.SuggestFix(ctx, ask)
			if err != nil {
				t.Fatal(err)
			}
			// Speccy picks the paragraph of the claim; the model copies no text of the file.
			s := res.(api.SuggestFix200JSONResponse)
			const para = "The provider caps each request body at 999 kilobytes for every endpoint, and the service stays below it. The orders database runs on Postgres 17 in every region."
			if s.Old != para || s.New != strings.Replace(para, "999 kilobytes", "1 megabyte", 1) || s.Line != 17 || s.EndLine != 17 || s.VersionId != before.CurrentVersionID.UUID {
				t.Fatalf("suggestion = %+v", s)
			}
			after, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
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

const passiveSDD = `---
type: sdd
title: Orders
standalone:
  reason: Internal change.
  acknowledged_by: nathan
---

# Orders

## Retries

The request is retried. The client waits one second first.

The order is stored. The invoice is sent.

## Logs

The log is rotated. The service keeps seven days of logs.

See [the diagram](diagram.png) and [the plan](plan.md).
`

// Fix all rewrites every reword finding of one check with one model call for each section,
// drops a section whose rewrite still fails, and saves the accepted sections as one version.
func TestFixAll(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			pe := newPipeline(t, e, map[string]string{"orders/SPEC.md": passiveSDD, "orders/assets/diagram.png": "png"}, "fake-large")
			run, fs, _ := pe.latest(t, "orders")
			if n := len(findingsOf(fs, lint.PassiveVoice)); n != 4 {
				t.Fatalf("%d passive-voice findings, want 4", n)
			}
			a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews, Change: pe.bundles.Change}

			// The fix list: each finding has its lines and its fix kind. A broken link is a reword
			// finding only when one bundle file has the linked name.
			listed, err := a.ListFindings(ctx, api.ListFindingsRequestObject{RunId: run.ID})
			if err != nil {
				t.Fatal(err)
			}
			kinds := map[string]api.FixKind{}
			for _, f := range listed.(api.ListFindings200JSONResponse).Items {
				switch {
				case f.CheckSlug == lint.BrokenLink:
					kinds[f.Anchor.Quote] = f.FixKind
				case f.CheckSlug == lint.PassiveVoice && f.Anchor.Quote == "is rotated":
					if f.Line != 19 || f.EndLine != 19 || f.FixKind != api.Reword {
						t.Errorf("the finding in Logs: line %d to %d, kind %s; want line 19, reword", f.Line, f.EndLine, f.FixKind)
					}
				}
			}
			if kinds["the diagram"] != api.Reword || kinds["the plan"] != api.Answer {
				t.Errorf("broken link fix kinds = %v; want reword for the file the bundle has, answer for the other", kinds)
			}

			if _, err := a.SuggestFixes(ctx, api.SuggestFixesRequestObject{RunId: run.ID, Body: &api.SuggestFixesJSONRequestBody{CheckSlug: lint.Placeholder}}); err == nil {
				t.Fatal("Fix all ran on a check whose findings need a fact")
			}
			res, err := a.SuggestFixes(ctx, api.SuggestFixesRequestObject{RunId: run.ID, Body: &api.SuggestFixesJSONRequestBody{CheckSlug: lint.PassiveVoice}})
			if err != nil {
				t.Fatal(err)
			}
			got := res.(api.SuggestFixes200JSONResponse)
			if got.Calls != 2 || pe.fake.count(review.PromptFixAll) != 2 {
				t.Fatalf("%d calls for 2 sections with findings (the fake saw %d)", got.Calls, pe.fake.count(review.PromptFixAll))
			}
			// The fake cannot reword the Logs section, so Speccy drops that section.
			if len(got.Sections) != 1 || got.Sections[0].Findings != 3 || len(got.Dropped) != 1 || strings.Join(got.Dropped[0].HeadingPath, "/") != "Orders/Logs" {
				t.Fatalf("sections %+v, dropped %+v", got.Sections, got.Dropped)
			}
			sec := got.Sections[0]
			// The rewrite holds the text of the section, without its heading and the blank line under it.
			if !strings.HasPrefix(sec.Old, "The request is retried.") || sec.Line != 13 || !strings.Contains(sec.New, "The client retries the request. The client waits one second first.") {
				t.Fatalf("the rewrite of Retries: %+v", sec)
			}
			q := pe.bundles.DB.Queries()
			before, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "orders"})
			if disk, _ := os.ReadFile(filepath.Join(pe.dir, "orders", "SPEC.md")); string(disk) != passiveSDD {
				t.Fatal("Fix all changed the doc before an accept")
			}

			out, err := a.AcceptFixes(ctx, api.AcceptFixesRequestObject{RunId: run.ID, Body: &api.AcceptFixesJSONRequestBody{FindingIds: []uuid.UUID{sec.FindingId}}})
			if err != nil {
				t.Fatal(err)
			}
			w := out.(api.AcceptFixes200JSONResponse)
			if !w.Changed || w.Sections != 1 || w.Left != 0 || w.Version.Number != 2 {
				t.Fatalf("accept = %+v", w)
			}
			after, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "orders"})
			if after.CurrentVersionID == before.CurrentVersionID {
				t.Fatal("the accept made no version")
			}
			_, fs, _ = pe.latest(t, "orders")
			left := findingsOf(fs, lint.PassiveVoice)
			if len(left) != 1 || !strings.Contains(string(left[0].Anchor), "is rotated") {
				t.Fatalf("passive-voice findings after the accept: %+v", left)
			}
		})
	}
}
