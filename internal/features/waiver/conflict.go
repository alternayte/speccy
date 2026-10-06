package waiver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// conflictAnswer fills the request r for an answer to a conflict: an upstream request on a
// coherence.contradiction finding, a send-back on a coherence.downstream-request finding, or a
// waiver of a coherence.downstream-request finding. Each takes the approval policy of
// coherence.contradiction, because each one changes whether the conflict blocks a verdict.
func (a *API) conflictAnswer(ctx context.Context, b pgdb.SpecDoc, p profile.Profile, f pgdb.Finding, body *api.RequestWaiverJSONRequestBody, r *Request) error {
	upstream := body.UpstreamChange != nil && *body.UpstreamChange
	sendBack := body.SendBack != nil && *body.SendBack
	switch {
	case upstream && sendBack:
		return kernel.Invalid("two_answers", "Give one answer: the linked doc must change, or the downstream doc must change.")
	case upstream && f.CheckSlug != source.ContradictionCheck:
		return kernel.Invalid("not_a_conflict", "Only a %s finding takes the answer \"The linked doc must change\".", source.ContradictionCheck)
	case sendBack && f.CheckSlug != source.DownstreamRequestCheck:
		return kernel.Invalid("not_a_downstream_request", "Only a %s finding takes the answer \"The downstream doc must change\".", source.DownstreamRequestCheck)
	}
	policy := PolicyFor(p, source.ContradictionCheck, kernel.Must)
	if upstream {
		dec, err := a.Decisions(ctx, b)
		if err != nil {
			return err
		}
		if u := dec.UpstreamChangeFor(r.Conflict); u != nil {
			if u.SentBack != nil {
				return kernel.Conflict("sent_back", "%s answered that this doc must change: %s Edit the doc, or ask for a waiver of the conflict.",
					r.Conflict.With, sentenceOf(u.SentBack.Reason))
			}
			if u.Open() {
				return kernel.Conflict("upstream_change_exists", "The sidecar already asks %s to change for this conflict.", r.Conflict.With)
			}
		}
		r.Scope, r.Policy, r.Level = ScopeUpstream, policy, kernel.Must
		return nil
	}
	if f.CheckSlug != source.DownstreamRequestCheck {
		return nil
	}
	down, err := a.downstreamOf(ctx, f.Evidence)
	if err != nil {
		return err
	}
	r.Downstream, r.Policy, r.Level = down.ID, policy, kernel.Must
	if sendBack {
		r.Scope = ScopeSendBack
		return nil
	}
	// A waiver of the conflict says that the conflict is acceptable. It goes in the downstream
	// doc's sidecar as a waiver of coherence.contradiction, bound as that finding binds, so it
	// closes the conflict on both docs.
	main, doc, err := a.mainDoc(ctx, down)
	if err != nil {
		return err
	}
	start, end, ok := anchor.Find(main, r.Conflict.Quote)
	if !ok {
		return kernel.Conflict("conflict_gone", "%s no longer has the text of this conflict. Run the review again.", down.Slug)
	}
	an := anchor.New(down.DocPath, main, doc, start, end)
	dp := a.Profiles()[down.ProfileKey]
	bound, ok := dp.Profile.Bind(source.ContradictionCheck, doc, main, an.HeadingPath)
	if !ok {
		return kernel.Conflict("section_gone", "The section of this conflict is not in %s. Run the review again.", down.Slug)
	}
	r.Section, r.SectionHash = bound.Path, bound.Hash
	return nil
}

// downstreamOf is the downstream doc that a coherence.downstream-request finding names.
func (a *API) downstreamOf(ctx context.Context, evidence []byte) (pgdb.SpecDoc, error) {
	var ev struct {
		ID uuid.UUID `json:"downstream_bundle_id"`
	}
	_ = json.Unmarshal(evidence, &ev)
	if ev.ID == uuid.Nil {
		return pgdb.SpecDoc{}, kernel.Invalid("no_downstream", "Speccy does not hold the downstream doc of this finding, so it cannot write the answer. Answer in the repo of that doc.")
	}
	d, err := a.bundle(ctx, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return pgdb.SpecDoc{}, kernel.NotFound("no_downstream", "The downstream doc of this finding is gone. Run the review again.")
	}
	return d, err
}

