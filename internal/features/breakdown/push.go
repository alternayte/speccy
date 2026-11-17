package breakdown

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/handoff"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/kernel"
)

// HandoffLabel is the label of the handoff a push records.
const HandoffLabel = "breakdown"

// pushAnyway says how to push a breakdown of a doc that is not Build Ready.
const pushAnyway = "push it anyway with speccy breakdown push --acknowledged, or with acknowledged: true in the API."

// Created is one issue a push created.
type Created struct {
	Kind, ID, Title, Key, URL string
}

// PushResult is what one push did.
type PushResult struct {
	Handoff uuid.UUID
	Created []Created
	View    View
}

// Push creates the epics and the items that have no issue yet, in the tracker the person names.
// It refuses a stale breakdown, a breakdown with a MUST finding, and a doc that is not Build
// Ready unless acknowledged. It never edits or closes an issue. Each issue is recorded as it is
// created, so a push that fails half way creates only the rest when it runs again.
func (a *API) Push(ctx context.Context, docID uuid.UUID, basedOn int64, kind, target string, ack bool) (PushResult, error) {
	q := a.DB.Queries()
	v, err := a.view(ctx, q, docID)
	if err != nil {
		return PushResult{}, err
	}
	if v.Row.Revision != basedOn {
		return PushResult{}, kernel.Conflict("breakdown_changed",
			"Someone changed this breakdown after you read it (revision %d, you read %d). Read it again, then push.",
			v.Row.Revision, basedOn)
	}
	if v.Stale {
		return PushResult{}, kernel.Conflict("breakdown_stale",
			"The doc has version %d, and the breakdown quotes version %d. Update the breakdown to this version, then push.",
			v.CurrentVersion, v.VersionNumber)
	}
	if n := mustCount(v.Findings); n > 0 {
		return PushResult{}, kernel.Conflict("breakdown_must",
			"The breakdown has %d MUST finding%s. Fix %s, then push: %s", n, plural(n), them(n), mustMessages(v.Findings))
	}
	sum, _, err := review.Summary(ctx, q, v.Bundle)
	if err != nil {
		return PushResult{}, err
	}
	result := handoff.VerdictOf(sum)
	if err := handoff.Allow(result, sum, ack, pushAnyway); err != nil {
		return PushResult{}, err
	}
	for _, is := range v.Issues {
		if is.Tracker != kind {
			return PushResult{}, kernel.Conflict("other_tracker",
				"This breakdown was pushed to %s. Push the new items there too, so one tracker holds the whole breakdown.", is.Tracker)
		}
	}
	todo := pushOrder(v.Body.Items)
	todo = slices.DeleteFunc(todo, func(it Item) bool { return v.issueOf("item", it.ID) != nil })
	if len(todo) == 0 {
		return PushResult{View: v}, nil
	}
	deps := slices.ContainsFunc(todo, func(it Item) bool { return len(it.DependsOn) > 0 })

	tr, err := a.openTracker(ctx, kind, target)
	if err != nil {
		return PushResult{}, err
	}
	defer tr.close()
	if err := tr.check(ctx, deps); err != nil {
		return PushResult{}, err
	}

	h := pgdb.Handoff{ID: kernel.NewID(), WorkspaceID: a.Workspace, SpecDocID: v.Bundle.ID, VersionID: v.Row.VersionID,
		Verdict: result, Acknowledged: ack && result != "build_ready", Label: HandoffLabel,
		TakenBy: kernel.ActorFrom(ctx).UserID, CreatedAt: a.now()}
	// The handoff is written with the first issue, so a push the tracker refuses at once
	// records no handoff.
	inserted := false
	out := PushResult{Handoff: h.ID}
	created := map[string]issue{}
	for _, is := range v.Issues {
		created[is.Kind+":"+is.ItemID] = issue{Key: is.IssueKey, URL: is.IssueUrl, Ref: is.IssueRef}
	}
	record := func(k, id, title string, traceIDs []string, is issue) error {
		if !inserted {
			if err := q.InsertHandoff(ctx, pgdb.InsertHandoffParams(h)); err != nil {
				return err
			}
			inserted = true
		}
		created[k+":"+id] = is
		out.Created = append(out.Created, Created{Kind: k, ID: id, Title: title, Key: is.Key, URL: is.URL})
		ids, _ := json.Marshal(nonNil(traceIDs))
		return q.InsertBreakdownIssue(ctx, pgdb.InsertBreakdownIssueParams{
			ID: kernel.NewID(), WorkspaceID: a.Workspace, SpecDocID: v.Bundle.ID, BreakdownID: v.Row.ID, Kind: k,
			ItemID: id, Tracker: kind, IssueKey: is.Key, IssueUrl: is.URL, IssueRef: is.Ref, VersionID: v.Row.VersionID,
			HandoffID: h.ID, TraceIds: ids, PushedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: a.now(),
		})
	}
	fail := func(err error) (PushResult, error) {
		if len(out.Created) == 0 {
			return PushResult{}, kernel.Invalid("push_failed", "The tracker refused the push: %s.", strings.TrimSuffix(err.Error(), "."))
		}
		return PushResult{}, kernel.Invalid("push_failed",
			"The tracker refused the push after %d issue%s: %s. Speccy keeps the issues it created; push again to create the rest.",
			len(out.Created), plural(len(out.Created)), strings.TrimSuffix(err.Error(), "."))
	}
	for _, e := range v.Body.Epics {
		if _, ok := created["epic:"+e.ID]; ok {
			continue
		}
		if !slices.ContainsFunc(todo, func(it Item) bool { return it.Epic == e.ID }) {
			continue
		}
		blk := a.block(v, h.ID, result, nil, "")
		is, err := tr.createEpic(ctx, e.Title, epicBody(e, v.Body.Items, blk))
		if err != nil {
			return fail(err)
		}
		if err := record("epic", e.ID, e.Title, nil, is); err != nil {
			return PushResult{}, err
		}
	}
	for _, it := range todo {
		var blockedBy []issue
		for _, d := range it.DependsOn {
			if is, ok := created["item:"+d]; ok {
				blockedBy = append(blockedBy, is)
			}
		}
		epicTitle := ""
		if e := v.Body.epic(it.Epic); e != nil {
			epicTitle = e.Title
		}
		blk := a.block(v, h.ID, result, it.TraceIDs, it.ID)
		body := issueBody(it, itemAnswers(v.Answers, it), epicTitle, blk)
		is, err := tr.createItem(ctx, it.Title, body, created["epic:"+it.Epic], effective(v.Body, it), blockedBy)
		if is.Key != "" {
			// The issue exists even when a later call for it failed, so Speccy records it.
			if rerr := record("item", it.ID, it.Title, it.TraceIDs, is); rerr != nil {
				return PushResult{}, rerr
			}
		}
		if err != nil {
			return fail(err)
		}
	}
	if out.View, err = a.view(ctx, q, docID); err != nil {
		return PushResult{}, err
	}
	return out, nil
}

