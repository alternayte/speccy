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

// An Acknowledgement goes in the sidecar on approval, and a withdrawal takes it out at once:
// "Mark it standalone" answers links.has-upstream, and Withdraw brings back the finding and the
// coverage gap, which the verdict counts again. The waiver records who withdrew it.
func TestAcknowledgements_StandaloneAndWithdraw(t *testing.T) {
	prd := "---\ntype: prd\n---\n# Refunds\n\n## Requirements\n\n| ID | Requirement |\n|---|---|\n| REQ-001 | Refund a paid order. |\n| REQ-002 | Email the customer. |\n"
	sddDoc := "---\ntype: sdd\nlinks:\n  - kind: implements\n    target: PRD.md\n---\n# Refunds design\n\n## Design\n\nThe order page gets a Refund button. Covers REQ-001.\n"
	alone := "---\ntype: sdd\n---\n# Cache design\n\n## Design\n\nThe cache keeps each page for 5 minutes.\n"
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := kernel.WithActor(context.Background(), kernel.LocalActor)
			en := newEnv(t, e, map[string]string{"refunds/PRD.md": prd, "refunds/SDD.md": sddDoc, "cache/SDD.md": alone})
			q := en.bundles.DB.Queries()
			doc := func(slug string) pgdb.SpecDoc {
				b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: slug})
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			w := &waiver.API{DB: en.bundles.DB, ES: es.New(en.bundles.DB, map[string][]es.Projection{waiver.StreamType: {waiver.Projection(en.bundles.Workspace)}}),
				Workspace: en.bundles.Workspace, Profiles: en.reviews.Profiles, Change: en.bundles.Change,
				Decisions: en.bundles.Decisions, SetDecisions: en.bundles.SetDecisions}
			sidecar := func(docPath string) string {
				raw, _ := os.ReadFile(filepath.Join(en.dir, ".speccy", "decisions", filepath.FromSlash(docPath)+".yaml"))
				return string(raw)
			}
			approve := func(body api.RequestWaiverJSONRequestBody, slug string) api.Waiver {
				t.Helper()
				res, err := w.RequestWaiver(ctx, api.RequestWaiverRequestObject{DocId: doc(slug).ID, Body: &body})
				if err != nil {
					t.Fatal(err)
				}
				req := res.(api.RequestWaiver200JSONResponse)
				if _, err := w.ApproveWaiver(ctx, api.ApproveWaiverRequestObject{WaiverId: req.Id}); err != nil {
					t.Fatal(err)
				}
				return api.Waiver(req)
			}
			withdraw := func(slug string, kind api.WithdrawAcknowledgementJSONBodyKind, id *string) {
				t.Helper()
				if _, err := w.WithdrawAcknowledgement(ctx, api.WithdrawAcknowledgementRequestObject{DocId: doc(slug).ID,
					Body: &api.WithdrawAcknowledgementJSONRequestBody{Kind: kind, TraceId: id}}); err != nil {
					t.Fatal(err)
				}
			}
			status := func(id string) api.Waiver {
				t.Helper()
				res, err := w.ListWaivers(ctx, api.ListWaiversRequestObject{DocId: doc("cache").ID})
				if err != nil {
					t.Fatal(err)
				}
				other, err := w.ListWaivers(ctx, api.ListWaiversRequestObject{DocId: doc("refunds/SDD").ID})
				if err != nil {
					t.Fatal(err)
				}
				for _, x := range append(res.(api.ListWaivers200JSONResponse).Items, other.(api.ListWaivers200JSONResponse).Items...) {
					if x.Id.String() == id {
						return x
					}
				}
				t.Fatalf("no waiver %s", id)
				return api.Waiver{}
			}

			// Mark it standalone: a request, then the approval writes standalone: to the sidecar.
			_, fs, _ := en.latest(t, "cache")
			up := findingsOf(fs, review.HasUpstreamSlug)
			if len(up) != 1 {
				t.Fatalf("an SDD with no link has %d links.has-upstream findings, want 1", len(up))
			}
			yes := true
			reason := "The cache is an internal change with no product doc."
			if _, err := w.RequestWaiver(ctx, api.RequestWaiverRequestObject{DocId: doc("refunds/SDD").ID, Body: &api.RequestWaiverJSONRequestBody{
				FindingId: up[0].ID, Reason: reason, Standalone: &yes}}); err == nil {
				t.Error("a standalone answer on another doc's finding was accepted")
			}
			alone := approve(api.RequestWaiverJSONRequestBody{FindingId: up[0].ID, Reason: reason, Standalone: &yes}, "cache")
			if alone.Standalone == nil || !*alone.Standalone || len(alone.Section) != 0 {
				t.Errorf("the request = %+v, want a standalone Acknowledgement of the whole doc", alone)
			}
			if s := sidecar("cache/SDD.md"); !strings.Contains(s, "standalone:\n  reason: "+reason) || !strings.Contains(s, "acknowledged_by: ") {
				t.Fatalf("the sidecar after the approval:\n%s", s)
			}
			if _, fs, _ := en.latest(t, "cache"); len(findingsOf(fs, review.HasUpstreamSlug)) != 0 {
				t.Error("links.has-upstream still fails after the approved standalone Acknowledgement")
			}

			// Withdraw: standalone leaves the sidecar at once, and the finding comes back.
			withdraw("cache", api.WithdrawAcknowledgementJSONBodyKindStandalone, nil)
			if s := sidecar("cache/SDD.md"); strings.Contains(s, "standalone") {
				t.Errorf("the sidecar after the withdrawal:\n%s", s)
			}
			if _, fs, _ := en.latest(t, "cache"); len(findingsOf(fs, review.HasUpstreamSlug)) != 1 {
				t.Error("links.has-upstream passes after the standalone Acknowledgement was withdrawn")
			}
			if got := status(alone.Id.String()); got.Status != api.WaiverStatusWithdrawn || got.WithdrawnBy == nil || *got.WithdrawnBy == "" {
				t.Errorf("the withdrawn Acknowledgement = %+v, want status withdrawn and who withdrew it", got)
			}

			// A trace Acknowledgement: the gap closes on approval, and a withdrawal reopens it in
			// the sidecar, the verdict and the matrix.
			_, fs, _ = en.latest(t, "refunds/SDD")
			gaps := findingsOf(fs, review.CoverageSlug)
			if len(gaps) != 1 || !strings.Contains(gaps[0].Message, "REQ-002 ") {
				t.Fatalf("coverage findings %+v, want one gap for REQ-002", gaps)
			}
			ack := approve(api.RequestWaiverJSONRequestBody{FindingId: gaps[0].ID, Reason: "The mail service sends the refund email.",
				Trace: &api.TraceAnswer{Status: api.TraceAnswerStatusOutOfScope}}, "refunds/SDD")
			if s := sidecar("refunds/SDD.md"); !strings.Contains(s, "id: REQ-002") {
				t.Fatalf("the sidecar after the approval:\n%s", s)
			}
			if _, fs, _ := en.latest(t, "refunds/SDD"); len(findingsOf(fs, review.CoverageSlug)) != 0 {
				t.Fatal("REQ-002 is still a gap after the approved Acknowledgement")
			}
			id := "REQ-002"
			withdraw("refunds/SDD", api.WithdrawAcknowledgementJSONBodyKindTrace, &id)
			if s := sidecar("refunds/SDD.md"); strings.Contains(s, "REQ-002") {
				t.Errorf("the sidecar after the withdrawal:\n%s", s)
			}
			_, fs, v := en.latest(t, "refunds/SDD")
			gaps = findingsOf(fs, review.CoverageSlug)
			if len(gaps) != 1 || !strings.Contains(string(v.BlockingFindingIds), gaps[0].ID.String()) || v.Result == "build_ready" {
				t.Errorf("after the withdrawal: gaps %+v, verdict %s blocking %s; want the REQ-002 gap to block again", gaps, v.Result, v.BlockingFindingIds)
			}
			if got := status(ack.Id.String()); got.Status != api.WaiverStatusWithdrawn {
				t.Errorf("the withdrawn Acknowledgement has status %s", got.Status)
			}
			a := &review.API{DB: en.bundles.DB, Workspace: en.bundles.Workspace, Service: en.reviews, Change: en.bundles.Change}
			tr, err := a.GetTrace(ctx, api.GetTraceRequestObject{DocId: doc("refunds/PRD").ID})
			if err != nil {
				t.Fatal(err)
			}
			m := tr.(api.GetTrace200JSONResponse).Matrices
			if len(m) != 1 || len(m[0].Cells) != 2 || m[0].Cells[1][0].State != api.TraceCellStateGap {
				t.Fatalf("matrix = %+v, want REQ-002 a gap again", m)
			}
			if c := m[0].Cells[1][0]; c.FindingId == nil || len(gaps) == 1 && *c.FindingId != gaps[0].ID {
				t.Errorf("the gap cell names finding %v, want the REQ-002 coverage finding, so Answer can close it", c.FindingId)
			}
			if len(m[0].Editable) != 1 || !m[0].Editable[0] || m[0].Columns[0].BundleId != doc("refunds/SDD").BundleID {
				t.Errorf("columns %+v editable %v, want the SDD with its bundle, editable by the local user", m[0].Columns, m[0].Editable)
			}

			// A second withdrawal finds nothing to take out.
			if _, err := w.WithdrawAcknowledgement(ctx, api.WithdrawAcknowledgementRequestObject{DocId: doc("refunds/SDD").ID,
				Body: &api.WithdrawAcknowledgementJSONRequestBody{Kind: api.WithdrawAcknowledgementJSONBodyKindTrace, TraceId: &id}}); err == nil {
				t.Error("a withdrawal of an acknowledgement the sidecar does not hold was accepted")
			}
		})
	}
}