// writeConflictDecision writes an approved answer to a conflict. An upstream request goes in
// this doc's sidecar. A send-back, and a waiver of a coherence.downstream-request finding, go in
// the downstream doc's sidecar: Speccy writes nothing into the linked doc's sidecar, and every
// decision about one conflict lives with the doc that asked.
func (a *API) writeConflictDecision(ctx context.Context, b pgdb.SpecDoc, s State, approvedBy string) error {
	by := kernel.PersonByID(ctx, a.People, s.RequestedBy).Label()
	if s.Conflict == nil {
		return kernel.Invalid("no_conflict", "This decision names no conflict. Ask again from the finding.")
	}
	if s.Scope == ScopeUpstream {
		dec, err := a.Decisions(ctx, b)
		if err != nil {
			return err
		}
		if u := dec.UpstreamChangeFor(s.Conflict); u != nil && u.SentBack != nil {
			return kernel.Conflict("sent_back", "%s answered that this doc must change: %s", s.Conflict.With, sentenceOf(u.SentBack.Reason))
		}
		dec = dec.WithUpstreamChange(source.UpstreamChange{Conflict: *s.Conflict, Reason: s.Reason, RequestedBy: by})
		return a.SetDecisions(ctx, b, dec, approvedBy, "Asked "+s.Conflict.With+" to change")
	}
	down, err := a.bundle(ctx, s.Downstream)
	if err != nil {
		return kernel.NotFound("no_downstream", "The downstream doc of this decision is gone.")
	}
	dec, err := a.Decisions(ctx, down)
	if err != nil {
		return err
	}
	if s.Scope == ScopeSendBack {
		u := dec.UpstreamChangeFor(s.Conflict)
		if u == nil || !u.Open() {
			return kernel.Conflict("no_upstream_change", "The sidecar of %s no longer asks for this change.", down.Slug)
		}
		sent := *u
		sent.SentBack = &source.SentBack{Reason: s.Reason, By: by}
		return a.SetDecisions(ctx, down, dec.WithUpstreamChange(sent), approvedBy, b.Slug+" sent the requested change back")
	}
	main, doc, err := a.mainDoc(ctx, down)
	if err != nil {
		return err
	}
	dp := a.Profiles()[down.ProfileKey]
	if !dp.Profile.Holds(source.ContradictionCheck, s.Section, s.SectionHash, doc, main) {
		return kernel.Conflict("section_changed", "The section of this conflict in %s changed after the request, so this waiver no longer fits it. Ask for a new waiver.", down.Slug)
	}
	w := source.Waiver{Check: source.ContradictionCheck, Section: s.Section, Reason: s.Reason, SectionHash: s.SectionHash, RequestedBy: by, Conflict: s.Conflict}
	return a.SetDecisions(ctx, down, dec.WithWaiver(w), approvedBy, "Approved a waiver for "+source.ContradictionCheck+" from "+b.Slug)
}

// endUpstreamRequest ends an approved upstream request of b that no longer waits: the linked
// doc sent it back, or an edit removed a quote of its conflict from either doc. A request that
// the sidecar does not hold yet, such as one in an open pull request, stays.
func endUpstreamRequest(ctx context.Context, db *store.DB, st *es.Store, b pgdb.SpecDoc, r pgdb.WaiverView,
	decisions func(context.Context, pgdb.SpecDoc) (source.Decisions, error)) error {
	s, err := loadState(ctx, st, r.ID)
	if err != nil || s.Conflict == nil {
		return err
	}
	dec, err := decisions(ctx, b)
	if err != nil {
		return nil //nolint:nilerr // a sidecar that does not parse ends nothing; the doc reports it
	}
	u := dec.UpstreamChangeFor(s.Conflict)
	if u == nil {
		return nil
	}
	why := ""
	switch {
	case u.SentBack != nil:
		why = EndedSentBack
	case !quoteIn(ctx, db, b, s.Conflict.Quote):
		why = EndedConflictClosed
	default:
		up, err := db.Queries().GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: b.WorkspaceID, Slug: s.Conflict.With})
		if err == nil && !quoteIn(ctx, db, up, s.Conflict.WithQuote) {
			why = EndedConflictClosed
		}
	}
	if why == "" {
		return nil
	}
	_, err = es.Run(ctx, st, StreamType, r.ID, func(s State) ([]es.Event, error) { return DecideInvalidate(s, false, why) }, Evolve)
	return err
}

