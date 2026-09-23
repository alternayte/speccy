package thread

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// GuestPostsPerHour is REQ-086's limit on a guest's posts.
const GuestPostsPerHour = 30

// API serves threads.
type API struct {
	DB        *store.DB
	ES        *es.Store
	Workspace uuid.UUID
	People    kernel.Directory
	// Ask queues an AI answer for a thread (REQ-088). Nil when no worker runs.
	Ask func(ctx context.Context, thread uuid.UUID) error
	// Answering reports whether an AI answer for a thread is queued or running.
	Answering func(ctx context.Context, thread uuid.UUID) bool
}

// AuthorOf is the author of the request's actor.
func AuthorOf(ctx context.Context, d kernel.Directory) Author {
	a := kernel.ActorFrom(ctx)
	if a.Guest != nil {
		return Author{Kind: "guest", ID: a.Guest.ID.String(), Name: a.Guest.Name}
	}
	p := kernel.PersonByID(ctx, d, a.UserID)
	if p.Name == p.ID && a.Email != "" {
		p.Name = a.Email
	}
	return Author{Kind: "user", ID: a.UserID, Name: p.Label()}
}

func (a *API) run(ctx context.Context, id uuid.UUID, decide func(State) ([]es.Event, error)) (State, error) {
	return es.Run(ctx, a.ES, StreamType, id, decide, Evolve)
}

// guestLimit refuses a guest's post over REQ-086's limit.
func (a *API) guestLimit(ctx context.Context, by Author) error {
	if by.Kind != "guest" {
		return nil
	}
	n, err := a.DB.Queries().CountAuthorMessagesSince(ctx, pgdb.CountAuthorMessagesSinceParams{AuthorID: by.ID, Since: time.Now().UTC().Add(-time.Hour)})
	if err != nil {
		return err
	}
	if n >= GuestPostsPerHour {
		return &kernel.Error{Status: 429, Code: "rate_limited", Detail: "A guest can post 30 messages an hour. Wait, then try again."}
	}
	return nil
}

func (a *API) open(ctx context.Context, bundleID *uuid.UUID, profileKey string, in api.OpenThread) (api.ThreadDetail, error) {
	by := AuthorOf(ctx, a.People)
	if err := a.guestLimit(ctx, by); err != nil {
		return api.ThreadDetail{}, err
	}
	anchor, _ := json.Marshal(in.Anchor)
	if bundleID != nil && in.AnchorKind == api.OpenThreadAnchorKindText {
		// The client sends a file and a byte range; the server builds the anchor from the
		// current version, so the quote, the context, and the heading path are right (§8.8).
		an, err := a.textAnchor(ctx, *bundleID, in.Anchor)
		if err != nil {
			return api.ThreadDetail{}, err
		}
		anchor, _ = json.Marshal(an)
	}
	c := Open{
		ID: kernel.NewID(), BundleID: bundleID, ProfileKey: profileKey, AnchorKind: string(in.AnchorKind), Anchor: anchor,
		AddressedTo: string(in.AddressedTo), Blocking: in.Blocking != nil && *in.Blocking, By: by, Body: in.Body,
		MessageID: kernel.NewID(), At: time.Now().UTC(),
	}
	if in.Title != nil {
		c.Title = *in.Title
	}
	if c.Title == "" {
		c.Title = titleFrom(in.Body)
	}
	s, err := a.run(ctx, c.ID, func(s State) ([]es.Event, error) { return DecideOpen(s, c) })
	if err != nil {
		return api.ThreadDetail{}, err
	}
	if s.AddressedTo == AI && a.Ask != nil {
		if err := a.Ask(ctx, s.ID); err != nil {
			return api.ThreadDetail{}, err
		}
	}
	return a.detail(ctx, s.ID)
}

// titleFrom is the first line of a message, cut to 80 characters.
func titleFrom(body string) string {
	t := strings.TrimSpace(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0])
	if r := []rune(t); len(r) > 80 {
		t = string(r[:79]) + "…"
	}
	return t
}

// ListBundleThreads lists a bundle's threads, open first.
func (a *API) ListBundleThreads(ctx context.Context, req api.ListBundleThreadsRequestObject) (api.ListBundleThreadsResponseObject, error) {
	rows, err := a.DB.Queries().ListSpecDocThreads(ctx, uuid.NullUUID{UUID: req.BundleId, Valid: true})
	if err != nil {
		return nil, err
	}
	b, err := a.DB.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: req.BundleId})
	if err != nil {
		return nil, err
	}
	cur, err := version.LoadCurrent(ctx, a.DB.Queries(), b)
	if err != nil {
		return nil, err
	}
	out := api.ListBundleThreads200JSONResponse{Items: []api.Thread{}}
	for _, r := range rows {
		t := threadAPI(r)
		follow(&t, cur)
		out.Items = append(out.Items, t)
	}
	return out, nil
}

