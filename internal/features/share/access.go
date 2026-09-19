// Package share holds who can see a bundle: visibility (REQ-084), share links (REQ-085), and
// guests (REQ-086).
package share

import (
	"context"

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

// CanRead reports whether the actor can see b. An admin sees every bundle (SDD §3).
func CanRead(ctx context.Context, q store.Querier, a kernel.Actor, b pgdb.Bundle) (bool, error) {
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

// CanEdit reports whether the actor can change b's files and settings: an author, or an admin.
func CanEdit(ctx context.Context, q store.Querier, a kernel.Actor, b pgdb.Bundle) (bool, error) {
	if a.Guest != nil || a.UserID == "" {
		return false, nil
	}
	if a.IsAdmin() {
		return true, nil
	}
	return q.IsBundleAuthor(ctx, pgdb.IsBundleAuthorParams{BundleID: b.ID, UserID: a.UserID})
}
