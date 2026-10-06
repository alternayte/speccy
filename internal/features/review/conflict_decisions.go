package review

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// DownstreamRequestSlug is the finding of a doc that another doc asks to change: an open
// upstream request in the sidecar of a doc that links to it.
const DownstreamRequestSlug = source.DownstreamRequestCheck

// Upstream request states, as a finding shows them.
const (
	upstreamWaiting  = "waiting"
	upstreamSentBack = "sent_back"
)

// upstreamState is the upstream request of one coherence.contradiction finding: the answer
// "The linked doc must change", from the doc's sidecar. A run stores it in the evidence of
// the finding, so the finding shows the state the run's verdict read.
type upstreamState struct {
	Upstream       string `json:"upstream"`
	UpstreamTitle  string `json:"upstream_title,omitempty"`
	Reason         string `json:"reason"`
	RequestedBy    string `json:"requested_by,omitempty"`
	State          string `json:"state"`
	Blocks         bool   `json:"blocks"`
	SentBackReason string `json:"sent_back_reason,omitempty"`
	SentBackBy     string `json:"sent_back_by,omitempty"`
}

// waiting reports whether the finding waits on the linked doc and does not block.
func (u *upstreamState) waiting() bool { return u != nil && !u.Blocks }

// upstreamStates returns the upstream request of each finding, or nil. An open request
// waits on the linked doc: it does not block, unless the profile sets
// coherence.upstream_pending to block. A request that the linked doc sent back blocks, and the
// finding shows the reason.
func upstreamStates(in input, ev *evaluation) []*upstreamState {
	out := make([]*upstreamState, len(ev.findings))
	if len(in.dec.UpstreamChanges) == 0 {
		return out
	}
	block := in.profile.Profile.Coherence.UpstreamPendingBlocks()
	for i, f := range ev.findings {
		u := in.dec.UpstreamChangeFor(f.conflict())
		if u == nil || strings.TrimSpace(u.Reason) == "" {
			continue
		}
		st := &upstreamState{Upstream: u.Conflict.With, Reason: u.Reason, RequestedBy: u.RequestedBy, State: upstreamWaiting, Blocks: block}
		for _, l := range in.linked {
			if l.target.Slug == u.Conflict.With {
				st.UpstreamTitle = l.target.Title
				break
			}
		}
		if u.SentBack != nil {
			st.State, st.Blocks, st.SentBackReason, st.SentBackBy = upstreamSentBack, true, u.SentBack.Reason, u.SentBack.By
		}
		out[i] = st
	}
	return out
}

// withUpstream returns the evidence with the upstream request st under "upstream_change", or
// without that key when st is nil.
func withUpstream(evidence []byte, st *upstreamState) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(evidence, &m) != nil || m == nil {
		if st == nil {
			return evidence
		}
		m = map[string]json.RawMessage{}
	}
	if st == nil {
		if _, ok := m["upstream_change"]; !ok {
			return evidence
		}
		delete(m, "upstream_change")
	} else {
		m["upstream_change"], _ = json.Marshal(st)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return evidence
	}
	return out
}

// upstreamOf reads the upstream request from the evidence of a finding, or nil.
func upstreamOf(evidence []byte) *upstreamState {
	var ev struct {
		Upstream *upstreamState `json:"upstream_change"`
	}
	if json.Unmarshal(evidence, &ev) != nil || ev.Upstream == nil || ev.Upstream.State == "" {
		return nil
	}
	return ev.Upstream
}

// upstreamAPI is the upstream request of a finding in the API, or nil.
func upstreamAPI(evidence []byte) *api.UpstreamChange {
	u := upstreamOf(evidence)
	if u == nil {
		return nil
	}
	out := &api.UpstreamChange{Upstream: u.Upstream, Reason: u.Reason, State: api.UpstreamChangeState(u.State), Blocks: u.Blocks}
	opt := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	out.UpstreamTitle, out.RequestedBy, out.SentBackReason, out.SentBackBy = opt(u.UpstreamTitle), opt(u.RequestedBy), opt(u.SentBackReason), opt(u.SentBackBy)
	return out
}

// incoming is a doc that links to the doc under review, with the upstream requests of its
// sidecar that name the doc under review. with is the slug that those requests give it.
type incoming struct {
	doc     pgdb.SpecDoc
	main    []byte
	parsed  section.Doc
	profile profile.Profile
	dec     source.Decisions
	with    string
}

// requests are the upstream requests of the downstream doc that name the doc under review.
func (c incoming) requests() []source.UpstreamChange {
	var out []source.UpstreamChange
	for _, u := range c.dec.UpstreamChanges {
		if u.Conflict.With == c.with {
			out = append(out, u)
		}
	}
	return out
}

