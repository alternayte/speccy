package breakdown

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Op is one edit of a breakdown. Kind names the edit; the other fields are its arguments.
type Op struct {
	Kind string `json:"op"`
	// Item is the item the edit changes. Into, Before and Epic name another item or an epic.
	Item   string `json:"item,omitempty"`
	Into   string `json:"into,omitempty"`
	Before string `json:"before,omitempty"`
	Epic   string `json:"epic,omitempty"`
	// TraceIDs are the trace IDs a split moves to the new item. TraceID is the one trace ID a move,
	// a cover or an uncover names; From and To are the items of a move ("" is no item).
	TraceIDs []string `json:"trace_ids,omitempty"`
	TraceID  string   `json:"trace_id,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	// Title, Note, Repos and DependsOn are the new values of a set.
	Title     string    `json:"title,omitempty"`
	Note      *string   `json:"note,omitempty"`
	Repos     []string  `json:"repos,omitempty"`
	DependsOn []string  `json:"depends_on,omitempty"`
	Assignee  *Assignee `json:"assignee,omitempty"`
	// Answer and Reason close a coverage finding.
	Answer string `json:"answer,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// The edit kinds.
const (
	OpSplit        = "split"
	OpMerge        = "merge"
	OpMoveTrace    = "move_trace"
	OpReorder      = "reorder"
	OpDepend       = "set_dependencies"
	OpAssign       = "set_assignee"
	OpEpicDefault  = "set_epic_default"
	OpNote         = "set_note"
	OpTitle        = "set_title"
	OpRepos        = "set_repos"
	OpMoveItem     = "move_item"
	OpAddEpic      = "add_epic"
	OpRenameEpic   = "rename_epic"
	OpCover        = "cover"
	OpUncover      = "uncover"
	OpRemoveItem   = "remove_item"
	opNoteMaxChars = 4000
)

// Edit applies ops to the breakdown, in order and all or none. basedOn is the revision the
// caller read.
func (a *API) Edit(ctx context.Context, docID uuid.UUID, basedOn int64, ops []Op) (Stored, error) {
	if len(ops) == 0 {
		return Stored{}, kernel.Invalid("no_ops", "Name at least one change.")
	}
	// Read what the edit checks against before the transaction: SQLite has one connection, so a
	// read outside the transaction would wait for it.
	doc, err := a.docTraceIDs(ctx, docID)
	if err != nil {
		return Stored{}, err
	}
	return a.edit(ctx, docID, basedOn, func(s *Stored, q store.Querier) error {
		pushed, err := a.pushedItems(ctx, q, s.Row.ID)
		if err != nil {
			return err
		}
		for i, op := range ops {
			if err := apply(&s.Body, op, pushed, doc, kernel.ActorFrom(ctx).UserID); err != nil {
				if ke, ok := kernel.AsError(err); ok && len(ops) > 1 {
					ke.Detail = fmt.Sprintf("Change %d (%s): %s", i+1, op.Kind, ke.Detail)
				}
				return err
			}
		}
		return nil
	})
}

// edit reads the breakdown, checks the revision, runs fn, and saves. basedOn 0 takes the newest
// revision, for a change the server makes itself.
func (a *API) edit(ctx context.Context, docID uuid.UUID, basedOn int64, fn func(*Stored, store.Querier) error) (Stored, error) {
	var out Stored
	err := a.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		s, ok, err := a.load(ctx, q, docID)
		if err != nil {
			return err
		}
		if !ok {
			return noBreakdown()
		}
		if basedOn == 0 {
			basedOn = s.Row.Revision
		}
		if basedOn != s.Row.Revision {
			return kernel.Conflict("breakdown_changed",
				"Someone changed this breakdown after you read it (revision %d, you read %d). Read it again, then make your change.",
				s.Row.Revision, basedOn)
		}
		if err := fn(&s, q); err != nil {
			return err
		}
		out, err = a.save(ctx, q, s, basedOn)
		return err
	})
	return out, err
}

