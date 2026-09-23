package waiver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// Change writes a file to a bundle as a new version (the bundle feature's Change).
type Change func(ctx context.Context, id, base uuid.UUID, op source.Op, by, message string) (pgdb.Version, bool, error)

// API serves waivers.
type API struct {
	DB        *store.DB
	ES        *es.Store
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
	People    kernel.Directory
	Change    Change
	// Decisions reads a bundle's sidecar, and SetDecisions writes it (DEC-009).
	Decisions    func(ctx context.Context, b pgdb.SpecDoc) (source.Decisions, error)
	SetDecisions func(ctx context.Context, b pgdb.SpecDoc, d source.Decisions, by, message string) error
}

// PolicyFor is the waiver policy of a check at a level (§9.1): the check's own policy when it
// has one, else the profile's policy for the level.
func PolicyFor(p profile.Profile, check string, level kernel.Level) profile.Policy {
	for _, c := range p.Checks {
		if c.Slug == check && c.Waiver != nil {
			return *c.Waiver
		}
	}
	if level == kernel.Must {
		return p.Waivers.Must
	}
	return p.Waivers.Should
}

func (a *API) bundle(ctx context.Context, id uuid.UUID) (pgdb.SpecDoc, error) {
	return a.DB.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: id})
}

// approver is the actor as an approver of a waiver on b.
func (a *API) approver(ctx context.Context, b pgdb.SpecDoc) (Approver, error) {
	act := kernel.ActorFrom(ctx)
	q := a.DB.Queries()
	author, err := q.IsBundleAuthor(ctx, pgdb.IsBundleAuthorParams{BundleID: b.BundleID, UserID: act.UserID})
	if err != nil {
		return Approver{}, err
	}
	maint, err := q.IsProfileMaintainer(ctx, pgdb.IsProfileMaintainerParams{WorkspaceID: a.Workspace, Key: b.ProfileKey, UserID: act.UserID})
	if err != nil {
		return Approver{}, err
	}
	return Approver{UserID: act.UserID, Author: author, Maintainer: maint, Admin: act.IsAdmin()}, nil
}

// mainDoc returns the current main doc of b, parsed.
func (a *API) mainDoc(ctx context.Context, b pgdb.SpecDoc) ([]byte, section.Doc, error) {
	files, err := version.Files(ctx, a.DB.Queries(), b.CurrentVersionID.UUID)
	if err != nil {
		return nil, section.Doc{}, err
	}
	for _, f := range files {
		if f.Path == b.DocPath {
			return f.Content, section.Parse(f.Content), nil
		}
	}
	return nil, section.Doc{}, kernel.NotFound("no_main_doc", "The bundle's current version has no main doc.")
}