// invalidateDownstreamWaiver ends, or brings back, a waiver of a coherence.downstream-request
// finding of b. It lives in the downstream doc's sidecar, so it holds while the section of the
// conflict in the downstream doc is the text it was approved for.
func invalidateDownstreamWaiver(ctx context.Context, db *store.DB, st *es.Store, b pgdb.SpecDoc, r pgdb.WaiverView,
	decisions func(context.Context, pgdb.SpecDoc) (source.Decisions, error), profiles func() map[string]profile.Versioned) error {
	if r.Status != StatusApproved && r.Status != StatusInvalidated {
		return nil
	}
	s, err := loadState(ctx, st, r.ID)
	if err != nil || s.Downstream == uuid.Nil {
		return err
	}
	down, err := db.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: b.WorkspaceID, ID: s.Downstream})
	if err != nil || !down.CurrentVersionID.Valid {
		return nil //nolint:nilerr // a downstream doc that is gone ends nothing here
	}
	main := docContent(ctx, db, down)
	holds := profiles()[down.ProfileKey].Profile.Holds(source.ContradictionCheck, s.Section, s.SectionHash, section.Parse(main), main)
	decide := func(s State) ([]es.Event, error) { return DecideInvalidate(s, holds, EndedSectionChanged) }
	if r.Status == StatusInvalidated {
		if !holds {
			return nil
		}
		dec, err := decisions(ctx, down)
		if err != nil {
			return nil //nolint:nilerr // a sidecar that does not parse restores nothing
		}
		inSidecar := slices.ContainsFunc(dec.Waivers, func(w source.Waiver) bool {
			return w.Check == source.ContradictionCheck && slices.Equal(w.Section, s.Section) && w.SectionHash == s.SectionHash && w.Conflict.Same(s.Conflict)
		})
		decide = func(s State) ([]es.Event, error) { return DecideRestore(s, holds, inSidecar) }
	}
	_, err = es.Run(ctx, st, StreamType, r.ID, decide, Evolve)
	return err
}

// docContent is the current main doc of b, or nil.
func docContent(ctx context.Context, db *store.DB, b pgdb.SpecDoc) []byte {
	if !b.CurrentVersionID.Valid {
		return nil
	}
	files, err := version.Files(ctx, db.Queries(), b.CurrentVersionID.UUID)
	if err != nil {
		return nil
	}
	for _, f := range files {
		if f.Path == b.DocPath {
			return f.Content
		}
	}
	return nil
}

// quoteIn reports whether the current main doc of b holds quote. A doc that does not read
// holds it, so a failed read ends nothing.
func quoteIn(ctx context.Context, db *store.DB, b pgdb.SpecDoc, quote string) bool {
	main := docContent(ctx, db, b)
	if main == nil {
		return true
	}
	_, _, ok := anchor.Find(main, quote)
	return ok
}

// loadState reads the state of one waiver stream.
func loadState(ctx context.Context, st *es.Store, id uuid.UUID) (State, error) {
	snap, err := st.Load(ctx, id)
	if err != nil {
		return State{}, err
	}
	var s State
	err = json.Unmarshal(snap.State, &s)
	return s, err
}

// sentenceOf ends a reason with a full stop.
func sentenceOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") {
		return s
	}
	return s + "."
}
