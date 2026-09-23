// Package inbox is each member's inbox (REQ-091): bundles waiting for their review, new
// messages and finished reviews on the bundles they author, and mentions. It is in-app only.
package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/waiver"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// window is how far back the inbox looks.
const window = 30 * 24 * time.Hour

// API serves the inbox.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	People    kernel.Directory
	// Waivers lists the waivers that wait for the caller's approval (SDD §9.1).
	Waivers *waiver.API
}

// GetInbox returns the caller's inbox, newest first.
func (a *API) GetInbox(ctx context.Context, _ api.GetInboxRequestObject) (api.GetInboxResponseObject, error) {
	act := kernel.ActorFrom(ctx)
	q := a.DB.Queries()
	seen := time.Time{}
	if st, err := q.GetUserState(ctx, act.UserID); err == nil {
		seen = st.InboxSeenAt
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	since := time.Now().UTC().Add(-window)
	me := kernel.PersonByID(ctx, a.People, act.UserID)
	bundles := map[uuid.UUID]pgdb.Bundle{}
	bundle := func(id uuid.UUID) (pgdb.Bundle, bool) {
		if b, ok := bundles[id]; ok {
			return b, true
		}
		b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: id})
		if err != nil || b.ArchivedAt.Valid {
			return b, false
		}
		if ok, _ := share.CanRead(ctx, q, act, b); !ok {
			return b, false
		}
		bundles[id] = b
		return b, true
	}
	// Local mode's one user, and an admin in no author list, own no bundle; local mode's user
	// sees every bundle's activity.
	authored := map[uuid.UUID]bool{}
	ids, err := q.ListAuthorBundles(ctx, act.UserID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		authored[id] = true
	}
	mine := func(id uuid.UUID) bool { return authored[id] || act.UserID == kernel.LocalActor.UserID }

	var items []api.InboxItem
	add := func(kind api.InboxItemKind, b pgdb.Bundle, thread *uuid.UUID, text string, at time.Time) {
		items = append(items, api.InboxItem{Kind: kind, BundleId: b.ID, BundleTitle: b.Title, ThreadId: thread, Text: text, At: at.UTC(), Unread: at.After(seen)})
	}
	// addWaiver is add for an item about one waiver: the bundle page opens on the finding it
	// excuses (SDD §9.1).
	addWaiver := func(kind api.InboxItemKind, b pgdb.Bundle, id uuid.UUID, text string, at time.Time) {
		items = append(items, api.InboxItem{Kind: kind, BundleId: b.ID, BundleTitle: b.Title, WaiverId: &id, Text: text, At: at.UTC(), Unread: at.After(seen)})
	}

	// Bundles waiting for my review.
	reviewing, err := q.ListReviewerBundles(ctx, act.UserID)
	if err != nil {
		return nil, err
	}
	for _, id := range reviewing {
		sv, err := q.GetBundleStatusView(ctx, id)
		if err != nil || sv.Status != "in_review" {
			continue
		}
		b, ok := bundle(id)
		if !ok {
			continue
		}
		var approvals []struct {
			By      string    `json:"by"`
			Version uuid.UUID `json:"version"`
		}
		_ = json.Unmarshal(sv.Approvals, &approvals)
		if slices.ContainsFunc(approvals, func(x struct {
			By      string    `json:"by"`
			Version uuid.UUID `json:"version"`
		}) bool {
			return x.By == act.UserID && x.Version == b.CurrentVersionID.UUID
		}) {
			continue
		}
		at := sv.UpdatedAt
		if sv.ReviewRequestedAt.Valid {
			at = sv.ReviewRequestedAt.Time
		}
		add(api.InboxItemKindReviewRequest, b, nil, "Waiting for your review.", at)
	}

	// Waivers that wait for my approval. A profile maintainer is not always on the bundle, so
	// this is the only place they meet the request.
	if a.Waivers != nil {
		waiting, err := a.Waivers.Waiting(ctx)
		if err != nil {
			return nil, err
		}
		for _, w := range waiting {
			b, ok := bundle(w.BundleId)
			if !ok {
				continue
			}
			addWaiver(api.InboxItemKindWaiverRequest, b, w.Id,
				fmt.Sprintf("%s asks to waive %s: %s", w.RequestedBy, w.CheckSlug, w.Reason), w.CreatedAt)
		}

		// A rejection goes to the person who asked. An ended waiver goes to the authors, who
		// usually caused it with an edit and do not know (REQ-074).
		decided, err := a.Waivers.DecidedSince(ctx, since)
		if err != nil {
			return nil, err
		}
		for _, d := range decided {
			b, ok := bundle(d.Waiver.BundleId)
			if !ok {
				continue
			}
			switch {
			case d.Waiver.Status == api.WaiverStatusRejected && d.RequestedBy == act.UserID:
				text := fmt.Sprintf("Your waiver of %s is rejected.", d.Waiver.CheckSlug)
				if d.Waiver.DecisionReason != nil {
					text += " " + *d.Waiver.DecisionReason
				}
				addWaiver(api.InboxItemKindWaiverRejected, b, d.Waiver.Id, text, d.At)
			case d.Waiver.Status == api.WaiverStatusInvalidated && mine(b.ID):
				addWaiver(api.InboxItemKindWaiverEnded, b, d.Waiver.Id,
					fmt.Sprintf("The waiver of %s ended: %s changed. Run the review again.", d.Waiver.CheckSlug, sectionName(d.Waiver.Section)), d.At)
			}
		}
	}

	// Messages on my bundles, and mentions of me anywhere I can read.
	msgs, err := q.ListMessagesSince(ctx, pgdb.ListMessagesSinceParams{WorkspaceID: a.Workspace, Since: since})
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		if m.AuthorID == act.UserID || !m.BundleID.Valid {
			continue
		}
		b, ok := bundle(m.BundleID.UUID)
		if !ok {
			continue
		}
		tid := m.ThreadID
		switch {
		case mentions(m.Body, me):
			add(api.InboxItemKindMention, b, &tid, fmt.Sprintf("%s mentioned you in “%s”: %s", m.AuthorName, m.Title, snippet(m.Body)), m.CreatedAt)
		case mine(b.ID):
			add(api.InboxItemKindMessage, b, &tid, fmt.Sprintf("%s in “%s”: %s", m.AuthorName, m.Title, snippet(m.Body)), m.CreatedAt)
		}
	}

	// Finished reviews of my bundles.
	runs, err := q.ListFullRunsSince(ctx, pgdb.ListFullRunsSinceParams{WorkspaceID: a.Workspace, Since: sql.NullTime{Time: since, Valid: true}})
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if !mine(r.BundleID) || !r.FinishedAt.Valid {
			continue
		}
		b, ok := bundle(r.BundleID)
		if !ok {
			continue
		}
		text := "The review failed: " + r.Error
		if r.Status == "complete" {
			text = "The review finished."
			if v, err := q.GetVerdict(ctx, r.ID); err == nil {
				text = map[string]string{"build_ready": "The review finished: Build Ready.", "not_build_ready": "The review finished: Not Build Ready."}[v.Result]
			}
		}
		add(api.InboxItemKindRun, b, nil, text, r.FinishedAt.Time)
	}

	// A key names each item, so a click marks one item read. An item is unread when it is
	// newer than "mark all read" and the person has not opened it.
	read := map[string]bool{}
	keys, err := q.ListInboxRead(ctx, pgdb.ListInboxReadParams{UserID: act.UserID, Since: since})
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		read[k] = true
	}
	for i := range items {
		items[i].Key = itemKey(items[i])
		items[i].Unread = items[i].Unread && !read[items[i].Key]
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At.After(items[j].At) })
	if len(items) > 100 {
		items = items[:100]
	}
	if items == nil {
		items = []api.InboxItem{}
	}
	return api.GetInbox200JSONResponse{Items: items, SeenAt: seen.UTC()}, nil
}