// waived reports whether a waiver in the downstream doc's sidecar excuses the conflict c: the
// conflict is acceptable, so it is closed on both docs.
func (c incoming) waived(conflict source.Conflict) bool {
	for _, w := range c.dec.Waivers {
		if w.Check != ContradictionSlug || w.Conflict == nil || !w.Conflict.Same(&conflict) || strings.TrimSpace(w.Reason) == "" {
			continue
		}
		if c.profile.Holds(ContradictionSlug, w.Section, waiverHash(w), c.parsed, c.main) {
			return true
		}
	}
	return false
}

// loadIncoming returns each doc that links to b, or to the saved doc that content from a repo
// is, with a sidecar that asks b to change. A doc of the same commit counts when its sidecar
// names b's slug. A doc that the review cannot see gives b nothing.
func (s *Service) loadIncoming(ctx context.Context, b pgdb.SpecDoc, from *FromRepo) ([]incoming, error) {
	if s.Decisions == nil && from == nil {
		return nil, nil
	}
	var out []incoming
	var targets []pgdb.SpecDoc
	if b.ID != uuid.Nil {
		targets = append(targets, b)
	} else if from != nil {
		twin, err := s.twinOf(ctx, b, from)
		if err != nil {
			return nil, err
		}
		if twin != nil {
			targets = append(targets, *twin)
		}
	}
	q := s.DB.Queries()
	for _, t := range targets {
		if s.Decisions == nil {
			break
		}
		links, err := q.ListLinksTo(ctx, uuid.NullUUID{UUID: t.ID, Valid: true})
		if err != nil {
			return nil, err
		}
		seen := map[uuid.UUID]bool{}
		for _, l := range links {
			if seen[l.FromSpecDocID] || !slices.Contains(contradictionKinds, l.Kind) {
				continue
			}
			seen[l.FromSpecDocID] = true
			d, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: l.FromSpecDocID})
			if err != nil || d.ArchivedAt.Valid || !d.CurrentVersionID.Valid {
				continue
			}
			dec, err := s.Decisions(ctx, d)
			if err != nil {
				continue // a sidecar that does not parse asks for nothing; its own doc reports it
			}
			c := incoming{doc: d, dec: dec, with: t.Slug}
			if len(c.requests()) == 0 {
				continue
			}
			files, err := version.Files(ctx, q, d.CurrentVersionID.UUID)
			if err != nil {
				return nil, err
			}
			c.main = mainOf(files, d.DocPath)
			c.parsed = section.Parse(c.main)
			if p, ok := s.Profiles()[d.ProfileKey]; ok {
				c.profile = p.Profile
			}
			out = append(out, c)
		}
	}
	if from != nil {
		for _, sib := range from.Siblings {
			c := incoming{doc: pgdb.SpecDoc{Slug: sib.Slug, Title: sib.Title, DocPath: sib.DocPath, ProfileKey: sib.Profile},
				dec: sib.Decisions, with: b.Slug}
			if len(c.requests()) == 0 {
				continue
			}
			c.main = mainOf(sib.Files, sib.DocPath)
			c.parsed = section.Parse(c.main)
			if p, ok := s.Profiles()[sib.Profile]; ok {
				c.profile = p.Profile
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// twinOf returns the saved doc that content from a repo is: the doc of a GitHub source of the
// same repo at the same path, or nil. A doc that links to the GitHub doc links to that saved doc.
func (s *Service) twinOf(ctx context.Context, b pgdb.SpecDoc, from *FromRepo) (*pgdb.SpecDoc, error) {
	if from.Repo == "" {
		return nil, nil
	}
	held, err := s.githubDocs(ctx)
	if err != nil || len(held) == 0 {
		return nil, err
	}
	all, err := s.allBundles(ctx)
	if err != nil {
		return nil, err
	}
	place, err := s.places(ctx)
	if err != nil {
		return nil, err
	}
	want := path.Join(from.Dir, b.DocPath)
	for i := range all {
		var r bundleRef
		if all[i].ArchivedAt.Valid || json.Unmarshal(all[i].SourceRef, &r) != nil || r.Source == uuid.Nil {
			continue
		}
		if src, ok := held[r.Source]; ok && strings.EqualFold(src.Repo, from.Repo) && mainDocPath(all[i], place) == want {
			return &all[i], nil
		}
	}
	return nil, nil
}

// mainOf is the content of the file at p, or nil.
func mainOf(files []source.File, p string) []byte {
	for _, f := range files {
		if f.Path == p {
			return f.Content
		}
	}
	return nil
}

// incomingHash is the hash of what the downstream docs ask of the doc: their current
// versions, their upstream requests that name it, and their waivers of a conflict with it. A
// run records it with the sidecar's hash, so a new request, a send-back or an edit of a
// downstream doc lints the doc again. It is "" when no doc asks anything.
func incomingHash(in []incoming) string {
	if len(in) == 0 {
		return ""
	}
	var parts []string
	for _, c := range in {
		var waivers []source.Waiver
		for _, w := range c.dec.Waivers {
			if w.Check == ContradictionSlug && w.Conflict != nil && w.Conflict.With == c.with {
				waivers = append(waivers, w)
			}
		}
		raw, _ := json.Marshal(struct {
			Requests []source.UpstreamChange
			Waivers  []source.Waiver
		}{c.requests(), waivers})
		parts = append(parts, c.doc.Slug, version.Hash(c.main), string(raw))
	}
	return hashOf(parts...)
}

// runDecisionsHash is the decisions hash a run records: the doc's sidecar, and what the docs
// that link to it ask of it.
func runDecisionsHash(dec source.Decisions, in []incoming) string {
	h := decisionsHash(dec)
	if ih := incomingHash(in); ih != "" {
		return h + ":" + ih
	}
	return h
}

// decisionsKey is runDecisionsHash for a saved doc.
func (s *Service) decisionsKey(ctx context.Context, b pgdb.SpecDoc) (string, error) {
	if s.Decisions == nil {
		return "", nil
	}
	dec, err := s.Decisions(ctx, b)
	if err != nil {
		return "", err
	}
	in, err := s.loadIncoming(ctx, b, nil)
	if err != nil {
		return "", err
	}
	return runDecisionsHash(dec, in), nil
}

// downstreamRequests reports each open upstream request that a doc linking to this doc makes
// of it, as a MUST finding on the quoted text of this doc. A request whose quote is gone from
// either doc is closed: the edit removed the conflict. A waiver of the conflict on the
// downstream doc says that the conflict is acceptable, so it closes the request too.
func downstreamRequests(in input, ev *evaluation) {
	lvl := in.level(DownstreamRequestSlug, checkLevel(in.profile.Profile, DownstreamRequestSlug, kernel.Must))
	asked, open := 0, 0
	for _, c := range in.incoming {
		name := c.doc.Slug
		if t := strings.TrimSpace(c.doc.Title); t != "" {
			name = t
		}
		for _, u := range c.requests() {
			if !u.Open() {
				continue
			}
			asked++
			ts, te, ok1 := anchor.Find(in.main, u.Conflict.WithQuote)
			ds, de, ok2 := anchor.Find(c.main, u.Conflict.Quote)
			if !ok1 || !ok2 || c.waived(u.Conflict) {
				continue
			}
			open++
			conflict := u.Conflict
			evidence := map[string]any{"downstream": c.doc.Slug, "downstream_title": c.doc.Title, "conflict": conflict,
				"quote": u.Conflict.WithQuote, "downstream_quote": u.Conflict.Quote, "reason": u.Reason,
				"downstream_anchor": anchor.New(c.doc.DocPath, c.main, c.parsed, ds, de)}
			if c.doc.ID != uuid.Nil {
				evidence["downstream_bundle_id"] = c.doc.ID
			}
			if u.RequestedBy != "" {
				evidence["requested_by"] = u.RequestedBy
			}
			ev.findings = append(ev.findings, pending{
				slug: DownstreamRequestSlug, level: lvl, stage: StageCoherence,
				anchor: anchor.New(in.bundle.DocPath, in.main, in.doc, ts, te),
				message: fmt.Sprintf("%s says “%s”, and this doc says “%s”. %s asks that this doc change: %s",
					name, oneLine(u.Conflict.Quote), oneLine(u.Conflict.WithQuote), name, sentence(u.Reason)),
				fix:      fmt.Sprintf("Change this doc so that it agrees with %s, or answer that %s must change.", name, name),
				evidence: evidence,
			})
		}
	}
	if asked > 0 {
		ev.items = append(ev.items, verdict.Item{Slug: DownstreamRequestSlug, Category: verdict.Coherence, Level: lvl, Passed: open == 0, Applicable: true})
	}
}

// oneLine is a quote on one line, with its runs of white space folded.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// carriedUpstream patches the evidence of a carried row with the upstream request that the
// verdict read, so the row shows today's state and not the state of the run that found it.
func carriedUpstream(f *pgdb.Finding, c carriedFinding) {
	if f.CheckSlug == ContradictionSlug {
		f.Evidence = withUpstream(f.Evidence, c.Upstream)
	}
}

// blocksVerdict reports whether a stored finding counts as open in a verdict: no waiver covers
// it, and it does not wait on a linked doc.
func blocksVerdict(f pgdb.Finding) bool {
	return !f.Waived && !upstreamOf(f.Evidence).waiting()
}
