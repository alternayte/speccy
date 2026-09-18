// Package version records bundle versions and compares them (REQ-005, REQ-006).
// A version is an immutable set of (path, blob hash) pairs.
package version

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// ErrConflict means the bundle got a newer version after the change was based on it.
var ErrConflict = kernel.Conflict("version_conflict",
	"The bundle changed since you loaded it. Reload the bundle, then make the change again.")

// Change is one new state of a bundle's files.
type Change struct {
	Bundle    pgdb.Bundle
	Files     []source.File
	Title     string
	Profile   string
	MainDoc   string
	CreatedBy string
	Message   string
}

// Record creates a version for c.Files when they differ from the bundle's current version,
// and moves the bundle head to it. It returns the current version and false when nothing
// changed. It fails with ErrConflict when the head moved since c.Bundle was read.
func Record(ctx context.Context, tx store.Tx, c Change) (pgdb.Version, bool, error) {
	q := tx.Queries()
	b := c.Bundle
	hashes := make(map[string]string, len(c.Files))
	for _, f := range c.Files {
		hashes[f.Path] = Hash(f.Content)
	}
	if b.CurrentVersionID.Valid {
		cur, err := q.ListVersionFiles(ctx, b.CurrentVersionID.UUID)
		if err != nil {
			return pgdb.Version{}, false, err
		}
		same := len(cur) == len(hashes)
		for _, f := range cur {
			if hashes[f.Path] != f.Sha256 {
				same = false
				break
			}
		}
		if same && b.Title == c.Title && b.ProfileKey == c.Profile && b.MainDoc == c.MainDoc {
			v, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: b.CurrentVersionID.UUID})
			return v, false, err
		}
	}

	now := time.Now().UTC()
	for _, f := range c.Files {
		if err := q.InsertBlob(ctx, pgdb.InsertBlobParams{Sha256: hashes[f.Path], Content: f.Content, Size: int64(len(f.Content))}); err != nil {
			return pgdb.Version{}, false, err
		}
	}
	number, err := q.NextVersionNumber(ctx, b.ID)
	if err != nil {
		return pgdb.Version{}, false, err
	}
	v := pgdb.Version{
		ID: kernel.NewID(), WorkspaceID: b.WorkspaceID, BundleID: b.ID, Number: number,
		CreatedBy: c.CreatedBy, Message: c.Message, CreatedAt: now,
	}
	if err := q.InsertVersion(ctx, pgdb.InsertVersionParams(v)); err != nil {
		return pgdb.Version{}, false, err
	}
	for _, f := range c.Files {
		if err := q.InsertVersionFile(ctx, pgdb.InsertVersionFileParams{VersionID: v.ID, Path: f.Path, Sha256: hashes[f.Path]}); err != nil {
			return pgdb.Version{}, false, err
		}
	}
	n, err := q.UpdateBundleHead(ctx, pgdb.UpdateBundleHeadParams{
		ID: b.ID, Title: c.Title, ProfileKey: c.Profile, MainDoc: c.MainDoc,
		CurrentVersionID:  uuid.NullUUID{UUID: v.ID, Valid: true},
		ExpectedVersionID: b.CurrentVersionID, UpdatedAt: now,
	})
	if err != nil {
		return pgdb.Version{}, false, err
	}
	if n == 0 {
		return pgdb.Version{}, false, ErrConflict
	}
	return v, true, nil
}

// Hash returns the blob hash of content: lowercase hex SHA-256.
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Files returns the files of a version, sorted by path.
func Files(ctx context.Context, q store.Querier, versionID uuid.UUID) ([]source.File, error) {
	rows, err := q.ListVersionFiles(ctx, versionID)
	if err != nil {
		return nil, err
	}
	files := make([]source.File, len(rows))
	for i, r := range rows {
		content, err := q.GetBlob(ctx, r.Sha256)
		if err != nil {
			return nil, err
		}
		files[i] = source.File{Path: r.Path, Content: content}
	}
	return files, nil
}

// Bundle returns a bundle of the workspace, or a not-found error.
func Bundle(ctx context.Context, q store.Querier, workspace, id uuid.UUID) (pgdb.Bundle, error) {
	b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return pgdb.Bundle{}, kernel.NotFound("bundle_not_found", "No bundle has the ID %s.", id)
	}
	return b, err
}

// Get returns version id of bundle b, or the current version when id is nil.
func Get(ctx context.Context, q store.Querier, b pgdb.Bundle, id *uuid.UUID) (pgdb.Version, error) {
	vid := b.CurrentVersionID.UUID
	if id != nil {
		vid = *id
	} else if !b.CurrentVersionID.Valid {
		return pgdb.Version{}, kernel.NotFound("version_not_found", "The bundle has no version yet.")
	}
	v, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: vid})
	if errors.Is(err, sql.ErrNoRows) {
		return pgdb.Version{}, kernel.NotFound("version_not_found", "The bundle has no version with the ID %s.", vid)
	}
	return v, err
}

// ToAPI returns the API form of a version.
func ToAPI(v pgdb.Version) api.Version {
	return api.Version{Id: v.ID, Number: v.Number, CreatedBy: v.CreatedBy, Message: v.Message, CreatedAt: v.CreatedAt.UTC()}
}
