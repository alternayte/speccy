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
	"sort"
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
	// GitHub returns the client for a GitHub host: the workspace token in hosted mode, and the
	// machine's gh login in local mode (REQ-129). apiURL is the source's API address.
	GitHub func(ctx context.Context, apiURL string) (*github.Client, error)

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
	found := map[uuid.UUID]bool{}
	folders := map[string]bool{}
	for _, fb := range scan.Bundles {
		folders[fb.Folder] = true
		ref, _ := json.Marshal(localRef{Dir: fb.Dir, File: fb.File})
		err := s.DB.InTx(ctx, func(tx store.Tx) error {
			folder, err := s.ensureBundle(ctx, tx.Queries(), fb.Folder, s.folderTitle(fb.Folder), KindLocal, dbtype.JSON(ref), now)
			if err != nil {
				return err
			}
			d, isNew, err := s.ensureSpecDoc(ctx, tx.Queries(), folder, fb.Slug, fb.Main, KindLocal, dbtype.JSON(ref), now)
			if err != nil {
				return err
			}
			message := "Changed on disk"
			if isNew {
				message = "Found on disk"
			}
			found[d.ID] = true
			_, _, err = version.Record(ctx, tx, version.Change{
				Bundle: d, Files: fb.Files, Title: s.title(fb.Main, fb.Slug), Profile: fb.Main.Frontmatter.Type,
				MainDoc: fb.Main.Path, CreatedBy: LocalUser, Message: message,
			})
			return err
		})
		if err != nil {
			return err
		}
	}
	q := s.DB.Queries()
	existing, err := q.ListSpecDocsBySource(ctx, pgdb.ListSpecDocsBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindLocal})
	if err != nil {
		return err
	}
	for _, d := range existing {
		if !found[d.ID] && !d.ArchivedAt.Valid {
			err := q.SetSpecDocArchived(ctx, pgdb.SetSpecDocArchivedParams{
				ID: d.ID, ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
		}
	}
	if err := s.archiveEmptyBundles(ctx, q, KindLocal, folders, now); err != nil {
		return err
	}
	s.scan = scan
	s.repo = repo
	s.problems = append(problems, scan.Problems...)
	return nil
}

// ensureBundle returns the bundle with slug, and creates it when it is new. It keeps the title
// and the source in step and un-archives the bundle.
func (s *Service) ensureBundle(ctx context.Context, q store.Querier, slug, title, kind string, ref dbtype.JSON, now time.Time) (pgdb.Bundle, error) {
	b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: slug})
	if errors.Is(err, sql.ErrNoRows) {
		b = pgdb.Bundle{ID: kernel.NewID(), WorkspaceID: s.Workspace, Slug: slug, Title: title, SourceKind: kind,
			SourceRef: ref, Visibility: "internal", CreatedAt: now, UpdatedAt: now}
		return b, q.InsertBundle(ctx, pgdb.InsertBundleParams{ID: b.ID, WorkspaceID: b.WorkspaceID, Slug: b.Slug,
			Title: b.Title, SourceKind: b.SourceKind, SourceRef: b.SourceRef, CreatedAt: now, UpdatedAt: now})
	}
	if err != nil {
		return b, err
	}
	if b.Title != title || string(b.SourceRef) != string(ref) || b.ArchivedAt.Valid {
		if err := q.UpdateBundle(ctx, pgdb.UpdateBundleParams{ID: b.ID, Title: title, SourceRef: ref, UpdatedAt: now}); err != nil {
			return b, err
		}
		b.Title, b.SourceRef, b.ArchivedAt = title, ref, sql.NullTime{}
	}
	return b, nil
}

// ensureSpecDoc returns the spec doc main in bundle folder, and creates it when it is new. A spec
// doc is found by its slug, or by its path in the bundle when its slug changed. isNew reports
// that the spec doc was created.
func (s *Service) ensureSpecDoc(ctx context.Context, q store.Querier, folder pgdb.Bundle, slug string, main source.MainDoc,
	kind string, ref dbtype.JSON, now time.Time) (d pgdb.SpecDoc, isNew bool, err error) {
	d, err = q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: slug})
	if errors.Is(err, sql.ErrNoRows) {
		d, err = q.GetSpecDocByPath(ctx, pgdb.GetSpecDocByPathParams{BundleID: folder.ID, DocPath: main.Path})
	}
	if errors.Is(err, sql.ErrNoRows) {
		d = pgdb.SpecDoc{
			ID: kernel.NewID(), WorkspaceID: s.Workspace, BundleID: folder.ID, Slug: slug, Title: s.title(main, slug),
			ProfileKey: main.Frontmatter.Type, DocPath: main.Path, SourceKind: kind, SourceRef: ref,
			CreatedAt: now, UpdatedAt: now,
		}
		return d, true, q.InsertSpecDoc(ctx, insertParams(d))
	}
	if err != nil {
		return d, false, err
	}
	if d.BundleID != folder.ID || d.Slug != slug || string(d.SourceRef) != string(ref) {
		if err := q.SetSpecDocBundle(ctx, pgdb.SetSpecDocBundleParams{ID: d.ID, BundleID: folder.ID, Slug: slug, SourceRef: ref, UpdatedAt: now}); err != nil {
			return d, false, err
		}
		d.BundleID, d.Slug, d.SourceRef = folder.ID, slug, ref
	}
	if d.ArchivedAt.Valid {
		if err := q.SetSpecDocArchived(ctx, pgdb.SetSpecDocArchivedParams{ID: d.ID, UpdatedAt: now}); err != nil {
			return d, false, err
		}
		d.ArchivedAt = sql.NullTime{}
	}
	return d, false, nil
}

