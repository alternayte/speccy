package approval

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// API serves bundle status, review requests, and approvals.
type API struct {
	DB        *store.DB
	ES        *es.Store
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
	People    kernel.Directory
}

// Load returns a bundle's status; a bundle with no stream is a draft.
func Load(ctx context.Context, st *es.Store, bundle uuid.UUID) (State, error) {
	snap, err := st.Load(ctx, bundle)
	if errors.Is(err, es.ErrNotFound) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	return s, json.Unmarshal(snap.State, &s)
}

func (a *API) run(ctx context.Context, bundle uuid.UUID, decide func(State) ([]es.Event, error)) (State, error) {
	return es.Run(ctx, a.ES, StreamType, bundle, decide, Evolve)
}

func (a *API) bundle(ctx context.Context, id uuid.UUID) (pgdb.Bundle, error) {
	return a.DB.Queries().GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: id})
}

func (a *API) required(b pgdb.Bundle) int {
	if p, ok := a.Profiles()[b.ProfileKey]; ok && p.Profile.Approvals.Required > 0 {
		return p.Profile.Approvals.Required
	}
	return 1
}

// buildReady reports whether b has a current build_ready verdict (REQ-076).
func (a *API) buildReady(ctx context.Context, b pgdb.Bundle) (bool, error) {
	v, _, err := review.Summary(ctx, a.DB.Queries(), b)
	if err != nil || v == nil {
		return false, err
	}
	return v.Result == api.VerdictResult("build_ready"), nil
}

