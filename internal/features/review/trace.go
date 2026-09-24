package review

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/coherence"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// idSuggestion is a trace ID that Speccy suggests for an unnumbered item (REQ-052). Insert
// is where "**ID:** " goes in the main doc.
type idSuggestion struct {
	ID     string
	Insert int
	Text   string
	Anchor anchor.Anchor
}

var leadingIDRe = regexp.MustCompile(`^\**\s*[A-Z]{2,6}-\d+\b`)

// suggestIDs returns an ID for each top-level list item without one, under a heading that
// names requirements, non-functional requirements, or decisions, for the profile's
// prefixes. Numbers continue after the highest number the doc uses for the prefix.
// Deterministic: the same doc gives the same suggestions (REQ-136).
func suggestIDs(file string, main []byte, doc section.Doc, prefixes []string) []idSuggestion {
	next := map[string]int{}
	for _, d := range append(lint.Definitions(main, prefixes), lint.References(main, prefixes)...) {
		p, n, _ := strings.Cut(d.ID, "-")
		if v, err := strconv.Atoi(n); err == nil && v > next[p] {
			next[p] = v
		}
	}
	var out []idSuggestion
	for _, it := range lint.ListItems(main) {
		if leadingIDRe.MatchString(strings.TrimSpace(it.Text)) {
			continue
		}
		prefix := ""
		for _, s := range doc.Sections {
			if s.Level == 0 || it.Start < s.Start || it.Start >= s.End {
				continue
			}
			if p := lint.SectionPrefix(s.Path, prefixes); p != "" {
				prefix = p
			}
		}
		if prefix == "" {
			continue
		}
		next[prefix]++
		end := it.Start + len(it.Text)
		out = append(out, idSuggestion{
			ID: fmt.Sprintf("%s-%03d", prefix, next[prefix]), Insert: it.Start, Text: strings.TrimSpace(it.Text),
			Anchor: anchor.New(file, main, doc, it.Start, end),
		})
	}
	return out
}

// applyIDs inserts the chosen suggestions into main.
func applyIDs(main []byte, chosen []idSuggestion) []byte {
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Insert > chosen[j].Insert })
	out := append([]byte{}, main...)
	for _, s := range chosen {
		ins := []byte("**" + s.ID + ":** ")
		out = append(out[:s.Insert], append(ins, out[s.Insert:]...)...)
	}
	return out
}

// traceCell is one downstream doc's coverage of one upstream ID (REQ-058).
type traceCell struct {
	State  string // referenced | covered_by | out_of_scope | gap
	Refs   []anchor.Anchor
	Reason string
	Target string
}

// cellFor returns the coverage of id in a downstream main doc.
func cellFor(id, file string, main []byte, doc section.Doc, fm fmAcks, prefixes []string) traceCell {
	var c traceCell
	for _, r := range append(lint.References(main, prefixes), lint.Definitions(main, prefixes)...) {
		if r.ID == id {
			c.Refs = append(c.Refs, anchor.New(file, main, doc, r.Start, r.End))
		}
	}
	if len(c.Refs) > 0 {
		c.State = "referenced"
		return c
	}
	if a, ok := fm[id]; ok && a.Valid() {
		return traceCell{State: a.Status, Reason: a.Reason, Target: a.Target}
	}
	return traceCell{State: "gap"}
}

type fmAcks map[string]coherence.Ack

// Change writes a file to a bundle as a new version from base (the bundle feature's Change).
type Change func(ctx context.Context, id, base uuid.UUID, op source.Op, by, message string) (pgdb.Version, bool, error)

