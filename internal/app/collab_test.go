package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/app"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// people is a fixed directory: an author, a member, and a maintainer.
type people struct{}

func (people) People(context.Context) ([]kernel.Person, error) {
	return []kernel.Person{
		{ID: "author", Name: "Ann Author", Email: "ann@x.test", Role: kernel.RoleMember},
		{ID: "member", Name: "Max Member", Email: "max@x.test", Role: kernel.RoleMember},
		{ID: "keeper", Name: "Kim Keeper", Email: "kim@x.test", Role: kernel.RoleMember},
	}, nil
}

func as(id string) context.Context {
	return kernel.WithActor(context.Background(), kernel.Actor{UserID: id, Role: kernel.RoleMember})
}

// A profile with no required headings and no upstream link, so a placeholder is the only MUST
// finding. MUST waivers need a maintainer.
const profileYAML = `key: note
name: Note
template: note.md
waivers: { should: any_member, must: maintainer }
approvals: { required: 1 }
checks: []
`

const doc = "---\ntype: note\ntitle: Pay\n---\n\n# Pay\n\n## Limits\n\nThe request limit is TBD for now.\n\n## Other\n\nThe service logs each call.\n"

type env struct {
	app *app.App
	b   pgdb.Bundle
}

func newEnv(t *testing.T, e storetest.Engine) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := e.Open(t)
	a, err := app.New(ctx, db, nil, app.Options{People: people{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Profiles.Save(ctx, "note", []byte(profileYAML), []byte("# Note\n"), "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.API.SetMaintainers(kernel.WithActor(ctx, kernel.Actor{UserID: "admin", Role: kernel.RoleAdmin}),
		api.SetMaintainersRequestObject{Key: "note", Body: &api.SetMaintainersJSONRequestBody{UserIds: []string{"keeper"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := a.Bundles.CreateDB(ctx, "pay", []source.File{{Path: "NOTE.md", Content: []byte(doc)}}, "author")
	if err != nil {
		t.Fatal(err)
	}
	return &env{app: a, b: b}
}

// verdict returns the bundle's verdict now: result, open MUST findings, blocking threads.
func (e *env) verdict(t *testing.T) (string, int) {
	t.Helper()
	q := e.app.Bundles.DB.Queries()
	b, err := q.GetBundle(context.Background(), pgdb.GetBundleParams{WorkspaceID: e.app.Workspace, ID: e.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.b = b
	v, _, err := review.Summary(context.Background(), q, b)
	if err != nil || v == nil {
		t.Fatalf("summary: %v %v", v, err)
	}
	return string(v.Result), v.Must
}

func (e *env) mustFinding(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	q := e.app.Bundles.DB.Queries()
	run, err := q.LatestRun(context.Background(), e.b.ID)
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := q.ListFindings(context.Background(), run.ID)
	for _, f := range fs {
		if f.CheckSlug == slug && f.Level == "MUST" {
			return f.ID
		}
	}
	t.Fatalf("no MUST %s finding in %d findings", slug, len(fs))
	return uuid.Nil
}

func (e *env) edit(t *testing.T, content string) {
	t.Helper()
	q := e.app.Bundles.DB.Queries()
	b, _ := q.GetBundle(context.Background(), pgdb.GetBundleParams{WorkspaceID: e.app.Workspace, ID: e.b.ID})
	if _, _, err := e.app.Bundles.Change(as("author"), b.ID, b.CurrentVersionID.UUID,
		source.Op{Kind: source.OpWrite, Path: "NOTE.md", Content: []byte(content)}, "author", "Edit"); err != nil {
		t.Fatal(err)
	}
}

// waive requests a waiver of the placeholder as the author and approves it as the maintainer.
func (e *env) waive(t *testing.T) api.Waiver {
	t.Helper()
	res, err := e.app.API.RequestWaiver(as("author"), api.RequestWaiverRequestObject{BundleId: e.b.ID,
		Body: &api.RequestWaiverJSONRequestBody{FindingId: e.mustFinding(t, "lint.placeholder"), Reason: "The provider sets this limit; the asset lists it."}})
	if err != nil {
		t.Fatal(err)
	}
	w := api.Waiver(res.(api.RequestWaiver200JSONResponse))
	if _, err := e.app.API.ApproveWaiver(as("member"), api.ApproveWaiverRequestObject{WaiverId: w.Id}); err == nil {
		t.Fatal("a member who is not a maintainer approved a MUST waiver")
	}
	out, err := e.app.API.ApproveWaiver(as("keeper"), api.ApproveWaiverRequestObject{WaiverId: w.Id})
	if err != nil {
		t.Fatal(err)
	}
	return api.Waiver(out.(api.ApproveWaiver200JSONResponse))
}

// T-002 through the app: an approved waiver of the only MUST finding gives Build Ready, and it
// lives in the frontmatter (DEC-009).
func TestWaiver_ApprovedWaiverPasses(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			if r, must := e.verdict(t); r != "not_build_ready" || must != 1 {
				t.Fatalf("before the waiver: %s with %d MUST, want not_build_ready with 1", r, must)
			}
			if w := e.waive(t); w.Status != "approved" {
				t.Fatalf("waiver status %s", w.Status)
			}
			if r, must := e.verdict(t); r != "build_ready" || must != 0 {
				t.Errorf("after the waiver: %s with %d open MUST, want build_ready with 0", r, must)
			}
			dec := sidecar(t, e)
			if len(dec.Waivers) != 1 || dec.Waivers[0].Check != "lint.placeholder" || dec.Waivers[0].RequestedBy != "Ann Author" {
				t.Errorf("sidecar waivers %+v", dec.Waivers)
			}
		})
	}
}

// T-010
func TestWaiver_InvalidatedOnSectionEdit(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			e.waive(t)
			main, _ := bundleFiles(t, e)
			// An edit elsewhere keeps the waiver.
			e.edit(t, strings.Replace(string(main), "logs each call", "logs each call and each retry", 1))
			if r, _ := e.verdict(t); r != "build_ready" {
				t.Fatalf("after an edit in another section: %s, want build_ready", r)
			}
			// An edit of the waived section ends it, and the check counts again.
			main, _ = bundleFiles(t, e)
			e.edit(t, strings.Replace(string(main), "is TBD for now", "is TBD until June", 1))
			if r, must := e.verdict(t); r != "not_build_ready" || must != 1 {
				t.Errorf("after an edit in the waived section: %s with %d MUST, want not_build_ready with 1", r, must)
			}
			ws, _ := e.app.API.ListWaivers(as("author"), api.ListWaiversRequestObject{BundleId: e.b.ID})
			items := ws.(api.ListWaivers200JSONResponse).Items
			if len(items) != 1 || items[0].Status != "invalidated" {
				t.Errorf("waivers after the edit: %+v", items)
			}
		})
	}
}

// sidecar returns the bundle's sidecar: its approved waivers and acknowledgements (DEC-009).
func sidecar(t *testing.T, e *env) source.Decisions {
	t.Helper()
	e.verdict(t)
	dec, err := e.app.Bundles.Decisions(context.Background(), e.b)
	if err != nil {
		t.Fatal(err)
	}
	return dec
}

func bundleFiles(t *testing.T, e *env) ([]byte, error) {
	t.Helper()
	e.verdict(t) // refresh e.b
	files, err := version.Files(context.Background(), e.app.Bundles.DB.Queries(), e.b.CurrentVersionID.UUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "NOTE.md" {
			return f.Content, nil
		}
	}
	t.Fatal("no NOTE.md")
	return nil, nil
}

// fixed makes the doc Build Ready: it replaces the placeholder.
func fixed(e *env, t *testing.T) {
	main, _ := bundleFiles(t, e)
	e.edit(t, strings.Replace(string(main), "is TBD for now", "is 100 requests a second", 1))
}

// T-012
func TestApproval_AuthorCannotApprove(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			fixed(e, t)
			if r, _ := e.verdict(t); r != "build_ready" {
				t.Fatalf("fixture verdict %s", r)
			}
			if _, err := e.app.API.RequestReview(as("member"), api.RequestReviewRequestObject{BundleId: e.b.ID,
				Body: &api.RequestReviewJSONRequestBody{Reviewers: []string{"member"}}}); err == nil {
				t.Error("a member who is not an author requested a review")
			}
			if _, err := e.app.API.RequestReview(as("author"), api.RequestReviewRequestObject{BundleId: e.b.ID,
				Body: &api.RequestReviewJSONRequestBody{Reviewers: []string{"member"}}}); err != nil {
				t.Fatal(err)
			}
			_, err := e.app.API.ApproveBundle(as("author"), api.ApproveBundleRequestObject{BundleId: e.b.ID})
			if ke, ok := kernel.AsError(err); !ok || ke.Code != "author_cannot_approve" {
				t.Errorf("the author approved: %v", err)
			}
			res, err := e.app.API.ApproveBundle(as("member"), api.ApproveBundleRequestObject{BundleId: e.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			if st := api.BundleStatus(res.(api.ApproveBundle200JSONResponse)); st.Status != "approved" {
				t.Errorf("status after the reviewer approved: %s", st.Status)
			}
		})
	}
}

// T-013
func TestApproval_EditRevokes(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			fixed(e, t)
			if _, err := e.app.API.RequestReview(as("author"), api.RequestReviewRequestObject{BundleId: e.b.ID,
				Body: &api.RequestReviewJSONRequestBody{Reviewers: []string{"member"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.app.API.ApproveBundle(as("member"), api.ApproveBundleRequestObject{BundleId: e.b.ID}); err != nil {
				t.Fatal(err)
			}
			main, _ := bundleFiles(t, e)
			e.edit(t, strings.Replace(string(main), "logs each call", "logs each call twice", 1))
			res, err := e.app.API.GetBundleStatus(as("member"), api.GetBundleStatusRequestObject{BundleId: e.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			st := api.BundleStatus(res.(api.GetBundleStatus200JSONResponse))
			if st.Status != "in_review" || len(st.Approvals) != 0 {
				t.Errorf("after an edit: status %s with %d approvals, want in_review with 0", st.Status, len(st.Approvals))
			}
		})
	}
}

// T-004 through the app: an open blocking thread makes a Build Ready bundle Not Build Ready at
// once; resolving it restores Build Ready. A guest cannot mark a thread blocking.
func TestThread_BlockingThreadBlocks(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			fixed(e, t)
			blocking := true
			res, err := e.app.API.OpenBundleThread(as("member"), api.OpenBundleThreadRequestObject{BundleId: e.b.ID, Body: &api.OpenThread{
				AnchorKind: api.OpenThreadAnchorKindSection, Anchor: map[string]any{"heading_path": []string{"Pay", "Limits"}},
				AddressedTo: api.OpenThreadAddressedToHumans, Body: "Who owns the limit? @ann please decide.", Blocking: &blocking}})
			if err != nil {
				t.Fatal(err)
			}
			th := api.ThreadDetail(res.(api.OpenBundleThread200JSONResponse))
			if r, _ := e.verdict(t); r != "not_build_ready" {
				t.Errorf("with an open blocking thread: %s", r)
			}
			// The author's inbox shows the mention.
			in, err := e.app.API.GetInbox(as("author"), api.GetInboxRequestObject{})
			if err != nil {
				t.Fatal(err)
			}
			items := in.(api.GetInbox200JSONResponse).Items
			if len(items) == 0 || items[0].Kind != api.InboxItemKindMention {
				t.Errorf("author inbox %+v, want the mention first", items)
			}
			guest := kernel.WithActor(context.Background(), kernel.Actor{Guest: &kernel.Guest{ID: uuid.New(), BundleID: e.b.ID, Name: "G"}})
			if _, err := e.app.API.SetThreadBlocking(guest, api.SetThreadBlockingRequestObject{ThreadId: th.Id, Body: &api.SetThreadBlockingJSONRequestBody{Blocking: false}}); err == nil {
				t.Error("a guest changed blocking")
			}
			if _, err := e.app.API.SetThreadStatus(as("author"), api.SetThreadStatusRequestObject{ThreadId: th.Id, Body: &api.SetThreadStatusJSONRequestBody{Open: false}}); err != nil {
				t.Fatal(err)
			}
			if r, _ := e.verdict(t); r != "build_ready" {
				t.Errorf("after resolving the thread: %s", r)
			}
		})
	}
}

// REQ-088, REQ-035: a message in a thread for the AI gets an answer from the writer role, with
// its sources; the docs reach the prompt as data. A guest cannot ask the AI.
func TestThread_AIAnswers(t *testing.T) {
	e := newEnv(t, storetest.Engines()[0])
	ctx := context.Background()
	q := e.app.Bundles.DB.Queries()
	id := kernel.NewID()
	if err := q.InsertBackend(ctx, pgdb.InsertBackendParams{ID: id, WorkspaceID: e.app.Workspace, Kind: model.KindFake, Name: "fake",
		Config: dbtype.JSON(`{}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: e.app.Workspace, Role: model.RoleWriter, BackendID: id, Model: "fake-writer"}); err != nil {
		t.Fatal(err)
	}
	var prompt string
	e.app.Reviews.Gateway.Fake = model.BackendFunc(func(_ context.Context, _ string, c model.Call) (model.Raw, error) {
		prompt = c.Prompt
		return model.Raw{Text: `{"answer":"The doc says the limit is TBD. Stripe allows 100 per second.","sources":["https://stripe.com/docs/rate-limits"]}`}, nil
	})
	res, err := e.app.API.OpenBundleThread(as("member"), api.OpenBundleThreadRequestObject{BundleId: e.b.ID, Body: &api.OpenThread{
		AnchorKind: api.OpenThreadAnchorKindSection, Anchor: map[string]any{"heading_path": []string{"Pay", "Limits"}},
		AddressedTo: api.OpenThreadAddressedToAi, Body: "What is the request limit?"}})
	if err != nil {
		t.Fatal(err)
	}
	th := api.ThreadDetail(res.(api.OpenBundleThread200JSONResponse))
	var got api.ThreadDetail
	for range 100 {
		r, err := e.app.API.GetThread(as("member"), api.GetThreadRequestObject{ThreadId: th.Id})
		if err != nil {
			t.Fatal(err)
		}
		got = api.ThreadDetail(r.(api.GetThread200JSONResponse))
		if len(got.Messages) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got.Messages) != 2 || got.Messages[1].AuthorKind != "ai" || len(got.Messages[1].Sources) != 1 {
		t.Fatalf("thread messages %+v", got.Messages)
	}
	if !strings.Contains(prompt, "The request limit is TBD for now.") || !strings.Contains(prompt, "<<<DATA") {
		t.Errorf("the prompt lacks the doc as data:\n%s", prompt)
	}
	// The thread is anchored to a section, so the prompt names that section (#56).
	if !strings.Contains(prompt, "The section the thread is about: Pay > Limits") {
		t.Errorf("the prompt lacks the thread's anchor:\n%s", prompt)
	}
	guest := kernel.WithActor(ctx, kernel.Actor{Guest: &kernel.Guest{ID: uuid.New(), BundleID: e.b.ID, Name: "G"}})
	if _, err := e.app.API.PostMessage(guest, api.PostMessageRequestObject{ThreadId: th.Id, Body: &api.PostMessageJSONRequestBody{Body: "And?"}}); err == nil {
		t.Error("a guest asked the AI")
	}
}