// archiveEmptyBundles archives the bundles of kind that hold no live spec doc, and those whose
// slug is not in keep when keep is not nil.
func (s *Service) archiveEmptyBundles(ctx context.Context, q store.Querier, kind string, keep map[string]bool, now time.Time) error {
	bundles, err := q.ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{WorkspaceID: s.Workspace, SourceKind: kind})
	if err != nil {
		return err
	}
	for _, b := range bundles {
		if b.ArchivedAt.Valid {
			continue
		}
		docs, err := q.ListSpecDocsOfBundle(ctx, b.ID)
		if err != nil {
			return err
		}
		if len(docs) > 0 && (keep == nil || keep[b.Slug]) {
			continue
		}
		if err := q.SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{ID: b.ID, ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

// folderTitle is the title of a bundle: its folder name.
func (s *Service) folderTitle(slug string) string {
	if slug == "." && s.Local != nil {
		return filepath.Base(s.Local.Dir())
	}
	return path.Base(slug)
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
	siblings, err := s.siblings(ctx, q, b)
	if err != nil {
		return pgdb.Version{}, false, err
	}
	for _, sib := range siblings {
		if op.Path == sib.DocPath || (op.Kind == source.OpRename && op.To == sib.DocPath) {
			return pgdb.Version{}, false, kernel.Invalid("other_spec_doc", "%s is another spec doc in this bundle. Open that doc to change it.", sib.DocPath)
		}
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
		if err != nil || !changed || b.SourceKind == KindLocal || op.Path == b.DocPath {
			return err
		}
		// An asset belongs to every spec doc of the bundle, so a change to it makes a new
		// version of each. On disk the sync below does this.
		for _, sib := range siblings {
			if err := s.applyToSibling(ctx, tx, sib, op, by, message); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return v, changed, err
	}
	if changed && b.SourceKind == KindLocal {
		// The other spec docs of the folder read the changed asset from disk.
		err = s.syncLocked(ctx)
	}
	return v, changed, err
}

// siblings returns the other live spec docs of the bundle that holds d.
func (s *Service) siblings(ctx context.Context, q store.Querier, d pgdb.SpecDoc) ([]pgdb.SpecDoc, error) {
	docs, err := q.ListSpecDocsOfBundle(ctx, d.BundleID)
	if err != nil {
		return nil, err
	}
	var out []pgdb.SpecDoc
	for _, o := range docs {
		if o.ID != d.ID {
			out = append(out, o)
		}
	}
	return out, nil
}

// applyToSibling applies an asset change to another spec doc of the same bundle. A change that
// does not apply to its files (a carried file only this doc holds) leaves it as it is.
func (s *Service) applyToSibling(ctx context.Context, tx store.Tx, sib pgdb.SpecDoc, op source.Op, by, message string) error {
	if !sib.CurrentVersionID.Valid {
		return nil
	}
	q := tx.Queries()
	files, err := version.Files(ctx, q, sib.CurrentVersionID.UUID)
	if err != nil {
		return err
	}
	next, err := source.Apply(files, op)
	if err == nil {
		var main source.MainDoc
		if main, err = s.mainDoc(sib, next); err == nil {
			_, _, err = version.Record(ctx, tx, version.Change{
				Bundle: sib, Files: next, Title: s.title(main, sib.Slug), Profile: main.Frontmatter.Type,
				MainDoc: main.Path, CreatedBy: by, Message: message,
			})
			return err
		}
	}
	return nil
}

// mainDoc finds the main doc of b in files: the mapped file of a single-file bundle, or the
// REQ-001 rule for a folder.
func (s *Service) mainDoc(b pgdb.SpecDoc, files []source.File) (source.MainDoc, error) {
	var ref localRef
	var gh githubRef
	switch b.SourceKind {
	case KindLocal, KindDB:
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
func (s *Service) createLocal(ctx context.Context, name string, files []source.File) (pgdb.SpecDoc, error) {
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
			return pgdb.SpecDoc{}, err
		}
		return pgdb.SpecDoc{}, kernel.Invalid("create_failed", "%s", sentence(err.Error()))
	}
	all, err := s.bundlesUnder(ctx, dir)
	if err != nil {
		return pgdb.SpecDoc{}, err
	}
	if len(all) == 0 {
		return pgdb.SpecDoc{}, fmt.Errorf("find the new bundle %s: the scan found none", dir)
	}
	return all[0], nil
}

// bundlesUnder returns the local bundles in the folder dir, by slug: one for a folder with one
// spec doc, one for each spec doc otherwise.
func (s *Service) bundlesUnder(ctx context.Context, dir string) ([]pgdb.SpecDoc, error) {
	bundles, err := s.DB.Queries().ListSpecDocsBySource(ctx, pgdb.ListSpecDocsBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindLocal})
	if err != nil {
		return nil, err
	}
	var out []pgdb.SpecDoc
	for _, b := range bundles {
		if b.Slug == dir || strings.HasPrefix(b.Slug, dir+"/") {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// CreateDB creates a bundle in the db source from files, with the one spec doc they hold.
func (s *Service) CreateDB(ctx context.Context, slug string, files []source.File, by string) (pgdb.SpecDoc, error) {
	main, err := source.FindMainDoc(files)
	if err != nil {
		return pgdb.SpecDoc{}, kernel.Invalid("no_main_doc", "The files have %s. A bundle needs exactly one markdown file with a type field in its frontmatter.", err.Error())
	}
	docs, err := s.CreateDBBundle(ctx, slug, []NewDoc{{Slug: slug, Main: main, Files: files}}, by)
	if err != nil {
		return pgdb.SpecDoc{}, err
	}
	return docs[0], nil
}

// NewDoc is one spec doc of a new db bundle: its slug, its doc, and the files of its version.
type NewDoc struct {
	Slug  string
	Main  source.MainDoc
	Files []source.File
}

// CreateDBBundle creates the bundle slug in the db source, with one spec doc for each of docs.
func (s *Service) CreateDBBundle(ctx context.Context, slug string, docs []NewDoc, by string) ([]pgdb.SpecDoc, error) {
	for _, d := range docs {
		if err := source.CheckLimits(d.Files, s.limits(ctx)); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	var made []pgdb.SpecDoc
	err := s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if _, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: slug}); err == nil {
			return kernel.Conflict("slug_taken", "A bundle named %q already exists. Choose another name.", slug)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		folder, err := s.ensureBundle(ctx, q, slug, path.Base(slug), KindDB, dbtype.JSON(`{}`), now)
		if err != nil {
			return err
		}
		// SDD §3: a member who creates a bundle becomes its author.
		if by != LocalUser {
			if err := q.InsertBundleAuthor(ctx, pgdb.InsertBundleAuthorParams{BundleID: folder.ID, UserID: by}); err != nil {
				return err
			}
		}
		for _, nd := range docs {
			if _, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: nd.Slug}); err == nil {
				return kernel.Conflict("slug_taken", "A bundle named %q already exists. Choose another name.", nd.Slug)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			ref, _ := json.Marshal(localRef{File: nd.Main.Path})
			d := pgdb.SpecDoc{
				ID: kernel.NewID(), WorkspaceID: s.Workspace, BundleID: folder.ID, Slug: nd.Slug, Title: s.title(nd.Main, nd.Slug),
				ProfileKey: nd.Main.Frontmatter.Type, DocPath: nd.Main.Path, SourceKind: KindDB, SourceRef: dbtype.JSON(ref),
				CreatedAt: now, UpdatedAt: now,
			}
			if err := q.InsertSpecDoc(ctx, insertParams(d)); err != nil {
				return err
			}
			if _, _, err := version.Record(ctx, tx, version.Change{
				Bundle: d, Files: nd.Files, Title: d.Title, Profile: d.ProfileKey, MainDoc: d.DocPath, CreatedBy: by, Message: "Imported",
			}); err != nil {
				return err
			}
			made = append(made, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.afterChange(ctx); err != nil {
		return nil, err
	}
	for i, d := range made {
		if made[i], err = version.Bundle(ctx, s.DB.Queries(), s.Workspace, d.ID); err != nil {
			return nil, err
		}
	}
	return made, nil
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

func insertParams(b pgdb.SpecDoc) pgdb.InsertSpecDocParams {
	return pgdb.InsertSpecDocParams{
		ID: b.ID, WorkspaceID: b.WorkspaceID, BundleID: b.BundleID, Slug: b.Slug, Title: b.Title, ProfileKey: b.ProfileKey, DocPath: b.DocPath,
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
