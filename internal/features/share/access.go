// Package share holds who can see a bundle: visibility (REQ-084), share links (REQ-085), and
// guests (REQ-086).
package share

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Visibility values (REQ-084).
const (
	Private  = "private"  // authors and named members
	Internal = "internal" // every workspace member
	Link     = "link"     // every member, and anyone with the share link as a guest
)

// CanRead reports whether the actor can see spec doc d. Visibility belongs to the bundle that
// holds d. An admin sees every bundle (SDD §3).
func CanRead(ctx context.Context, q store.Querier, a kernel.Actor, d pgdb.SpecDoc) (bool, error) {
	if a.UserID == "" && a.Guest == nil {
		return false, nil
	}
	b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: d.WorkspaceID, ID: d.BundleID})
	if err != nil {
		return false, err
	}
	return CanReadBundle(ctx, q, a, b)
}

// CanReadBundle reports whether the actor can see bundle b and every spec doc in it.
func CanReadBundle(ctx context.Context, q store.Querier, a kernel.Actor, b pgdb.Bundle) (bool, error) {
	if a.Guest != nil {
		return a.Guest.BundleID == b.ID && b.Visibility == Link, nil
	}
	if a.UserID == "" {
		return false, nil
	}
	if a.IsAdmin() || b.Visibility != Private {
		return true, nil
	}
	return q.IsBundleMember(ctx, pgdb.IsBundleMemberParams{BundleID: b.ID, UserID: a.UserID})
}

// CanEdit reports whether the actor can change spec doc d's files and settings: an author of
// the bundle that holds d, or an admin.
func CanEdit(ctx context.Context, q store.Querier, a kernel.Actor, d pgdb.SpecDoc) (bool, error) {
	if ReadOnly(d) != "" {
		return false, nil
	}
	return CanEditBundle(ctx, q, a, d.BundleID)
}

// ReadOnly says why nobody can edit or review spec doc d any more, or "": the doc of a pull
// request that closed, or that merged while another bundle holds the doc on the base branch
// (docs/specs/pr-source-and-github-review.md).
func ReadOnly(d pgdb.SpecDoc) string {
	var ref struct {
		ReadOnly string `json:"read_only"`
	}
	_ = json.Unmarshal(d.SourceRef, &ref)
	switch ref.ReadOnly {
	case "closed":
		return "The pull request of this doc closed without a merge, so the doc is read-only."
	case "merged":
		return "The pull request of this doc merged, and another bundle holds the doc on the base branch, so this one is read-only."
	}
	return ""
}

// CanEditBundle reports whether the actor can change bundle id and its spec docs: an author of
// the bundle, or an admin. Nobody edits a bundle whose spec docs are all read-only; CanEdit
// refuses one read-only doc of a bundle.
func CanEditBundle(ctx context.Context, q store.Querier, a kernel.Actor, id uuid.UUID) (bool, error) {
	if a.Guest != nil || a.UserID == "" {
		return false, nil
	}
	docs, err := q.ListSpecDocsOfBundle(ctx, id)
	if err != nil {
		return false, err
	}
	live, frozen := 0, 0
	for _, d := range docs {
		if d.ArchivedAt.Valid {
			continue
		}
		if ReadOnly(d) != "" {
			frozen++
		} else {
			live++
		}
	}
	if frozen > 0 && live == 0 {
		return false, nil
	}
	if a.IsAdmin() {
		return true, nil
	}
	return q.IsBundleAuthor(ctx, pgdb.IsBundleAuthorParams{BundleID: id, UserID: a.UserID})
}