// docTraceIDs are the trace IDs the doc defines at its current version.
func (a *API) docTraceIDs(ctx context.Context, docID uuid.UUID) (map[string]bool, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, docID)
	if err != nil {
		return nil, err
	}
	d, err := a.read(ctx, q, b, false)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, def := range d.defs {
		out[def.ID] = true
	}
	return out, nil
}

// apply runs one edit on the body. pushed are the items with a tracker issue: their trace IDs
// cannot move, because the issue would not change with them.
func apply(b *Body, op Op, pushed map[string]bool, doc map[string]bool, actor string) error {
	need := func(id string) (*Item, error) {
		it := b.item(id)
		if it == nil {
			return nil, kernel.NotFound("no_item", "The breakdown has no item %q.", id)
		}
		return it, nil
	}
	notPushed := func(ids ...string) error {
		for _, id := range ids {
			if pushed[id] {
				return kernel.Conflict("item_pushed",
					"Item %q is already an issue in the tracker, and Speccy never edits an issue. Leave its trace IDs as they are.", id)
			}
		}
		return nil
	}
	switch op.Kind {
	case OpSplit:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		if err := notPushed(it.ID); err != nil {
			return err
		}
		if len(op.TraceIDs) == 0 || len(op.TraceIDs) >= len(it.TraceIDs) {
			return kernel.Invalid("bad_split", "Name some of the item's trace IDs, not all, to move to the new item.")
		}
		for _, t := range op.TraceIDs {
			if !slices.Contains(it.TraceIDs, t) {
				return kernel.Invalid("bad_split", "Item %q has no trace ID %s.", it.ID, t)
			}
		}
		b.NextItem++
		n := Item{ID: fmt.Sprintf("i%d", b.NextItem), Epic: it.Epic, Title: op.Title, TraceIDs: append([]string(nil), op.TraceIDs...),
			Criteria: []Criterion{}, Repos: append([]string(nil), it.Repos...), DependsOn: []string{}, Questions: []string{},
			Assignee: it.Assignee}
		if strings.TrimSpace(n.Title) == "" {
			n.Title = it.Title + " (" + strings.Join(op.TraceIDs, ", ") + ")"
		}
		// A criterion of a moved trace ID moves with it; a criterion with no trace ID stays.
		var keep []Criterion
		for _, c := range it.Criteria {
			if c.TraceID != "" && slices.Contains(op.TraceIDs, c.TraceID) {
				n.Criteria = append(n.Criteria, c)
			} else {
				keep = append(keep, c)
			}
		}
		it.Criteria = nonNil(keep)
		it.TraceIDs = slices.DeleteFunc(it.TraceIDs, func(t string) bool { return slices.Contains(op.TraceIDs, t) })
		it.Readers = nil
		at := slices.IndexFunc(b.Items, func(x Item) bool { return x.ID == it.ID })
		b.Items = slices.Insert(b.Items, at+1, n)
	case OpMerge:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		into, err := need(op.Into)
		if err != nil {
			return err
		}
		if it.ID == into.ID {
			return kernel.Invalid("bad_merge", "An item cannot merge into itself.")
		}
		if err := notPushed(it.ID, into.ID); err != nil {
			return err
		}
		for _, t := range it.TraceIDs {
			if !slices.Contains(into.TraceIDs, t) {
				into.TraceIDs = append(into.TraceIDs, t)
			}
		}
		for _, c := range it.Criteria {
			if !hasQuote(into.Criteria, c.Quote) {
				into.Criteria = append(into.Criteria, c)
			}
		}
		for _, r := range it.Repos {
			if !slices.Contains(into.Repos, r) {
				into.Repos = append(into.Repos, r)
			}
		}
		for _, d := range it.DependsOn {
			if d != into.ID && !slices.Contains(into.DependsOn, d) {
				into.DependsOn = append(into.DependsOn, d)
			}
		}
		into.Questions = append(into.Questions, it.Questions...)
		if it.Note != "" {
			into.Note = strings.TrimSpace(into.Note + "\n" + it.Note)
		}
		into.Readers = nil
		gone := it.ID
		b.Items = slices.DeleteFunc(b.Items, func(x Item) bool { return x.ID == gone })
		for i := range b.Items {
			deps := b.Items[i].DependsOn
			for j, d := range deps {
				if d == gone {
					deps[j] = op.Into
				}
			}
			b.Items[i].DependsOn = slices.DeleteFunc(uniq(deps), func(d string) bool { return d == b.Items[i].ID })
		}
	case OpMoveTrace:
		if !doc[op.TraceID] {
			return kernel.Invalid("unknown_trace_id", "The doc defines no trace ID %s.", op.TraceID)
		}
		if op.From == op.To {
			return nil
		}
		if err := notPushed(op.From, op.To); err != nil {
			return err
		}
		if op.From != "" {
			from, err := need(op.From)
			if err != nil {
				return err
			}
			if !slices.Contains(from.TraceIDs, op.TraceID) {
				return kernel.Invalid("bad_move", "Item %q has no trace ID %s.", from.ID, op.TraceID)
			}
			from.TraceIDs = slices.DeleteFunc(from.TraceIDs, func(t string) bool { return t == op.TraceID })
			from.Readers = nil
		}
		if op.To != "" {
			to, err := need(op.To)
			if err != nil {
				return err
			}
			if !slices.Contains(to.TraceIDs, op.TraceID) {
				to.TraceIDs = append(to.TraceIDs, op.TraceID)
			}
			to.Readers = nil
		}
		// The definition moves with the trace ID.
		var moved []Criterion
		for i := range b.Items {
			if b.Items[i].ID == op.From {
				b.Items[i].Criteria = slices.DeleteFunc(b.Items[i].Criteria, func(c Criterion) bool {
					if c.TraceID == op.TraceID {
						moved = append(moved, c)
						return true
					}
					return false
				})
			}
		}
		if op.To != "" {
			to := b.item(op.To)
			for _, c := range moved {
				if !hasQuote(to.Criteria, c.Quote) {
					to.Criteria = append(to.Criteria, c)
				}
			}
		}
		b.Covered = slices.DeleteFunc(b.Covered, func(c Cover) bool { return c.TraceID == op.TraceID && op.To != "" })
	case OpReorder:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		moved := *it
		b.Items = slices.DeleteFunc(b.Items, func(x Item) bool { return x.ID == moved.ID })
		at := len(b.Items)
		if op.Before != "" {
			at = slices.IndexFunc(b.Items, func(x Item) bool { return x.ID == op.Before })
			if at < 0 {
				return kernel.NotFound("no_item", "The breakdown has no item %q.", op.Before)
			}
		}
		b.Items = slices.Insert(b.Items, at, moved)
	case OpDepend:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		deps := []string{}
		for _, d := range op.DependsOn {
			if d == it.ID {
				return kernel.Invalid("bad_dependency", "An item cannot depend on itself.")
			}
			if b.item(d) == nil {
				return kernel.NotFound("no_item", "The breakdown has no item %q.", d)
			}
			if !slices.Contains(deps, d) {
				deps = append(deps, d)
			}
		}
		it.DependsOn = deps
	case OpAssign:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		if err := checkAssignee(op.Assignee); err != nil {
			return err
		}
		it.Assignee = op.Assignee
	case OpEpicDefault:
		e := b.epic(op.Epic)
		if e == nil {
			return kernel.NotFound("no_epic", "The breakdown has no epic %q.", op.Epic)
		}
		if err := checkAssignee(op.Assignee); err != nil {
			return err
		}
		e.Default = op.Assignee
	case OpNote:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		note := ""
		if op.Note != nil {
			note = strings.TrimSpace(*op.Note)
		}
		if len(note) > opNoteMaxChars {
			return kernel.Invalid("note_too_long", "A note holds at most %d characters.", opNoteMaxChars)
		}
		it.Note = note
	case OpTitle:
		t := strings.TrimSpace(op.Title)
		if t == "" {
			return kernel.Invalid("no_title", "A title cannot be empty.")
		}
		if op.Epic != "" {
			e := b.epic(op.Epic)
			if e == nil {
				return kernel.NotFound("no_epic", "The breakdown has no epic %q.", op.Epic)
			}
			e.Title = t
			return nil
		}
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		it.Title = t
	case OpRenameEpic:
		return apply(b, Op{Kind: OpTitle, Epic: op.Epic, Title: op.Title}, pushed, doc, actor)
	case OpRepos:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		repos := []string{}
		for _, r := range op.Repos {
			if r = strings.TrimSpace(r); r != "" && !slices.Contains(repos, r) {
				repos = append(repos, r)
			}
		}
		it.Repos = repos
	case OpMoveItem:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		if b.epic(op.Epic) == nil {
			return kernel.NotFound("no_epic", "The breakdown has no epic %q.", op.Epic)
		}
		it.Epic = op.Epic
	case OpAddEpic:
		t := strings.TrimSpace(op.Title)
		if t == "" {
			return kernel.Invalid("no_title", "A title cannot be empty.")
		}
		b.NextEpic++
		b.Epics = append(b.Epics, Epic{ID: fmt.Sprintf("e%d", b.NextEpic), Title: t})
	case OpRemoveItem:
		it, err := need(op.Item)
		if err != nil {
			return err
		}
		if err := notPushed(it.ID); err != nil {
			return err
		}
		if len(it.TraceIDs) > 0 {
			return kernel.Invalid("item_has_trace_ids", "Move the trace IDs of %q to another item first.", it.Title)
		}
		gone := it.ID
		b.Items = slices.DeleteFunc(b.Items, func(x Item) bool { return x.ID == gone })
		for i := range b.Items {
			b.Items[i].DependsOn = slices.DeleteFunc(b.Items[i].DependsOn, func(d string) bool { return d == gone })
		}
	case OpCover:
		if !doc[op.TraceID] {
			return kernel.Invalid("unknown_trace_id", "The doc defines no trace ID %s.", op.TraceID)
		}
		if !slices.Contains(CoverAnswers, op.Answer) {
			return kernel.Invalid("bad_answer", "The answer must be one of %s.", strings.Join(CoverAnswers, ", "))
		}
		if strings.TrimSpace(op.Reason) == "" {
			return kernel.Invalid("no_reason", "Say why %s needs no work item.", op.TraceID)
		}
		for _, it := range b.Items {
			if slices.Contains(it.TraceIDs, op.TraceID) {
				return kernel.Invalid("trace_id_in_item", "%s is in %q. Move it out of the item first.", op.TraceID, it.Title)
			}
		}
		b.Covered = slices.DeleteFunc(b.Covered, func(c Cover) bool { return c.TraceID == op.TraceID })
		b.Covered = append(b.Covered, Cover{TraceID: op.TraceID, Answer: op.Answer, Reason: strings.TrimSpace(op.Reason), By: actor})
	case OpUncover:
		b.Covered = slices.DeleteFunc(b.Covered, func(c Cover) bool { return c.TraceID == op.TraceID })
	default:
		return kernel.Invalid("bad_op", "Speccy knows no change %q.", op.Kind)
	}
	return nil
}

func checkAssignee(a *Assignee) error {
	if a == nil {
		return nil
	}
	if a.Kind != "person" && a.Kind != "agent" {
		return kernel.Invalid("bad_assignee", "An assignee is a person or an agent.")
	}
	if strings.TrimSpace(a.ID) == "" {
		return kernel.Invalid("bad_assignee", "An assignee needs the tracker's ID for them.")
	}
	return nil
}

// effective is the assignee of an item: its own, or its epic's default.
func effective(b Body, it Item) *Assignee {
	if it.Assignee != nil {
		return it.Assignee
	}
	if e := b.epic(it.Epic); e != nil {
		return e.Default
	}
	return nil
}

// uniq keeps the first of each value, in order.
func uniq(xs []string) []string {
	out := []string{}
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