// RequestWaiver asks for a waiver of one finding (REQ-072).
func (a *API) RequestWaiver(ctx context.Context, req api.RequestWaiverRequestObject) (api.RequestWaiverResponseObject, error) {
	q := a.DB.Queries()
	b, err := a.bundle(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	f, err := q.GetFinding(ctx, req.Body.FindingId)
	if err != nil {
		return nil, kernel.NotFound("finding_not_found", "No finding has this ID.")
	}
	run, err := q.GetRunByID(ctx, f.RunID)
	if err != nil || run.SpecDocID != b.ID {
		return nil, kernel.NotFound("finding_not_found", "This bundle has no such finding.")
	}
	if kernel.Level(f.Level) == kernel.Info {
		return nil, kernel.Invalid("info_finding", "An INFO finding never changes the verdict, so it needs no waiver.")
	}
	p, ok := a.Profiles()[b.ProfileKey]
	if !ok {
		return nil, kernel.Invalid("no_profile", "The bundle's doc type has no profile.")
	}
	var an anchor.Anchor
	_ = json.Unmarshal(f.Anchor, &an)
	path := an.HeadingPath
	if path == nil || p.Profile.DocScope(f.CheckSlug) {
		path = []string{}
	}
	main, doc, err := a.mainDoc(ctx, b)
	if err != nil {
		return nil, err
	}
	hash, ok := section.HashAt(doc, main, path)
	if !ok {
		return nil, kernel.Conflict("section_gone", "The section of this finding is not in the current version. Run the review again.")
	}
	r := Request{
		ID: kernel.NewID(), BundleID: b.ID, Check: f.CheckSlug, Level: kernel.Level(f.Level), Section: path, SectionHash: hash,
		Reason: req.Body.Reason, By: kernel.ActorFrom(ctx).UserID, Policy: PolicyFor(p.Profile, f.CheckSlug, kernel.Level(f.Level)),
	}
	if _, err := es.Run(ctx, a.ES, StreamType, r.ID, func(s State) ([]es.Event, error) { return DecideRequest(s, r) }, Evolve); err != nil {
		return nil, err
	}
	w, err := a.waiver(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	return api.RequestWaiver200JSONResponse(w), nil
}

// ApproveWaiver approves under the policy. A final approval first writes the waiver to the
// doc's sidecar, then records WaiverApproved (DEC-009).
func (a *API) ApproveWaiver(ctx context.Context, req api.ApproveWaiverRequestObject) (api.ApproveWaiverResponseObject, error) {
	s, b, err := a.load(ctx, req.WaiverId)
	if err != nil {
		return nil, err
	}
	who, err := a.approver(ctx, b)
	if err != nil {
		return nil, err
	}
	events, err := DecideApprove(s, who)
	if err != nil {
		return nil, err
	}
	// A verification waiver never goes in the sidecar: it excuses one trace ID in one code
	// repo, and the sidecar travels with the doc into every build of it.
	if Evolve(s, events[0]).Status == StatusApproved && s.Scope != ScopeVerify {
		if err := a.writeSidecar(ctx, b, s, who.UserID); err != nil {
			return nil, err
		}
	}
	if _, err := es.Run(ctx, a.ES, StreamType, s.ID, func(s State) ([]es.Event, error) { return DecideApprove(s, who) }, Evolve); err != nil {
		return nil, err
	}
	w, err := a.waiver(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	return api.ApproveWaiver200JSONResponse(w), nil
}

// writeSidecar writes the approved waiver to the doc's sidecar (DEC-009). The doc text does
// not change, so no version of the doc is made here.
func (a *API) writeSidecar(ctx context.Context, b pgdb.SpecDoc, s State, approvedBy string) error {
	main, doc, err := a.mainDoc(ctx, b)
	if err != nil {
		return err
	}
	if h, ok := section.HashAt(doc, main, s.Section); !ok || h != s.SectionHash {
		return kernel.Conflict("section_changed", "The section changed after the request, so this waiver no longer fits it. Ask for a new waiver.")
	}
	dec, err := a.Decisions(ctx, b)
	if err != nil {
		return err
	}
	dec = dec.WithWaiver(source.Waiver{
		Check: s.Check, Section: s.Section, Reason: s.Reason, SectionHash: s.SectionHash,
		RequestedBy: kernel.PersonByID(ctx, a.People, s.RequestedBy).Label(),
	})
	return a.SetDecisions(ctx, b, dec, approvedBy, "Approved a waiver for "+s.Check)
}

// RejectWaiver rejects a requested waiver.
func (a *API) RejectWaiver(ctx context.Context, req api.RejectWaiverRequestObject) (api.RejectWaiverResponseObject, error) {
	s, b, err := a.load(ctx, req.WaiverId)
	if err != nil {
		return nil, err
	}
	who, err := a.approver(ctx, b)
	if err != nil {
		return nil, err
	}
	reason := req.Body.Reason
	if _, err := es.Run(ctx, a.ES, StreamType, s.ID, func(s State) ([]es.Event, error) { return DecideReject(s, who, reason) }, Evolve); err != nil {
		return nil, err
	}
	w, err := a.waiver(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	return api.RejectWaiver200JSONResponse(w), nil
}

// ListWaivers lists a bundle's waivers.
func (a *API) ListWaivers(ctx context.Context, req api.ListWaiversRequestObject) (api.ListWaiversResponseObject, error) {
	rows, err := a.DB.Queries().ListSpecDocWaivers(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	out := api.ListWaivers200JSONResponse{Items: []api.Waiver{}}
	for _, r := range rows {
		w, err := a.waiver(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, w)
	}
	return out, nil
}

// Waiting returns the waivers that wait for the caller's approval, newest first (SDD §9.1). The
// inbox uses it, so a maintainer who is not on the bundle sees the request.
func (a *API) Waiting(ctx context.Context) ([]api.Waiver, error) {
	rows, err := a.DB.Queries().ListWorkspaceWaivers(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	var out []api.Waiver
	for _, row := range rows {
		if row.Status != string(StatusRequested) {
			continue
		}
		w, err := a.waiver(ctx, row.ID)
		if err != nil || !w.CanApprove {
			continue // the bundle is gone, or this person does not decide it
		}
		out = append(out, w)
	}
	return out, nil
}

// Decided is one waiver the inbox reports: a rejection to its requester, or a waiver an edit
// ended, to the bundle's authors (REQ-074).
type Decided struct {
	Waiver      api.Waiver
	RequestedBy string // the user ID, not the label
	At          time.Time
}

// DecidedSince returns the waivers rejected or ended since t, across the workspace.
func (a *API) DecidedSince(ctx context.Context, t time.Time) ([]Decided, error) {
	rows, err := a.DB.Queries().ListWorkspaceWaivers(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	var out []Decided
	for _, row := range rows {
		if row.Status != StatusRejected && row.Status != StatusInvalidated {
			continue
		}
		if row.UpdatedAt.Before(t) {
			continue
		}
		w, err := a.waiver(ctx, row.ID)
		if err != nil {
			continue // the bundle is gone
		}
		out = append(out, Decided{Waiver: w, RequestedBy: row.RequestedBy, At: row.UpdatedAt.UTC()})
	}
	return out, nil
}

func (a *API) load(ctx context.Context, id uuid.UUID) (State, pgdb.SpecDoc, error) {
	snap, err := a.ES.Load(ctx, id)
	if errors.Is(err, es.ErrNotFound) {
		return State{}, pgdb.SpecDoc{}, kernel.NotFound("waiver_not_found", "No waiver has this ID.")
	}
	if err != nil {
		return State{}, pgdb.SpecDoc{}, err
	}
	var s State
	if err := json.Unmarshal(snap.State, &s); err != nil {
		return State{}, pgdb.SpecDoc{}, err
	}
	b, err := a.bundle(ctx, s.BundleID)
	return s, b, err
}

func (a *API) waiver(ctx context.Context, id uuid.UUID) (api.Waiver, error) {
	s, b, err := a.load(ctx, id)
	if err != nil {
		return api.Waiver{}, err
	}
	v, err := a.DB.Queries().GetWaiverView(ctx, pgdb.GetWaiverViewParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return api.Waiver{}, kernel.NotFound("waiver_not_found", "No waiver has this ID.")
	}
	if err != nil {
		return api.Waiver{}, err
	}
	need := 1
	if s.Policy.Name == "n_approvals" && s.Policy.NApprovals > 1 {
		need = s.Policy.NApprovals
	}
	can := false
	if s.Status == StatusRequested && kernel.ActorFrom(ctx).Guest == nil {
		who, err := a.approver(ctx, b)
		if err != nil {
			return api.Waiver{}, err
		}
		ok, _ := CanApprove(s.Policy, who)
		can = ok && !slices.Contains(s.Approvals, who.UserID)
	}
	approvals := make([]string, len(s.Approvals))
	for i, u := range s.Approvals {
		approvals[i] = kernel.PersonByID(ctx, a.People, u).Label()
	}
	path := s.Section
	if path == nil {
		path = []string{}
	}
	w := api.Waiver{
		Id: s.ID, DocId: s.BundleID, CheckSlug: s.Check, Level: string(s.Level), Section: path, Reason: s.Reason,
		Status: api.WaiverStatus(s.Status), RequestedBy: kernel.PersonByID(ctx, a.People, s.RequestedBy).Label(),
		Approvals: approvals, Policy: s.Policy.Name, Needed: need, CanApprove: can, CreatedAt: v.CreatedAt.UTC(),
	}
	if s.DecisionReason != "" {
		w.DecisionReason = &s.DecisionReason
	}
	if main, doc, err := a.mainDoc(ctx, b); err == nil {
		if start, end, ok := section.RangeAt(doc, main, s.Section); ok {
			w.SectionRange = &api.SectionRange{Start: start, End: end}
		}
	}
	return w, nil
}

// Invalidate ends each approved waiver of b whose section changed (REQ-074, T-010). It runs
// after every new version.
func Invalidate(ctx context.Context, db *store.DB, st *es.Store, b pgdb.SpecDoc) error {
	if !b.CurrentVersionID.Valid {
		return nil
	}
	rows, err := db.Queries().ListSpecDocWaivers(ctx, b.ID)
	if err != nil {
		return err
	}
	var main []byte
	var doc section.Doc
	loaded := false
	for _, r := range rows {
		if r.Status != StatusApproved {
			continue
		}
		if !loaded {
			files, err := version.Files(ctx, db.Queries(), b.CurrentVersionID.UUID)
			if err != nil {
				return err
			}
			for _, f := range files {
				if f.Path == b.DocPath {
					main = f.Content
				}
			}
			doc, loaded = section.Parse(main), true
		}
		var path []string
		_ = json.Unmarshal(r.SectionPath, &path)
		hash, _ := section.HashAt(doc, main, path)
		if _, err := es.Run(ctx, st, StreamType, r.ID, func(s State) ([]es.Event, error) { return DecideInvalidate(s, hash) }, Evolve); err != nil {
			return err
		}
	}
	return nil
}

// RequestVerificationWaiver asks to excuse one trace ID in one code repo. It keeps the whole
// waiver mechanism: a reason of at least MinReason characters, the profile's policy for a MUST,
// no self-approval, and an end when the requirement's section changes. It never goes in the
// doc's sidecar, because the sidecar travels with the doc into every build of it.
func (a *API) RequestVerificationWaiver(ctx context.Context, req api.RequestVerificationWaiverRequestObject) (api.RequestVerificationWaiverResponseObject, error) {
	b, err := version.Bundle(ctx, a.DB.Queries(), a.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	p, ok := a.Profiles()[b.ProfileKey]
	if !ok {
		return nil, kernel.Invalid("no_profile", "The bundle's doc type has no profile.")
	}
	traceID := strings.TrimSpace(req.Body.TraceId)
	repo := strings.TrimSpace(req.Body.Repo)
	if traceID == "" || repo == "" {
		return nil, kernel.Invalid("no_target", "Name the trace ID and the repo this waiver excuses.")
	}
	main, doc, err := a.mainDoc(ctx, b)
	if err != nil {
		return nil, err
	}
	prefixes := append(append([]string(nil), p.Profile.Trace.Prefixes...), p.Profile.Verify.Prefixes...)
	var path []string
	found := false
	for _, d := range lint.Definitions(main, prefixes) {
		if strings.EqualFold(d.ID, traceID) {
			an := anchor.New(b.DocPath, main, doc, d.Start, d.End)
			path, found = an.HeadingPath, true
			break
		}
	}
	if !found {
		return nil, kernel.Invalid("trace_id_not_found", "The doc defines no %s.", traceID)
	}
	if path == nil {
		path = []string{}
	}
	hash, ok := section.HashAt(doc, main, path)
	if !ok {
		return nil, kernel.Conflict("section_gone", "The section of %s is not in the current version.", traceID)
	}
	slug := "verify." + strings.ToLower(traceID)
	r := Request{
		ID: kernel.NewID(), BundleID: b.ID, Check: slug, Scope: ScopeVerify, TraceID: traceID, Repo: repo,
		Level: kernel.Must, Section: path, SectionHash: hash, Reason: req.Body.Reason,
		By: kernel.ActorFrom(ctx).UserID, Policy: p.Profile.Waivers.Must,
	}
	if _, err := es.Run(ctx, a.ES, StreamType, r.ID, func(s State) ([]es.Event, error) { return DecideRequest(s, r) }, Evolve); err != nil {
		return nil, err
	}
	w, err := a.waiver(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	return api.RequestVerificationWaiver200JSONResponse(w), nil
}
