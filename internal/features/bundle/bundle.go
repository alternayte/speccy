// Package bundle keeps the bundle list in step with its source and applies file changes.
// Every change creates a version (REQ-005). In local mode the source is folders on disk;
// the db source keeps files as blobs (DEC-028).
package bundle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store"
)

// Source kinds (SDD §11.1).
const (
	KindLocal = "local"
	KindDB    = "db"
)

// LocalUser is created_by for changes in local mode, which has one implicit user (SDD §3).
const LocalUser = "local"

// Service holds the bundles of one workspace.
type Service struct {
	DB        *store.DB
	Workspace uuid.UUID
	// Local is the served folder in local mode, and nil otherwise.
	Local *local.Root
	// AfterChange runs after a sync, a change, or an import creates versions. Lint on save
	// (DEC-027) hooks in here.
	AfterChange func(context.Context) error
	// Limits returns the REQ-009 limits in force. Nil means the defaults.
	Limits func(context.Context) source.Limits
	// GitHub returns the client with the workspace token (hosted mode), and nil in local mode.
	GitHub func(context.Context) (*github.Client, error)

	mu       sync.Mutex // serialises disk changes and scans
	scan     *local.Scan
	repo     source.RepoConfig
	problems []local.Problem
	blobs    blobCache
}

// RepoConfig returns the .speccy.yaml of the last local scan.
func (s *Service) RepoConfig() source.RepoConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repo
}

// Sync scans the local folder and records a version for each bundle whose files changed.
// Bundles whose folder is gone are archived.
func (s *Service) Sync(ctx context.Context) error {
	s.mu.Lock()
	err := s.syncLocked(ctx)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.afterChange(ctx)
}

func (s *Service) afterChange(ctx context.Context) error {
	if s.AfterChange == nil {
		return nil
	}
	return s.AfterChange(ctx)
}

func (s *Service) syncLocked(ctx context.Context) error {
	if s.Local == nil {
		return nil
	}
	var problems []local.Problem
	repo, err := source.LoadRepoConfig(s.Local.Dir())
	if err != nil {
		problems = append(problems, local.Problem{Path: source.RepoConfigFile, Message: err.Error() + ". Speccy ignores the file until you fix it."})
		repo = source.RepoConfig{}
	}
	scan, err := s.Local.Scan(repo)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	found := map[string]bool{}
	for _, fb := range scan.Bundles {
		found[fb.Slug] = true
		err := s.DB.InTx(ctx, func(tx store.Tx) error {
			q := tx.Queries()
			b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: fb.Slug})
			message := "Changed on disk"
			if errors.Is(err, sql.ErrNoRows) {
				ref, _ := json.Marshal(localRef{Dir: fb.Dir, File: fb.File})
				b = pgdb.Bundle{
					ID: kernel.NewID(), WorkspaceID: s.Workspace, Slug: fb.Slug, Title: s.title(fb.Main, fb.Slug),
					ProfileKey: fb.Main.Frontmatter.Type, MainDoc: fb.Main.Path, SourceKind: KindLocal, SourceRef: dbtype.JSON(ref),
					CreatedAt: now, UpdatedAt: now,
				}
				if err := q.InsertBundle(ctx, insertParams(b)); err != nil {
					return err
				}
				message = "Found on disk"
			} else if err != nil {
				return err
			}
			if b.ArchivedAt.Valid {
				if err := q.SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{ID: b.ID, UpdatedAt: now}); err != nil {
					return err
				}
			}
			_, _, err = version.Record(ctx, tx, version.Change{
				Bundle: b, Files: fb.Files, Title: s.title(fb.Main, fb.Slug), Profile: fb.Main.Frontmatter.Type,
				MainDoc: fb.Main.Path, CreatedBy: LocalUser, Message: message,
			})
			return err
		})
		if err != nil {
			return err
		}
	}
	existing, err := s.DB.Queries().ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindLocal})
	if err != nil {
		return err
	}
	for _, b := range existing {
		if !found[b.Slug] && !b.ArchivedAt.Valid {
			err := s.DB.Queries().SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{
				ID: b.ID, ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
		}
	}
	s.scan = scan
	s.repo = repo
	s.problems = append(problems, scan.Problems...)
	return nil
}

