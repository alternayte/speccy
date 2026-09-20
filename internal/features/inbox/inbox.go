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
			add(api.InboxItemKindWaiverRequest, b, nil,
				fmt.Sprintf("%s asks to waive %s: %s", w.RequestedBy, w.CheckSlug, w.Reason), w.CreatedAt)
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