// GetTrace returns the bundle's links, its traceability matrices, and suggested IDs.
func (a *API) GetTrace(ctx context.Context, req api.GetTraceRequestObject) (api.GetTraceResponseObject, error) {
	b, err := a.bundle(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	out := api.TraceView{Links: []api.BundleLink{}, Incoming: []api.BundleLink{}, Matrices: []api.TraceMatrix{}, Suggestions: []api.IdSuggestion{}}
	if !b.CurrentVersionID.Valid {
		return api.GetTrace200JSONResponse(out), nil
	}
	s := a.Service
	p := s.Profiles()[b.ProfileKey]
	in, err := s.load(ctx, b, b.CurrentVersionID.UUID, p)
	if err != nil {
		return nil, err
	}
	states, err := a.DB.Queries().ListLinkStates(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	byRef := map[string]pgdb.LinkState{}
	for _, st := range states {
		byRef[st.TargetRef] = st
	}
	for _, l := range in.links {
		removable := a.removable(b, l.origin)
		bl := api.BundleLink{Kind: api.BundleLinkKind(l.kind), Origin: api.BundleLinkOrigin(l.origin), TargetKind: api.BundleLinkTargetKind(l.targetKind), TargetRef: l.ref, Removable: &removable}
		if l.target != nil {
			r := bundleRefAPI(*l.target)
			bl.Bundle = &r
		}
		if l.external != nil {
			bl.TargetUrl = &l.external.URL
		}
		// DEC-021: the four states of an external link, as the last run read it.
		switch st, ok := byRef[l.ref]; {
		case ok:
			state, reason, at := api.BundleLinkState(st.State), st.Reason, st.CheckedAt
			bl.State, bl.StateReason, bl.CheckedAt = &state, &reason, &at
		case l.targetKind == "external":
			state := api.Unchecked
			reason := "No review run has read this link yet."
			if l.problem != "" {
				reason = sentence(l.problem)
			}
			bl.State, bl.StateReason = &state, &reason
		}
		out.Links = append(out.Links, bl)
	}
	q := a.DB.Queries()
	incoming, err := q.ListLinksTo(ctx, uuid.NullUUID{UUID: b.ID, Valid: true})
	if err != nil {
		return nil, err
	}
	downstream := []pgdb.SpecDoc{}
	for _, l := range incoming {
		from, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: l.FromSpecDocID})
		if err != nil || from.ArchivedAt.Valid {
			continue
		}
		r := bundleRefAPI(from)
		out.Incoming = append(out.Incoming, api.BundleLink{Kind: api.BundleLinkKind(l.Kind), Origin: api.BundleLinkOrigin(l.Origin),
			TargetKind: api.BundleLinkTargetKind(l.TargetKind), TargetRef: from.Slug, Bundle: &r})
		if l.Kind == "implements" {
			downstream = append(downstream, from)
		}
	}
	if in.dec.Standalone != nil {
		out.Standalone = &api.Standalone{Reason: in.dec.Standalone.Reason, AcknowledgedBy: in.dec.Standalone.AcknowledgedBy}
	}

	// REQ-058: this bundle's IDs against the bundles that implement it, and the IDs of each
	// bundle it implements against that bundle's implementers.
	if len(downstream) > 0 {
		m, err := a.matrix(ctx, b, in.main, downstream)
		if err != nil {
			return nil, err
		}
		if m != nil {
			out.Matrices = append(out.Matrices, *m)
		}
	}
	for _, l := range in.linked {
		if l.kind != "implements" {
			continue
		}
		links, err := q.ListLinksTo(ctx, uuid.NullUUID{UUID: l.target.ID, Valid: true})
		if err != nil {
			return nil, err
		}
		downs := []pgdb.SpecDoc{b}
		for _, dl := range links {
			if dl.Kind != "implements" || dl.FromSpecDocID == b.ID {
				continue
			}
			d, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: dl.FromSpecDocID})
			if err == nil && !d.ArchivedAt.Valid {
				downs = append(downs, d)
			}
		}
		m, err := a.matrix(ctx, *l.target, l.main, downs)
		if err != nil {
			return nil, err
		}
		if m != nil {
			out.Matrices = append(out.Matrices, *m)
		}
	}
	for _, sg := range suggestIDs(b.DocPath, in.main, in.doc, p.Profile.Trace.Prefixes) {
		out.Suggestions = append(out.Suggestions, api.IdSuggestion{Id: sg.ID, Text: sg.Text, Anchor: anchorAPI(sg.Anchor)})
	}
	sections := [][]string{}
	for _, sec := range in.doc.Sections {
		if sec.Level > 0 {
			sections = append(sections, sec.Path)
		}
	}
	out.Sections = &sections
	return api.GetTrace200JSONResponse(out), nil
}