// follow moves a text anchor to the current version, or marks it detached (SDD §8.8).
func follow(t *api.Thread, cur *version.Current) {
	if t.AnchorKind != api.ThreadAnchorKindText {
		return
	}
	raw, _ := json.Marshal(t.Anchor)
	var an anchor.Anchor
	if json.Unmarshal(raw, &an) != nil {
		return
	}
	an, ok := cur.Anchor(an)
	raw, _ = json.Marshal(an)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	if !ok {
		m["detached"] = true
	}
	t.Anchor = m
}

// OpenBundleThread opens a thread on a bundle (REQ-087).
func (a *API) OpenBundleThread(ctx context.Context, req api.OpenBundleThreadRequestObject) (api.OpenBundleThreadResponseObject, error) {
	if req.Body.AnchorKind == api.OpenThreadAnchorKindCheck {
		return nil, kernel.Invalid("bad_anchor", "A thread on a profile check belongs to the profile.")
	}
	id := req.BundleId
	d, err := a.open(ctx, &id, "", *req.Body)
	if err != nil {
		return nil, err
	}
	return api.OpenBundleThread200JSONResponse(d), nil
}

// ListProfileThreads lists the suggestions on a profile (REQ-015).
func (a *API) ListProfileThreads(ctx context.Context, req api.ListProfileThreadsRequestObject) (api.ListProfileThreadsResponseObject, error) {
	rows, err := a.DB.Queries().ListProfileThreads(ctx, pgdb.ListProfileThreadsParams{WorkspaceID: a.Workspace, ProfileKey: req.Key})
	if err != nil {
		return nil, err
	}
	out := api.ListProfileThreads200JSONResponse{Items: []api.Thread{}}
	for _, r := range rows {
		out.Items = append(out.Items, threadAPI(r))
	}
	return out, nil
}

// OpenProfileThread suggests a change to a profile, as a thread on a check (REQ-015).
func (a *API) OpenProfileThread(ctx context.Context, req api.OpenProfileThreadRequestObject) (api.OpenProfileThreadResponseObject, error) {
	if _, err := a.DB.Queries().GetProfileByKey(ctx, pgdb.GetProfileByKeyParams{WorkspaceID: a.Workspace, Key: req.Key}); err != nil {
		return nil, kernel.NotFound("profile_not_found", "No profile has the key %s.", req.Key)
	}
	in := *req.Body
	in.AnchorKind = api.OpenThreadAnchorKindCheck
	in.AddressedTo = api.OpenThreadAddressedToHumans
	d, err := a.open(ctx, nil, req.Key, in)
	if err != nil {
		return nil, err
	}
	return api.OpenProfileThread200JSONResponse(d), nil
}