// GetBundleStatus returns the status, and what the caller can do.
func (a *API) GetBundleStatus(ctx context.Context, req api.GetBundleStatusRequestObject) (api.GetBundleStatusResponseObject, error) {
	out, err := a.status(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	return api.GetBundleStatus200JSONResponse(out), nil
}

// RequestReview moves a draft to in review and assigns reviewers (REQ-090).
func (a *API) RequestReview(ctx context.Context, req api.RequestReviewRequestObject) (api.RequestReviewResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	act := kernel.ActorFrom(ctx)
	canEdit, err := share.CanEdit(ctx, a.DB.Queries(), act, b)
	if err != nil {
		return nil, err
	}
	people, err := a.People.People(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range req.Body.Reviewers {
		if !slices.ContainsFunc(people, func(p kernel.Person) bool { return p.ID == r }) {
			return nil, kernel.Invalid("unknown_reviewer", "No member has the ID %s.", r)
		}
	}
	c := RequestReview{By: act.UserID, CanEdit: canEdit, Reviewers: req.Body.Reviewers, At: time.Now().UTC()}
	if _, err := a.run(ctx, b.ID, func(s State) ([]es.Event, error) { return DecideRequestReview(s, c) }); err != nil {
		return nil, err
	}
	out, err := a.status(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	return api.RequestReview200JSONResponse(out), nil
}

// ApproveBundle approves the current version (REQ-076).
func (a *API) ApproveBundle(ctx context.Context, req api.ApproveBundleRequestObject) (api.ApproveBundleResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	c, err := a.approveCommand(ctx, b)
	if err != nil {
		return nil, err
	}
	if _, err := a.run(ctx, b.ID, func(s State) ([]es.Event, error) { return DecideApprove(s, c) }); err != nil {
		return nil, err
	}
	out, err := a.status(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	return api.ApproveBundle200JSONResponse(out), nil
}

func (a *API) approveCommand(ctx context.Context, b pgdb.Bundle) (Approve, error) {
	act := kernel.ActorFrom(ctx)
	author, err := a.DB.Queries().IsBundleAuthor(ctx, pgdb.IsBundleAuthorParams{BundleID: b.ID, UserID: act.UserID})
	if err != nil {
		return Approve{}, err
	}
	ready, err := a.buildReady(ctx, b)
	if err != nil {
		return Approve{}, err
	}
	return Approve{By: act.UserID, Author: author, Version: b.CurrentVersionID.UUID, BuildReady: ready, Required: a.required(b), At: time.Now().UTC()}, nil
}

func (a *API) status(ctx context.Context, id uuid.UUID) (api.BundleStatus, error) {
	b, err := a.bundle(ctx, id)
	if err != nil {
		return api.BundleStatus{}, err
	}
	s, err := Load(ctx, a.ES, b.ID)
	if err != nil {
		return api.BundleStatus{}, err
	}
	act := kernel.ActorFrom(ctx)
	out := api.BundleStatus{Status: api.ReviewStatus(s.Current()), Reviewers: []string{}, Required: a.required(b)}
	out.Reviewers = append(out.Reviewers, s.Reviewers...)
	out.Approvals = []struct {
		At        time.Time `json:"at"`
		By        string    `json:"by"`
		VersionId uuid.UUID `json:"version_id"`
	}{}
	for _, ap := range s.Approvals {
		out.Approvals = append(out.Approvals, struct {
			At        time.Time `json:"at"`
			By        string    `json:"by"`
			VersionId uuid.UUID `json:"version_id"`
		}{At: ap.At.UTC(), By: kernel.PersonByID(ctx, a.People, ap.By).Label(), VersionId: ap.Version})
	}
	if act.Guest != nil || act.UserID == "" {
		return out, nil
	}
	canEdit, err := share.CanEdit(ctx, a.DB.Queries(), act, b)
	if err != nil {
		return api.BundleStatus{}, err
	}
	out.CanRequest = canEdit && (s.Current() == Draft || s.Current() == InReview)
	c, err := a.approveCommand(ctx, b)
	if err != nil {
		return api.BundleStatus{}, err
	}
	if _, err := DecideApprove(s, c); err != nil {
		if ke, ok := kernel.AsError(err); ok {
			out.ApproveBlockedBy = &ke.Detail
		}
	} else {
		out.CanApprove = true
	}
	return out, nil
}

// ListPeople lists the workspace's members, for reviewers and mentions.
func (a *API) ListPeople(ctx context.Context, _ api.ListPeopleRequestObject) (api.ListPeopleResponseObject, error) {
	out := api.ListPeople200JSONResponse{Items: []api.Person{}}
	if a.People == nil {
		return out, nil
	}
	people, err := a.People.People(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range people {
		out.Items = append(out.Items, api.Person{Id: p.ID, Name: p.Label(), Email: p.Email, Role: p.Role})
	}
	return out, nil
}

// OnNewVersions revokes the approvals of every bundle whose content changed since they were
// given (REQ-077, T-013), and marks bundles superseded by a supersedes link (§9.5). It runs
// after every change.
func OnNewVersions(ctx context.Context, db *store.DB, st *es.Store, workspace uuid.UUID) error {
	q := db.Queries()
	views, err := q.ListBundleStatusViews(ctx, workspace)
	if err != nil {
		return err
	}
	for _, v := range views {
		var approvals []Approval
		_ = json.Unmarshal(v.Approvals, &approvals)
		if len(approvals) == 0 && v.Status != StatusApproved {
			continue
		}
		b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: workspace, ID: v.BundleID})
		if err != nil || !b.CurrentVersionID.Valid {
			continue
		}
		cur := b.CurrentVersionID.UUID
		if _, err := es.Run(ctx, st, StreamType, b.ID, func(s State) ([]es.Event, error) { return DecideContentChanged(s, cur) }, Evolve); err != nil {
			return err
		}
	}
	links, err := q.ListSupersedesLinks(ctx, workspace)
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := Supersede(ctx, st, l.TargetBundleID.UUID, l.FromBundleID); err != nil {
			return err
		}
	}
	return nil
}

// Supersede marks target superseded by from, for a supersedes link (§9.5).
func Supersede(ctx context.Context, st *es.Store, target, from uuid.UUID) error {
	_, err := es.Run(ctx, st, StreamType, target, func(s State) ([]es.Event, error) { return DecideSupersede(s, from) }, Evolve)
	return err
}