// matrix builds one traceability matrix: the upstream IDs that any downstream profile covers,
// against each downstream bundle. It is nil when no downstream profile covers a prefix.
func (a *API) matrix(ctx context.Context, up pgdb.SpecDoc, upMain []byte, downs []pgdb.SpecDoc) (*api.TraceMatrix, error) {
	profiles := a.Service.Profiles()
	var prefixes []string
	for _, d := range downs {
		for _, p := range profiles[d.ProfileKey].Profile.Trace.Cover {
			if !slices.Contains(prefixes, p) {
				prefixes = append(prefixes, p)
			}
		}
	}
	if len(prefixes) == 0 {
		return nil, nil
	}
	sort.Slice(downs, func(i, j int) bool { return downs[i].Slug < downs[j].Slug })
	upDoc := section.Parse(upMain)
	m := &api.TraceMatrix{Upstream: bundleRefAPI(up), Rows: []api.TraceRow{}, Columns: []api.BundleRef{}, Cells: [][]api.TraceCell{}, Editable: []bool{}}
	q := a.DB.Queries()
	actor := kernel.ActorFrom(ctx)
	defs := lint.Definitions(upMain, prefixes)
	for _, d := range defs {
		m.Rows = append(m.Rows, api.TraceRow{Id: d.ID, Text: strings.TrimSpace(d.Text), Anchor: anchorAPI(anchor.New(up.DocPath, upMain, upDoc, d.Start, d.End))})
		m.Cells = append(m.Cells, []api.TraceCell{})
	}
	for _, d := range downs {
		if !d.CurrentVersionID.Valid {
			continue
		}
		files, err := version.Files(ctx, q, d.CurrentVersionID.UUID)
		if err != nil {
			return nil, err
		}
		var main []byte
		for _, f := range files {
			if f.Path == d.DocPath {
				main = f.Content
			}
		}
		doc := section.Parse(main)
		var dec source.Decisions
		if a.Service.Decisions != nil {
			if dec, err = a.Service.Decisions(ctx, d); err != nil {
				return nil, err
			}
		}
		acks := fmAcks{}
		for _, t := range dec.Trace {
			acks[t.ID] = coherence.Ack{Status: t.Status, Target: t.Target, Reason: t.Reason}
		}
		cover := profiles[d.ProfileKey].Profile.Trace.Cover
		// A person who can edit the column's doc answers its gaps and withdraws its
		// acknowledgements from the matrix.
		editable, err := share.CanEditBundle(ctx, q, actor, d.BundleID)
		if err != nil {
			return nil, err
		}
		gaps, err := gapFindings(ctx, q, d)
		if err != nil {
			return nil, err
		}
		m.Columns = append(m.Columns, bundleRefAPI(d))
		m.Editable = append(m.Editable, editable)
		for i, def := range defs {
			c := cellFor(def.ID, d.DocPath, main, doc, acks, cover)
			cell := api.TraceCell{State: api.TraceCellState(c.State), Refs: []api.Anchor{}}
			if id, ok := gaps[def.ID]; ok && c.State == "gap" {
				cell.FindingId = &id
			}
			for _, r := range c.Refs {
				cell.Refs = append(cell.Refs, anchorAPI(r))
			}
			if c.Reason != "" {
				cell.Reason = &c.Reason
			}
			if c.Target != "" {
				cell.Target = &c.Target
			}
			m.Cells[i] = append(m.Cells[i], cell)
		}
	}
	return m, nil
}

// gapFindings returns the trace.coverage findings of d's verdict run by trace ID. An answer to
// a gap names its finding, as the tour and the findings rail do.
func gapFindings(ctx context.Context, q store.Querier, d pgdb.SpecDoc) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	v, _, err := Summary(ctx, q, d)
	if err != nil || v == nil {
		return out, err
	}
	rows, err := q.ListFindings(ctx, v.RunId)
	if err != nil {
		return nil, err
	}
	for _, f := range rows {
		if f.CheckSlug != CoverageSlug {
			continue
		}
		var ev struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(f.Evidence, &ev) == nil && ev.ID != "" {
			out[ev.ID] = f.ID
		}
	}
	return out, nil
}

