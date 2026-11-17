package breakdown

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/features/handoff"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// changes names what the doc's current version changed in each item, against the version the
// breakdown quotes. An item with no change has no entry.
func changes(b Body, d docText) map[string][]string {
	now := map[string]string{}
	for _, def := range d.defs {
		now[def.ID] = defText(d.main, def)
	}
	out := map[string][]string{}
	for _, it := range b.Items {
		var why []string
		for _, t := range it.TraceIDs {
			text, ok := now[t]
			switch {
			case !ok:
				why = append(why, t+" is gone from the doc.")
			case text != b.Defs[t]:
				why = append(why, t+" changed.")
			}
		}
		for _, c := range it.Criteria {
			if c.Doc != "" || c.TraceID != "" {
				continue
			}
			if anchor.Count(d.main, c.Quote) != 1 {
				why = append(why, fmt.Sprintf("The doc no longer says %q.", short(c.Quote)))
			}
		}
		if len(why) > 0 {
			out[it.ID] = why
		}
	}
	return out
}

// newTraceIDs are the trace IDs the current version defines that the breakdown's version did
// not, in document order.
func newTraceIDs(b Body, d docText) []string {
	var out []string
	for _, def := range d.defs {
		if _, ok := b.Defs[def.ID]; !ok {
			out = append(out, def.ID)
		}
	}
	return out
}

// UpdateToVersion moves the breakdown to the doc's current version. It quotes each criterion
// again from the new text, keeps the edits, the assignees and the pushed issues, and adds an
// unpushed item for each new trace ID. Each item says what changed in it.
func (a *API) UpdateToVersion(ctx context.Context, docID uuid.UUID, basedOn int64, ack bool) (Stored, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, docID)
	if err != nil {
		return Stored{}, err
	}
	v, _, err := review.Summary(ctx, q, b)
	if err != nil {
		return Stored{}, err
	}
	result := handoff.VerdictOf(v)
	if err := handoff.Allow(result, v, ack, takeAnyway); err != nil {
		return Stored{}, err
	}
	d, err := a.read(ctx, q, b, true)
	if err != nil {
		return Stored{}, err
	}
	return a.edit(ctx, b.ID, basedOn, func(s *Stored, _ store.Querier) error {
		if s.Row.VersionID == b.CurrentVersionID.UUID {
			return kernel.Invalid("breakdown_current", "The breakdown already quotes the doc's current version.")
		}
		reQuote(&s.Body, d)
		s.Row.VersionID = b.CurrentVersionID.UUID
		s.Row.Verdict = result
		s.Row.Acknowledged = ack && result != "build_ready"
		return nil
	})
}

// reQuote moves body to the text of d, as UpdateToVersion describes.
func reQuote(body *Body, d docText) {
	why := changes(*body, d)
	fresh := newTraceIDs(*body, d)
	defs := map[string]lint.Definition{}
	for _, def := range d.defs {
		defs[def.ID] = def
	}
	linked := map[string][]byte{}
	for _, l := range d.linked {
		linked[l.Slug] = l.Text
	}
	for i := range body.Items {
		it := &body.Items[i]
		before := hashItem(*it)
		it.TraceIDs = slices.DeleteFunc(it.TraceIDs, func(t string) bool { _, ok := defs[t]; return !ok })
		var crit []Criterion
		for _, c := range it.Criteria {
			switch {
			case c.TraceID != "":
				if def, ok := defs[c.TraceID]; ok && slices.Contains(it.TraceIDs, c.TraceID) {
					crit = append(crit, definitionCriterion(d, def))
				}
			default:
				if nc, ok := quote(d, linked, c.Doc, c.Quote); ok {
					crit = append(crit, nc)
				}
			}
		}
		for _, t := range it.TraceIDs {
			if !hasTrace(crit, t) {
				crit = append(crit, definitionCriterion(d, defs[t]))
			}
		}
		it.Criteria = nonNil(crit)
		if len(it.HeadingPath) > 0 && sectionGone(d, it.HeadingPath) {
			why[it.ID] = append(why[it.ID], fmt.Sprintf("The section %q is gone from the doc.", strings.Join(it.HeadingPath, " > ")))
			it.HeadingPath = nil
		}
		it.Changed = why[it.ID]
		if it.Readers != nil && hashItem(*it) != before {
			it.Readers = nil
		}
	}
	for _, t := range fresh {
		if covered(*body, t) {
			continue
		}
		body.NextItem++
		item := Item{ID: fmt.Sprintf("i%d", body.NextItem), Epic: epicNear(*body, d, t),
			Title: t + ": " + short(oneLine(defs[t].Text)), TraceIDs: []string{t},
			Criteria: []Criterion{definitionCriterion(d, defs[t])}, Repos: []string{}, DependsOn: []string{},
			Questions: []string{}, Changed: []string{fmt.Sprintf("%s is new in version %d.", t, d.version.Number)}}
		if len(d.repos) == 1 {
			item.Repos = []string{d.repos[0]}
		}
		body.Items = append(body.Items, item)
	}
	body.Covered = slices.DeleteFunc(body.Covered, func(c Cover) bool { _, ok := defs[c.TraceID]; return !ok })
	body.Defs = map[string]string{}
	for _, def := range d.defs {
		body.Defs[def.ID] = defText(d.main, def)
	}
}

func sectionGone(d docText, path []string) bool {
	for _, s := range d.doc.Sections {
		if slices.Equal(s.Path, path) {
			return false
		}
	}
	return true
}

func covered(b Body, t string) bool {
	for _, it := range b.Items {
		if slices.Contains(it.TraceIDs, t) {
			return true
		}
	}
	return slices.ContainsFunc(b.Covered, func(c Cover) bool { return c.TraceID == t })
}

// epicNear is the epic of the item that holds the trace ID defined just before t, so a new
// requirement lands beside its neighbours. With none, the first epic; with no epic, a new one.
func epicNear(b Body, d docText, t string) string {
	prev := ""
	for _, def := range d.defs {
		if def.ID == t {
			break
		}
		prev = def.ID
	}
	for _, it := range b.Items {
		if prev != "" && slices.Contains(it.TraceIDs, prev) {
			return it.Epic
		}
	}
	if len(b.Epics) > 0 {
		return b.Epics[0].ID
	}
	return ""
}

func short(s string) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > 80 {
		return string(r[:77]) + "..."
	}
	return s
}

// pushedItems are the IDs of the items and the epics a push made an issue for.
func (a *API) pushedItems(ctx context.Context, q store.Querier, breakdownID uuid.UUID) (map[string]bool, error) {
	rows, err := q.ListBreakdownIssues(ctx, breakdownID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		if r.Kind == "item" {
			out[r.ItemID] = true
		}
	}
	return out, nil
}