// block is the Speccy block of one issue.
func (a *API) block(v View, handoffID uuid.UUID, verdict string, traceIDs []string, itemID string) Block {
	base := strings.TrimSuffix(a.BaseURL, "/")
	if base == "" {
		// Local mode serves the app on this address unless --addr moves it.
		base = "http://127.0.0.1:7878"
	}
	blk := Block{Bundle: v.Bundle.Slug, Version: v.VersionNumber, Handoff: handoffID.String(), Verdict: verdict,
		TraceIDs: nonNil(traceIDs),
		// The doc's text at the pinned version, which a person with access or a builder with a
		// token reads.
		Doc: fmt.Sprintf("%s/api/v1/docs/%s/files/content?path=%s&version=%s", base, v.Bundle.ID,
			url.QueryEscape(v.Bundle.DocPath), v.Row.VersionID)}
	if itemID != "" {
		blk.WorkItem = fmt.Sprintf("%s/docs/%s/breakdown?item=%s", base, v.Bundle.ID, url.QueryEscape(itemID))
	}
	return blk
}

// pushOrder orders items so that each comes after the items it depends on, keeping the
// breakdown's order where the dependencies allow. A cycle is a MUST finding, so a push never
// sees one; an item in one keeps its place.
func pushOrder(items []Item) []Item {
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	done := map[string]bool{}
	visiting := map[string]bool{}
	var out []Item
	var visit func(it Item)
	visit = func(it Item) {
		if done[it.ID] || visiting[it.ID] {
			return
		}
		visiting[it.ID] = true
		for _, d := range it.DependsOn {
			if dep, ok := byID[d]; ok {
				visit(dep)
			}
		}
		visiting[it.ID] = false
		done[it.ID] = true
		out = append(out, it)
	}
	for _, it := range items {
		visit(it)
	}
	return out
}

func mustMessages(fs []Finding) string {
	var ms []string
	for _, f := range fs {
		if f.Level == checkLevelMust {
			ms = append(ms, f.Message)
		}
	}
	return strings.Join(ms, " ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