// AddTraceIds inserts the chosen suggested IDs into the main doc as a new version (REQ-052).
func (a *API) AddTraceIds(ctx context.Context, req api.AddTraceIdsRequestObject) (api.AddTraceIdsResponseObject, error) {
	b, err := a.bundle(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	if a.Change == nil {
		return nil, kernel.Invalid("not_supported", "This server cannot change bundle files.")
	}
	if _, err := a.DB.Queries().GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: req.Params.BaseVersion}); err != nil {
		return nil, kernel.NotFound("version_not_found", "The bundle has no version %s.", req.Params.BaseVersion)
	}
	files, err := version.Files(ctx, a.DB.Queries(), req.Params.BaseVersion)
	if err != nil {
		return nil, err
	}
	var main []byte
	for _, f := range files {
		if f.Path == b.DocPath {
			main = f.Content
		}
	}
	want := map[string]bool{}
	for _, id := range req.Body.Ids {
		want[id] = true
	}
	var chosen []idSuggestion
	for _, sg := range suggestIDs(b.DocPath, main, section.Parse(main), a.Service.Profiles()[b.ProfileKey].Profile.Trace.Prefixes) {
		if want[sg.ID] {
			chosen = append(chosen, sg)
		}
	}
	if len(chosen) != len(want) {
		return nil, kernel.Invalid("suggestion_gone", "Some of these IDs are no longer suggested for this version. Reload the trace view and try again.")
	}
	ids := make([]string, len(chosen))
	for i, c := range chosen {
		ids[i] = c.ID
	}
	sort.Strings(ids)
	v, changed, err := a.Change(ctx, b.ID, req.Params.BaseVersion,
		source.Op{Kind: source.OpWrite, Path: b.DocPath, Content: applyIDs(main, chosen)}, kernel.ActorFrom(ctx).UserID, "Added trace IDs "+strings.Join(ids, ", "))
	if err != nil {
		return nil, err
	}
	return api.AddTraceIds200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}

// CoverTraceId answers a coverage gap with "This doc covers it": it adds "Covers <ID>." at the
// end of the section's own text, as a new version. The ID then sits in the doc, where a
// builder reads it, and the coverage check finds the reference.
func (a *API) CoverTraceId(ctx context.Context, req api.CoverTraceIdRequestObject) (api.CoverTraceIdResponseObject, error) {
	b, err := a.bundle(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	if a.Change == nil {
		return nil, kernel.Invalid("not_supported", "This server cannot change bundle files.")
	}
	id := strings.TrimSpace(req.Body.TraceId)
	if !traceIDRe.MatchString(id) {
		return nil, kernel.Invalid("bad_trace_id", "%q is not a trace ID, such as REQ-001.", req.Body.TraceId)
	}
	if _, err := a.DB.Queries().GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: req.Params.BaseVersion}); err != nil {
		return nil, kernel.NotFound("version_not_found", "The bundle has no version %s.", req.Params.BaseVersion)
	}
	files, err := version.Files(ctx, a.DB.Queries(), req.Params.BaseVersion)
	if err != nil {
		return nil, err
	}
	var main []byte
	for _, f := range files {
		if f.Path == b.DocPath {
			main = f.Content
		}
	}
	doc := section.Parse(main)
	i := slices.IndexFunc(doc.Sections, func(s section.Section) bool { return s.Level > 0 && slices.Equal(s.Path, req.Body.Section) })
	if i < 0 {
		return nil, kernel.Conflict("section_gone", "This doc has no section %q. Reload and pick a section again.", strings.Join(req.Body.Section, " > "))
	}
	sec := doc.Sections[i]
	head := strings.TrimRight(string(main[:sec.OwnEnd]), " \t\r\n")
	tail := string(main[sec.OwnEnd:])
	next := head + "\n\nCovers " + id + ".\n"
	if tail != "" {
		next += "\n" + tail
	}
	v, changed, err := a.Change(ctx, b.ID, req.Params.BaseVersion, source.Op{Kind: source.OpWrite, Path: b.DocPath, Content: []byte(next)},
		kernel.ActorFrom(ctx).UserID, fmt.Sprintf("Covers %s in %s", id, sec.Title))
	if err != nil {
		return nil, err
	}
	return api.CoverTraceId200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}

func bundleRefAPI(b pgdb.SpecDoc) api.BundleRef {
	return api.BundleRef{Id: b.ID, BundleId: b.BundleID, Slug: b.Slug, Title: b.Title, ProfileKey: b.ProfileKey}
}

// IDSuggestion is a trace ID that Speccy suggests for an unnumbered item (REQ-052): insert
// "**ID:** " at the byte offset Insert of the main doc.
type IDSuggestion struct {
	ID     string
	Insert int
}

// SuggestIDs returns the trace IDs that Speccy suggests for the main doc (REQ-052, REQ-136).
func SuggestIDs(file string, main []byte, prefixes []string) []IDSuggestion {
	var out []IDSuggestion
	for _, s := range suggestIDs(file, main, section.Parse(main), prefixes) {
		out = append(out, IDSuggestion{ID: s.ID, Insert: s.Insert})
	}
	return out
}