// MarkInboxSeen marks the inbox read up to now.
func (a *API) MarkInboxSeen(ctx context.Context, _ api.MarkInboxSeenRequestObject) (api.MarkInboxSeenResponseObject, error) {
	if err := a.DB.Queries().SetInboxSeen(ctx, pgdb.SetInboxSeenParams{UserID: kernel.ActorFrom(ctx).UserID, InboxSeenAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	return api.MarkInboxSeen204Response{}, nil
}

// sectionName names the section a waiver covers, for a sentence.
func sectionName(path []string) string {
	if len(path) == 0 {
		return "the doc"
	}
	return strings.Join(path, " › ")
}

// mentions reports whether body mentions the person: @ and their email, or @ and the part of
// the email before the @.
func mentions(body string, p kernel.Person) bool {
	lower := strings.ToLower(body)
	for _, name := range []string{p.Email, strings.SplitN(p.Email, "@", 2)[0]} {
		if name == "" {
			continue
		}
		at := "@" + strings.ToLower(name)
		for i := strings.Index(lower, at); i >= 0; {
			end := i + len(at)
			if end == len(lower) || !isNameChar(lower[end]) || (lower[end] == '.' && (end+1 == len(lower) || !isAlnum(lower[end+1]))) {
				return true
			}
			next := strings.Index(lower[end:], at)
			if next < 0 {
				break
			}
			i = end + next
		}
	}
	return false
}

func isAlnum(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '@'
}

func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		return string(r[:139]) + "…"
	}
	return s
}

// itemKey names one inbox item: its kind, its bundle, its thread or waiver, and its time.
func itemKey(i api.InboxItem) string {
	k := string(i.Kind) + "|" + i.BundleId.String()
	if i.ThreadId != nil {
		k += "|t:" + i.ThreadId.String()
	}
	if i.WaiverId != nil {
		k += "|w:" + i.WaiverId.String()
	}
	return k + "|" + strconv.FormatInt(i.At.UnixNano(), 10)
}

// MarkInboxItemRead marks one inbox item read, when the person opens it.
func (a *API) MarkInboxItemRead(ctx context.Context, req api.MarkInboxItemReadRequestObject) (api.MarkInboxItemReadResponseObject, error) {
	if err := a.DB.Queries().MarkInboxItemRead(ctx, pgdb.MarkInboxItemReadParams{UserID: kernel.ActorFrom(ctx).UserID,
		ItemKey: req.Body.Key, ReadAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	return api.MarkInboxItemRead204Response{}, nil
}
