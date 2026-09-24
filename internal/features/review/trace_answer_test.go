package review_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/waiver"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// The three answers to a coverage gap each close it in the verdict and in the matrix: "This
// doc covers it" writes the reference into the section, and "Out of scope" is an
// Acknowledgement that goes in the sidecar on approval.
func TestTraceAnswers_CloseGaps(t *testing.T) {
	prd := "---\ntype: prd\n---\n# Refunds\n\n## Requirements\n\n| ID | Requirement |\n|---|---|\n| REQ-001 | Refund a paid order. |\n| REQ-002 | Email the customer. |\n"
	sddDoc := "---\ntype: sdd\nlinks:\n  - kind: implements\n    target: PRD.md\n---\n# Refunds design\n\n## Design\n\nThe order page gets a Refund button.\n\n## Non-goals\n\nNo partial refunds.\n"
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := kernel.WithActor(context.Background(), kernel.LocalActor)
			en := newEnv(t, e, map[string]string{"refunds/PRD.md": prd, "refunds/SDD.md": sddDoc})
			q := en.bundles.DB.Queries()
			doc := func() pgdb.SpecDoc {
				b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: "refunds/SDD"})
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			gap := func(id string) (pgdb.ReviewRun, pgdb.Finding, bool) {
				run, fs, _ := en.latest(t, "refunds/SDD")
				for _, f := range findingsOf(fs, review.CoverageSlug) {
					if strings.Contains(f.Message, id+" ") {
						return run, f, true
					}
				}
				return run, pgdb.Finding{}, false
			}
			if _, _, ok := gap("REQ-001"); !ok {
				t.Fatal("a table row with REQ-001 in the first cell gave no gap")
			}
			a := &review.API{DB: en.bundles.DB, Workspace: en.bundles.Workspace, Service: en.reviews, Change: en.bundles.Change}
			b := doc()
			if _, err := a.CoverTraceId(ctx, api.CoverTraceIdRequestObject{DocId: b.ID, Params: api.CoverTraceIdParams{BaseVersion: b.CurrentVersionID.UUID},
				Body: &api.CoverTraceIdJSONRequestBody{TraceId: "REQ-001", Section: []string{"Refunds design", "Design"}}}); err != nil {
				t.Fatal(err)
			}
			disk, _ := os.ReadFile(filepath.Join(en.dir, "refunds", "SDD.md"))
			if !strings.Contains(string(disk), "The order page gets a Refund button.\n\nCovers REQ-001.\n\n## Non-goals") {
				t.Fatalf("the SDD after the answer:\n%s", disk)
			}
			if _, _, ok := gap("REQ-001"); ok {
				t.Error("REQ-001 is still a gap after this doc covers it")
			}

			_, f, ok := gap("REQ-002")
			if !ok {
				t.Fatal("REQ-002 is not a gap")
			}
			w := &waiver.API{DB: en.bundles.DB, ES: es.New(en.bundles.DB, map[string][]es.Projection{waiver.StreamType: {waiver.Projection(en.bundles.Workspace)}}), Workspace: en.bundles.Workspace, Profiles: en.reviews.Profiles,
				Change: en.bundles.Change, Decisions: en.bundles.Decisions, SetDecisions: en.bundles.SetDecisions}
			b = doc()
			if _, err := w.RequestWaiver(ctx, api.RequestWaiverRequestObject{DocId: b.ID, Body: &api.RequestWaiverJSONRequestBody{FindingId: f.ID,
				Reason: "The mail service sends the refund email."}}); err == nil {
				t.Fatal("a plain waiver of a coverage gap was accepted")
			}
			res, err := w.RequestWaiver(ctx, api.RequestWaiverRequestObject{DocId: b.ID, Body: &api.RequestWaiverJSONRequestBody{FindingId: f.ID,
				Reason: "The mail service sends the refund email.", Trace: &api.TraceAnswer{Status: api.TraceAnswerStatusOutOfScope}}})
			if err != nil {
				t.Fatal(err)
			}
			req := res.(api.RequestWaiver200JSONResponse)
			if req.Trace == nil || req.Trace.Id != "REQ-002" {
				t.Fatalf("the request = %+v, want an Acknowledgement of REQ-002", req)
			}
			if _, err := w.ApproveWaiver(ctx, api.ApproveWaiverRequestObject{WaiverId: req.Id}); err != nil {
				t.Fatal(err)
			}
			side, _ := os.ReadFile(filepath.Join(en.dir, ".speccy", "decisions", "refunds", "SDD.md.yaml"))
			if !strings.Contains(string(side), "id: REQ-002") || !strings.Contains(string(side), "status: out_of_scope") {
				t.Fatalf("the sidecar:\n%s", side)
			}
			if _, _, ok := gap("REQ-002"); ok {
				t.Error("REQ-002 is still a gap after the approved Acknowledgement")
			}
			prdDoc, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: "refunds/PRD"})
			tr, err := a.GetTrace(ctx, api.GetTraceRequestObject{DocId: prdDoc.ID})
			if err != nil {
				t.Fatal(err)
			}
			m := tr.(api.GetTrace200JSONResponse).Matrices
			if len(m) != 1 || len(m[0].Cells) != 2 || m[0].Cells[0][0].State != api.TraceCellStateReferenced || m[0].Cells[1][0].State != api.TraceCellStateOutOfScope {
				t.Errorf("matrix = %+v, want REQ-001 referenced and REQ-002 out of scope", m)
			}
		})
	}
}