// localRef is source_ref for a local bundle: its folder, and the file of a single-file bundle.
type localRef struct {
	Dir  string `json:"dir"`
	File string `json:"file,omitempty"`
}

// SkippedDocs returns the markdown files the last local scan passed over, newest scan first.
func (s *Service) SkippedDocs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scan == nil {
		return nil
	}
	return append([]string(nil), s.scan.Skipped...)
}

// Problems returns the problems of the last local scan.
func (s *Service) Problems() []local.Problem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]local.Problem(nil), s.problems...)
}

// Change applies op to bundle id and records a version. base is the version the change is
// based on; when the bundle has a newer version, Change fails with version.ErrConflict.
func (s *Service) Change(ctx context.Context, id, base uuid.UUID, op source.Op, by, message string) (pgdb.Version, bool, error) {
	v, changed, err := s.change(ctx, id, base, op, by, message)
	if err != nil || !changed {
		return v, changed, err
	}
	return v, changed, s.afterChange(ctx)
}

func (s *Service) change(ctx context.Context, id, base uuid.UUID, op source.Op, by, message string) (pgdb.Version, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.DB.Queries()
	b, err := version.Bundle(ctx, q, s.Workspace, id)
	if err != nil {
		return pgdb.Version{}, false, err
	}
	if b.SourceKind == KindLocal {
		// Pick up edits made on disk first, so a change never overwrites one it did not see.
		if err := s.syncLocked(ctx); err != nil {
			return pgdb.Version{}, false, err
		}
		if b, err = version.Bundle(ctx, q, s.Workspace, id); err != nil {
			return pgdb.Version{}, false, err
		}
		if b.ArchivedAt.Valid {
			return pgdb.Version{}, false, kernel.NotFound("bundle_not_found", "The folder of bundle %s is gone from disk.", b.Slug)
		}
	}
	if !b.CurrentVersionID.Valid || b.CurrentVersionID.UUID != base {
		return pgdb.Version{}, false, version.ErrConflict
	}
	files, err := version.Files(ctx, q, base)
	if err != nil {
		return pgdb.Version{}, false, err
	}
	next, err := source.Apply(files, op)
	if err != nil {
		return pgdb.Version{}, false, applyError(err)
	}
	main, err := s.mainDoc(b, next)
	if err != nil {
		return pgdb.Version{}, false, kernel.Invalid("no_main_doc", "After this change the bundle would have %s. A bundle needs exactly one markdown file with a type field in its frontmatter.", err.Error())
	}
	switch b.SourceKind {
	case KindLocal:
		if err := s.Local.Persist(s.scan, b.Slug, op); err != nil {
			return pgdb.Version{}, false, applyError(err)
		}
	case KindDB, KindGitHub:
		// A change to a GitHub bundle is a draft until Publish (REQ-123).
		if err := source.CheckLimits(next, s.limits(ctx)); err != nil {
			return pgdb.Version{}, false, err
		}
	default:
		return pgdb.Version{}, false, kernel.Invalid("source_read_only", "Bundles from %s cannot be changed here.", b.SourceKind)
	}
	var v pgdb.Version
	var changed bool
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		v, changed, err = version.Record(ctx, tx, version.Change{
			Bundle: b, Files: next, Title: s.title(main, b.Slug), Profile: main.Frontmatter.Type,
			MainDoc: main.Path, CreatedBy: by, Message: message,
		})
		return err
	})
	return v, changed, err
}

// mainDoc finds the main doc of b in files: the mapped file of a single-file bundle, or the
// REQ-001 rule for a folder.
func (s *Service) mainDoc(b pgdb.Bundle, files []source.File) (source.MainDoc, error) {
	var ref localRef
	var gh githubRef
	switch b.SourceKind {
	case KindLocal:
		_ = json.Unmarshal(b.SourceRef, &ref)
	case KindGitHub:
		_ = json.Unmarshal(b.SourceRef, &gh)
		ref = localRef{Dir: gh.Dir, File: gh.File}
	}
	if ref.File == "" {
		return source.FindMainDoc(files)
	}
	for _, f := range files {
		if f.Path == ref.File {
			mapped, _ := s.repo.MappedProfile(path.Join(ref.Dir, ref.File))
			if b.SourceKind == KindGitHub {
				mapped = gh.Profile
			}
			return source.SingleFileMainDoc(f.Path, f.Content, mapped)
		}
	}
	return source.MainDoc{}, &source.MainDocError{Message: "no main doc"}
}