// GetThread returns a thread with its messages.
func (a *API) GetThread(ctx context.Context, req api.GetThreadRequestObject) (api.GetThreadResponseObject, error) {
	d, err := a.detail(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	return api.GetThread200JSONResponse(d), nil
}

// PostMessage posts a message; in a thread for the AI, the AI answers (REQ-088).
func (a *API) PostMessage(ctx context.Context, req api.PostMessageRequestObject) (api.PostMessageResponseObject, error) {
	by := AuthorOf(ctx, a.People)
	if err := a.guestLimit(ctx, by); err != nil {
		return nil, err
	}
	m := Message{ID: kernel.NewID(), Author: by, Body: req.Body.Body, At: time.Now().UTC()}
	s, err := a.run(ctx, req.ThreadId, func(s State) ([]es.Event, error) { return DecidePost(s, m) })
	if err != nil {
		return nil, err
	}
	if s.AddressedTo == AI && a.Ask != nil {
		if err := a.Ask(ctx, s.ID); err != nil {
			return nil, err
		}
	}
	d, err := a.detail(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	return api.PostMessage200JSONResponse(d), nil
}

// MarkDecision marks a message as the decision (REQ-089).
func (a *API) MarkDecision(ctx context.Context, req api.MarkDecisionRequestObject) (api.MarkDecisionResponseObject, error) {
	by := AuthorOf(ctx, a.People)
	if _, err := a.run(ctx, req.ThreadId, func(s State) ([]es.Event, error) { return DecideDecision(s, req.Body.MessageId, by) }); err != nil {
		return nil, err
	}
	d, err := a.detail(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	return api.MarkDecision200JSONResponse(d), nil
}

// SetThreadBlocking marks the thread blocking or not (REQ-089).
func (a *API) SetThreadBlocking(ctx context.Context, req api.SetThreadBlockingRequestObject) (api.SetThreadBlockingResponseObject, error) {
	by := AuthorOf(ctx, a.People)
	if _, err := a.run(ctx, req.ThreadId, func(s State) ([]es.Event, error) { return DecideBlocking(s, req.Body.Blocking, by) }); err != nil {
		return nil, err
	}
	d, err := a.detail(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	return api.SetThreadBlocking200JSONResponse(d), nil
}

// SetThreadStatus resolves or reopens the thread.
func (a *API) SetThreadStatus(ctx context.Context, req api.SetThreadStatusRequestObject) (api.SetThreadStatusResponseObject, error) {
	by := AuthorOf(ctx, a.People)
	if _, err := a.run(ctx, req.ThreadId, func(s State) ([]es.Event, error) { return DecideResolve(s, req.Body.Open, by) }); err != nil {
		return nil, err
	}
	d, err := a.detail(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	return api.SetThreadStatus200JSONResponse(d), nil
}

// PostAI posts the AI's answer to a thread (REQ-088). The review worker calls it.
func PostAI(ctx context.Context, store *es.Store, thread uuid.UUID, body string, sources []string) error {
	m := Message{ID: kernel.NewID(), Author: Author{Kind: "ai", ID: "ai", Name: "Speccy AI"}, Body: body, Sources: sources, At: time.Now().UTC()}
	_, err := es.Run(ctx, store, StreamType, thread, func(s State) ([]es.Event, error) {
		if !s.Open {
			return nil, nil // resolved while the AI wrote: drop the answer
		}
		return DecidePost(s, m)
	}, Evolve)
	return err
}

func (a *API) detail(ctx context.Context, id uuid.UUID) (api.ThreadDetail, error) {
	q := a.DB.Queries()
	t, err := q.GetThreadView(ctx, pgdb.GetThreadViewParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return api.ThreadDetail{}, kernel.NotFound("thread_not_found", "No thread has this ID.")
	}
	if err != nil {
		return api.ThreadDetail{}, err
	}
	msgs, err := q.ListThreadMessages(ctx, id)
	if err != nil {
		return api.ThreadDetail{}, err
	}
	base := threadAPI(t)
	if t.SpecDocID.Valid && t.AnchorKind == AnchorText {
		b, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: t.SpecDocID.UUID})
		if err != nil {
			return api.ThreadDetail{}, err
		}
		cur, err := version.LoadCurrent(ctx, q, b)
		if err != nil {
			return api.ThreadDetail{}, err
		}
		follow(&base, cur)
	}
	d := api.ThreadDetail{
		Id: base.Id, BundleId: base.BundleId, ProfileKey: base.ProfileKey, AnchorKind: api.ThreadDetailAnchorKind(base.AnchorKind),
		Anchor: base.Anchor, AddressedTo: api.ThreadDetailAddressedTo(base.AddressedTo), Title: base.Title, Blocking: base.Blocking,
		Status: api.ThreadDetailStatus(base.Status), CreatedBy: base.CreatedBy, CreatedAt: base.CreatedAt,
		LastMessageAt: base.LastMessageAt, MessageCount: base.MessageCount, Messages: []api.ThreadMessage{},
	}
	for _, m := range msgs {
		sources := []string{}
		_ = json.Unmarshal(m.Sources, &sources)
		am := api.ThreadMessage{Id: m.ID, Seq: int(m.Seq), AuthorKind: api.ThreadMessageAuthorKind(m.AuthorKind), AuthorName: m.AuthorName,
			Body: m.Body, Sources: sources, Decision: api.ThreadMessageDecision(m.Decision), CreatedAt: m.CreatedAt.UTC()}
		if m.AuthorKind == "user" {
			am.AuthorId = &m.AuthorID
		}
		d.Messages = append(d.Messages, am)
	}
	d.Answering = t.AddressedTo == AI && a.Answering != nil && a.Answering(ctx, id)
	return d, nil
}

func threadAPI(t pgdb.ThreadView) api.Thread {
	anchor := map[string]any{}
	_ = json.Unmarshal(t.Anchor, &anchor)
	out := api.Thread{
		Id: t.ID, AnchorKind: api.ThreadAnchorKind(t.AnchorKind), Anchor: anchor, AddressedTo: api.ThreadAddressedTo(t.AddressedTo),
		Title: t.Title, Blocking: t.Blocking, Status: api.ThreadStatus(t.Status), CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt.UTC(), LastMessageAt: t.LastMessageAt.UTC(), MessageCount: int(t.MessageCount),
	}
	if t.SpecDocID.Valid {
		out.BundleId = &t.SpecDocID.UUID
	}
	if t.ProfileKey != "" {
		out.ProfileKey = &t.ProfileKey
	}
	// The handoff a builder opened this thread from, and the version it took (REQ-137).
	if t.HandoffID.Valid {
		out.HandoffId = &t.HandoffID.UUID
		v := t.HandoffVersion
		out.HandoffVersion = &v
	}
	return out
}

// textAnchor builds a text anchor from {file, start, end} on the bundle's current version.
func (a *API) textAnchor(ctx context.Context, bundleID uuid.UUID, raw map[string]any) (anchor.Anchor, error) {
	file, _ := raw["file"].(string)
	start, ok1 := raw["start"].(float64)
	end, ok2 := raw["end"].(float64)
	if file == "" || !ok1 || !ok2 || start < 0 || end <= start {
		return anchor.Anchor{}, kernel.Invalid("bad_anchor", "A text anchor needs a file and a range of text. Select some text first.")
	}
	b, err := a.DB.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: bundleID})
	if err != nil {
		return anchor.Anchor{}, err
	}
	files, err := version.Files(ctx, a.DB.Queries(), b.CurrentVersionID.UUID)
	if err != nil {
		return anchor.Anchor{}, err
	}
	for _, f := range files {
		if f.Path != file {
			continue
		}
		s, e := int(start), int(end)
		if e > len(f.Content) || !utf8.Valid(f.Content[s:e]) {
			return anchor.Anchor{}, kernel.Invalid("bad_anchor", "The selected text is not in the current version. Reload the file, then select again.")
		}
		doc := section.Doc{}
		if f.Path == b.DocPath {
			doc = section.Parse(f.Content)
		}
		return anchor.New(file, f.Content, doc, s, e), nil
	}
	return anchor.Anchor{}, kernel.NotFound("file_not_found", "The bundle has no file %s.", file)
}

// OpenFromBuild opens a thread that a builder reported from its handoff (REQ-137). The
// handoff feature calls it, so the report reuses the thread aggregate, its anchor, its
// blocking flag, and its place in the rail.
func (a *API) OpenFromBuild(ctx context.Context, bundleID uuid.UUID, handoffID uuid.UUID, version int64, in api.OpenThread) (api.ThreadDetail, error) {
	return a.openFrom(ctx, bundleID, &handoffID, version, in)
}

// OpenFromVerification opens the thread a verification run's outcome becomes. The run carries
// a handoff only when the caller named one, so handoffID may be nil.
func (a *API) OpenFromVerification(ctx context.Context, bundleID uuid.UUID, handoffID *uuid.UUID, version int64, in api.OpenThread) (api.ThreadDetail, error) {
	return a.openFrom(ctx, bundleID, handoffID, version, in)
}

func (a *API) openFrom(ctx context.Context, bundleID uuid.UUID, handoffID *uuid.UUID, version int64, in api.OpenThread) (api.ThreadDetail, error) {
	by := AuthorOf(ctx, a.People)
	anchor, _ := json.Marshal(in.Anchor)
	c := Open{
		ID: kernel.NewID(), BundleID: &bundleID, AnchorKind: string(in.AnchorKind), Anchor: anchor,
		AddressedTo: string(in.AddressedTo), Blocking: in.Blocking != nil && *in.Blocking, By: by, Body: in.Body,
		MessageID: kernel.NewID(), At: time.Now().UTC(), HandoffID: handoffID, HandoffVersion: version,
	}
	if in.Title != nil {
		c.Title = *in.Title
	}
	if c.Title == "" {
		c.Title = titleFrom(in.Body)
	}
	s, err := a.run(ctx, c.ID, func(s State) ([]es.Event, error) { return DecideOpen(s, c) })
	if err != nil {
		return api.ThreadDetail{}, err
	}
	return a.detail(ctx, s.ID)
}
