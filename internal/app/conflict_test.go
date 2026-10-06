package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const (
	prdDoc = "---\ntype: note\ntitle: Pay PRD\n---\n\n# Pay PRD\n\n## Limits\n\nThe request limit is 10 a second.\n\n## Other\n\nThe service logs each call.\n"
	sddDoc = "---\ntype: note\ntitle: Pay SDD\nlinks:\n  - kind: implements\n    target: prd\n---\n\n# Pay SDD\n\n## Limits\n\nThe request limit is 5 a second.\n\n## Other\n\nThe service stores each call.\n"
)

// The answers to a conflict (#133): "The linked doc must change" lifts the conflict from the
// downstream verdict and gives the linked doc a MUST finding; "The downstream doc must change"
// makes the conflict block the downstream doc again, with its reason; an edit that removes a
// quote closes the conflict on both docs. coherence.upstream_pending: block keeps the block.
func TestConflict_UpstreamRequestAndSendBack(t *testing.T) {
	e := newEnv(t, storetest.Engines()[0])
	ctx := context.Background()
	a := e.app
	q := a.Bundles.DB.Queries()
	prdB, err := a.Bundles.CreateDB(ctx, "prd", []source.File{{Path: "NOTE.md", Content: []byte(prdDoc)}}, "author")
	if err != nil {
		t.Fatal(err)
	}
	sddB, err := a.Bundles.CreateDB(ctx, "sdd", []source.File{{Path: "NOTE.md", Content: []byte(sddDoc)}}, "author")
	if err != nil {
		t.Fatal(err)
	}
	prd, sdd := &env{app: a, b: prdB}, &env{app: a, b: sddB}
	id := kernel.NewID()
	if err := q.InsertBackend(ctx, pgdb.InsertBackendParams{ID: id, WorkspaceID: a.Workspace, Kind: model.KindFake, Name: "fake",
		Config: dbtype.JSON(`{}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: a.Workspace, Role: model.RoleReviewer, BackendID: id, Model: "fake-reviewer"}); err != nil {
		t.Fatal(err)
	}
	a.Reviews.Gateway.Fake = model.BackendFunc(func(_ context.Context, _ string, c model.Call) (model.Raw, error) {
		if c.PromptVersion == review.PromptContradiction {
			return model.Raw{Text: `{"conflicts":[{"analysis":"The SDD says 5, the PRD says 10.","both_can_hold":false,"explanation":"The two docs give a different request limit.","other_quote":"The request limit is 10 a second.","this_quote":"The request limit is 5 a second."}]}`}, nil
		}
		return model.Raw{Text: `{"matches":[]}`}, nil
	})
	fullReview := func(b pgdb.SpecDoc) {
		t.Helper()
		cur, _ := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: b.ID})
		run, err := a.Reviews.StartRun(ctx, cur, review.Stages{review.StageCoherence})
		if err != nil {
			t.Fatal(err)
		}
		for range 200 {
			r, _ := q.GetRunByID(ctx, run.ID)
			if r.Status == "failed" {
				t.Fatalf("the review failed: %s", r.Error)
			}
			if r.Status == "complete" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("the review did not end")
	}
	findings := func(e *env) []api.Finding {
		t.Helper()
		e.verdict(t)
		run, err := q.LatestRun(ctx, e.b.ID)
		if err != nil {
			t.Fatal(err)
		}
		res, err := a.API.ListFindings(as("author"), api.ListFindingsRequestObject{RunId: run.ID})
		if err != nil {
			t.Fatal(err)
		}
		return res.(api.ListFindings200JSONResponse).Items
	}
	finding := func(e *env, slug string) *api.Finding {
		t.Helper()
		for _, f := range findings(e) {
			if f.CheckSlug == slug {
				return &f
			}
		}
		return nil
	}
	answer := func(e *env, f uuid.UUID, body api.RequestWaiverJSONRequestBody) api.Waiver {
		t.Helper()
		body.FindingId = f
		res, err := a.API.RequestWaiver(as("author"), api.RequestWaiverRequestObject{DocId: e.b.ID, Body: &body})
		if err != nil {
			t.Fatal(err)
		}
		w := api.Waiver(res.(api.RequestWaiver200JSONResponse))
		out, err := a.API.ApproveWaiver(as("keeper"), api.ApproveWaiverRequestObject{WaiverId: w.Id})
		if err != nil {
			t.Fatal(err)
		}
		return api.Waiver(out.(api.ApproveWaiver200JSONResponse))
	}
	yes := true

	fullReview(sddB)
	c := finding(sdd, review.ContradictionSlug)
	if c == nil || c.Conflict == nil {
		t.Fatalf("no conflict after the review: %+v", findings(sdd))
	}
	if r, _ := sdd.verdict(t); r != "not_build_ready" {
		t.Fatalf("the SDD with a conflict: %s, want not_build_ready", r)
	}

	// The linked doc must change: the SDD no longer counts the conflict, and the PRD has it.
	if w := answer(sdd, c.Id, api.RequestWaiverJSONRequestBody{Reason: "The shipped code limits requests to 5 a second.", UpstreamChange: &yes}); w.Status != "approved" {
		t.Fatalf("the upstream request is %s", w.Status)
	}
	if d := sidecar(t, sdd); len(d.UpstreamChanges) != 1 || d.UpstreamChanges[0].Conflict.With != "prd" {
		t.Fatalf("the SDD sidecar %+v, want one upstream request of prd", d)
	}
	if r, must := sdd.verdict(t); r != "build_ready" || must != 0 {
		t.Errorf("after the upstream request: %s with %d MUST, want build_ready with 0", r, must)
	}
	if c = finding(sdd, review.ContradictionSlug); c == nil || c.UpstreamChange == nil || c.UpstreamChange.State != "waiting" || c.UpstreamChange.Upstream != "prd" {
		t.Fatalf("the SDD conflict %+v, want one that waits on prd", c)
	}
	down := finding(prd, review.DownstreamRequestSlug)
	if down == nil || down.Level != "MUST" || !strings.Contains(down.Message, "The request limit is 5 a second.") ||
		!strings.Contains(down.Message, "shipped code") || !strings.Contains(down.Anchor.Quote, "10 a second") {
		t.Fatalf("the PRD finding %+v, want a MUST downstream request that quotes both docs and the reason", down)
	}
	if r, _ := prd.verdict(t); r != "not_build_ready" {
		t.Errorf("the PRD with a downstream request: %s, want not_build_ready", r)
	}

	// A profile that blocks on a pending upstream request keeps the SDD Not Build Ready.
	setPending := func(v string) {
		t.Helper()
		if _, err := a.Profiles.Save(ctx, "note", []byte(profileYAML+"coherence: { upstream_pending: "+v+" }\n"), []byte("# Note\n"), "admin"); err != nil {
			t.Fatal(err)
		}
		if err := a.Reviews.EnsureLinted(ctx); err != nil {
			t.Fatal(err)
		}
	}
	setPending("block")
	if r, _ := sdd.verdict(t); r != "not_build_ready" {
		t.Errorf("with upstream_pending block: %s, want not_build_ready", r)
	}
	if c = finding(sdd, review.ContradictionSlug); c == nil || c.UpstreamChange == nil || !c.UpstreamChange.Blocks {
		t.Errorf("with upstream_pending block: the conflict %+v, want one that waits and blocks", c)
	}
	setPending("allow")
	if r, _ := sdd.verdict(t); r != "build_ready" {
		t.Errorf("with upstream_pending allow: %s, want build_ready", r)
	}

	// The downstream doc must change: the conflict blocks the SDD again, with the reason.
	down = finding(prd, review.DownstreamRequestSlug)
	if w := answer(prd, down.Id, api.RequestWaiverJSONRequestBody{Reason: "Finance signed off on 10 a second for launch.", SendBack: &yes}); w.Status != "approved" || w.Downstream == nil {
		t.Fatalf("the send-back %+v", w)
	}
	if r, _ := sdd.verdict(t); r != "not_build_ready" {
		t.Errorf("after the send-back: %s, want not_build_ready", r)
	}
	c = finding(sdd, review.ContradictionSlug)
	if c == nil || c.UpstreamChange == nil || c.UpstreamChange.State != "sent_back" || c.UpstreamChange.SentBackReason == nil ||
		!strings.Contains(*c.UpstreamChange.SentBackReason, "Finance") {
		t.Fatalf("after the send-back: the conflict %+v, want the send-back reason", c)
	}
	if f := finding(prd, review.DownstreamRequestSlug); f != nil {
		t.Errorf("after the send-back the PRD still has %+v", f)
	}
	ws, _ := a.API.ListWaivers(as("author"), api.ListWaiversRequestObject{DocId: sddB.ID})
	for _, w := range ws.(api.ListWaivers200JSONResponse).Items {
		if w.UpstreamChange != nil && (w.Status != "invalidated" || w.EndedBecause == nil || *w.EndedBecause != api.WaiverEndedBecauseSentBack) {
			t.Errorf("the upstream request after the send-back: %s %v, want ended because sent back", w.Status, w.EndedBecause)
		}
	}
	// No second upstream request after a send-back.
	if _, err := a.API.RequestWaiver(as("author"), api.RequestWaiverRequestObject{DocId: sddB.ID, Body: &api.RequestWaiverJSONRequestBody{
		FindingId: c.Id, Reason: "The shipped code limits requests to 5 a second.", UpstreamChange: &yes}}); err == nil {
		t.Error("a second upstream request after a send-back was accepted")
	}

	// An edit of the PRD that removes its quote closes the conflict.
	prd.edit2(t, "NOTE.md", strings.Replace(prdDoc, "is 10 a second", "is 5 a second, as the code ships", 1))
	if f := finding(sdd, review.ContradictionSlug); f != nil {
		t.Errorf("after the PRD edit the SDD still has %+v", f)
	}
	if r, _ := sdd.verdict(t); r != "build_ready" {
		t.Errorf("after the PRD edit: %s, want build_ready", r)
	}
}

// A conflict keeps its identity from run to run (#132). A conflict that the model words in
// another way keeps the earlier quotes, so its decisions still match it. A conflict that the
// model does not find again stays while both quotes are in their docs, and an edit that
// removes a quote ends it.
func TestConflict_KeepsItsIdentity(t *testing.T) {
	e := newEnv(t, storetest.Engines()[0])
	ctx := context.Background()
	a := e.app
	q := a.Bundles.DB.Queries()
	if _, err := a.Bundles.CreateDB(ctx, "prd", []source.File{{Path: "NOTE.md", Content: []byte(prdDoc)}}, "author"); err != nil {
		t.Fatal(err)
	}
	sddB, err := a.Bundles.CreateDB(ctx, "sdd", []source.File{{Path: "NOTE.md", Content: []byte(sddDoc)}}, "author")
	if err != nil {
		t.Fatal(err)
	}
	sdd := &env{app: a, b: sddB}
	id := kernel.NewID()
	if err := q.InsertBackend(ctx, pgdb.InsertBackendParams{ID: id, WorkspaceID: a.Workspace, Kind: model.KindFake, Name: "fake",
		Config: dbtype.JSON(`{}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: a.Workspace, Role: model.RoleReviewer, BackendID: id, Model: "fake-reviewer"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	conflicts, sameCalls := "", 0
	set := func(c string) { mu.Lock(); conflicts = c; mu.Unlock() }
	calls := func() int { mu.Lock(); defer mu.Unlock(); return sameCalls }
	a.Reviews.Gateway.Fake = model.BackendFunc(func(_ context.Context, _ string, c model.Call) (model.Raw, error) {
		mu.Lock()
		defer mu.Unlock()
		if c.PromptVersion == review.PromptContradiction {
			return model.Raw{Text: `{"conflicts":[` + conflicts + `]}`}, nil
		}
		sameCalls++
		return model.Raw{Text: `{"matches":[{"analysis":"Both are about the request limit.","earlier":1,"new":1}]}`}, nil
	})
	review1 := func() {
		t.Helper()
		cur, _ := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: sddB.ID})
		run, err := a.Reviews.StartRun(ctx, cur, review.Stages{review.StageCoherence})
		if err != nil {
			t.Fatal(err)
		}
		for range 200 {
			if r, _ := q.GetRunByID(ctx, run.ID); r.Status == "complete" || r.Status == "failed" {
				if r.Status == "failed" {
					t.Fatalf("the review failed: %s", r.Error)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("the review did not end")
	}
	quotes := func() []string {
		t.Helper()
		sdd.verdict(t)
		run, _ := q.LatestRun(ctx, sddB.ID)
		res, err := a.API.ListFindings(as("author"), api.ListFindingsRequestObject{RunId: run.ID})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range res.(api.ListFindings200JSONResponse).Items {
			if f.Conflict != nil {
				out = append(out, f.Conflict.Quote+" | "+f.Conflict.WithQuote)
			}
		}
		return out
	}
	const first = "The request limit is 5 a second. | The request limit is 10 a second."
	set(`{"analysis":"5 against 10.","both_can_hold":false,"explanation":"The limits differ.","other_quote":"The request limit is 10 a second.","this_quote":"The request limit is 5 a second."}`)
	review1()
	if got := quotes(); len(got) != 1 || got[0] != first {
		t.Fatalf("after the first review: %q", got)
	}
	// An edit elsewhere, and the model quotes less of the same text: the earlier quotes stay.
	main, _ := bundleFiles(t, sdd)
	sdd.edit2(t, "NOTE.md", strings.Replace(string(main), "stores each call", "stores each call for a year", 1))
	set(`{"analysis":"5 against 10.","both_can_hold":false,"explanation":"The SDD and the PRD give a different limit.","other_quote":"limit is 10 a second","this_quote":"limit is 5 a second"}`)
	review1()
	if got := quotes(); len(got) != 1 || got[0] != first || calls() != 1 {
		t.Fatalf("after a reworded answer: %q with %d match calls, want the first quotes and 1 call", got, calls())
	}
	// The model misses the conflict: it stays while both quotes are in the docs.
	main, _ = bundleFiles(t, sdd)
	sdd.edit2(t, "NOTE.md", strings.Replace(string(main), "for a year", "for two years", 1))
	set("")
	review1()
	if got := quotes(); len(got) != 1 || got[0] != first {
		t.Fatalf("after an answer with no conflict: %q, want the conflict carried", got)
	}
	// An edit that removes the quote ends it.
	main, _ = bundleFiles(t, sdd)
	sdd.edit2(t, "NOTE.md", strings.Replace(string(main), "is 5 a second", "is 10 a second", 1))
	review1()
	if got := quotes(); len(got) != 0 {
		t.Fatalf("after the edit that removes the quote: %q, want no conflict", got)
	}
}