// createLocal writes files to the new folder name under the served folder, and returns the
// bundle that the sync finds there.
func (s *Service) createLocal(ctx context.Context, name string, files []source.File) (pgdb.Bundle, error) {
	s.mu.Lock()
	dir, err := s.Local.CreateBundle(name, files)
	if err == nil {
		err = s.syncLocked(ctx)
	}
	s.mu.Unlock()
	if err == nil {
		err = s.afterChange(ctx)
	}
	if err != nil {
		if _, ok := kernel.AsError(err); ok {
			return pgdb.Bundle{}, err
		}
		return pgdb.Bundle{}, kernel.Invalid("create_failed", "%s", sentence(err.Error()))
	}
	b, err := s.DB.Queries().GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: dir})
	if err != nil {
		return pgdb.Bundle{}, fmt.Errorf("find the new bundle %s: %w", dir, err)
	}
	return b, nil
}

// CreateDB creates a bundle in the db source from files.
func (s *Service) CreateDB(ctx context.Context, slug string, files []source.File, by string) (pgdb.Bundle, error) {
	main, err := source.FindMainDoc(files)
	if err != nil {
		return pgdb.Bundle{}, kernel.Invalid("no_main_doc", "The files have %s. A bundle needs exactly one markdown file with a type field in its frontmatter.", err.Error())
	}
	if err := source.CheckLimits(files, s.limits(ctx)); err != nil {
		return pgdb.Bundle{}, err
	}
	now := time.Now().UTC()
	b := pgdb.Bundle{
		ID: kernel.NewID(), WorkspaceID: s.Workspace, Slug: slug, Title: s.title(main, slug),
		ProfileKey: main.Frontmatter.Type, MainDoc: main.Path, SourceKind: KindDB, SourceRef: dbtype.JSON(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if _, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: slug}); err == nil {
			return kernel.Conflict("slug_taken", "A bundle named %q already exists. Choose another name.", slug)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := q.InsertBundle(ctx, insertParams(b)); err != nil {
			return err
		}
		// SDD §3: a member who creates a bundle becomes its author.
		if by != LocalUser {
			if err := q.InsertBundleAuthor(ctx, pgdb.InsertBundleAuthorParams{BundleID: b.ID, UserID: by}); err != nil {
				return err
			}
		}
		_, _, err := version.Record(ctx, tx, version.Change{
			Bundle: b, Files: files, Title: b.Title, Profile: b.ProfileKey, MainDoc: b.MainDoc, CreatedBy: by, Message: "Imported",
		})
		return err
	})
	if err != nil {
		return pgdb.Bundle{}, err
	}
	if err := s.afterChange(ctx); err != nil {
		return pgdb.Bundle{}, err
	}
	return version.Bundle(ctx, s.DB.Queries(), s.Workspace, b.ID)
}

func (s *Service) title(m source.MainDoc, slug string) string {
	if m.Title != "" {
		return m.Title
	}
	if slug == "." && s.Local != nil {
		return filepath.Base(s.Local.Dir())
	}
	return path.Base(slug)
}

func insertParams(b pgdb.Bundle) pgdb.InsertBundleParams {
	return pgdb.InsertBundleParams{
		ID: b.ID, WorkspaceID: b.WorkspaceID, Slug: b.Slug, Title: b.Title, ProfileKey: b.ProfileKey, MainDoc: b.MainDoc,
		SourceKind: b.SourceKind, SourceRef: b.SourceRef, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
}

func applyError(err error) error {
	if _, ok := kernel.AsError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, source.ErrNotFound):
		return &kernel.Error{Status: 404, Code: "file_not_found", Detail: sentence(err.Error()), Err: err}
	case errors.Is(err, source.ErrExists):
		return &kernel.Error{Status: 409, Code: "file_exists", Detail: sentence(err.Error()), Err: err}
	}
	return &kernel.Error{Status: 400, Code: "invalid_change", Detail: sentence(err.Error()), Err: err}
}

// sentence starts msg with a capital letter and ends it with a full stop.
func sentence(msg string) string {
	if msg == "" {
		return msg
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

func (s *Service) limits(ctx context.Context) source.Limits {
	if s.Limits == nil {
		return source.DefaultLimits
	}
	return s.Limits(ctx)
}
