package bundle

import (
	"context"
	"encoding/json"
	"path"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// RepoDocPath is the path of a bundle's main doc in the repo, for a local or GitHub bundle.
// A db bundle has no repo, so its doc path is its path in the bundle.
func (s *Service) RepoDocPath(b pgdb.SpecDoc) string {
	switch b.SourceKind {
	case KindLocal:
		var ref localRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		return path.Join(ref.Dir, b.DocPath)
	case KindGitHub:
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		return path.Join(ref.Dir, b.DocPath)
	default:
		return b.DocPath
	}
}

// Decisions reads the sidecar of a bundle's main doc: its approved waivers and
// acknowledgements (DEC-009, SDD §9.3). A missing sidecar is no decisions.
//
// A local bundle reads the sidecar at the root of the served folder, where git sees it. A db
// or GitHub bundle reads it from the bundle's own files, so it travels in the version, and a
// publish puts it at the root of the repo.
func (s *Service) Decisions(ctx context.Context, b pgdb.SpecDoc) (source.Decisions, error) {
	var src []byte
	if b.SourceKind == KindLocal {
		root := s.Local
		if root == nil {
			return source.Decisions{}, nil
		}
		content, err := root.ReadRepoFile(source.SidecarPath(s.RepoDocPath(b)))
		if err != nil {
			return source.Decisions{}, err
		}
		src = content
	} else {
		if !b.CurrentVersionID.Valid {
			return source.Decisions{}, nil
		}
		files, err := version.Files(ctx, s.DB.Queries(), b.CurrentVersionID.UUID)
		if err != nil {
			return source.Decisions{}, err
		}
		src = FileContent(files, source.SidecarPath(b.DocPath))
	}
	if len(src) == 0 {
		return source.Decisions{}, nil
	}
	d, err := source.ParseDecisions(src)
	if err != nil {
		return source.Decisions{}, kernel.Invalid("sidecar_invalid", "%s: %s.", source.SidecarPath(s.RepoDocPath(b)), err.Error())
	}
	return d, nil
}

// FileContent returns the content of the file at p, or nil.
func FileContent(files []source.File, p string) []byte {
	for _, f := range files {
		if f.Path == p {
			return f.Content
		}
	}
	return nil
}

// SetDecisions writes the sidecar of a bundle's main doc. The doc text does not change, so a
// local write makes no version, and a db or GitHub write records one (DEC-009).
func (s *Service) SetDecisions(ctx context.Context, b pgdb.SpecDoc, d source.Decisions, by, message string) error {
	content, err := d.Marshal()
	if err != nil {
		return err
	}
	if b.SourceKind == KindLocal {
		root := s.Local
		if root == nil {
			return kernel.Invalid("source_read_only", "This folder cannot be changed here.")
		}
		if err := root.WriteRepoFile(source.SidecarPath(s.RepoDocPath(b)), content); err != nil {
			return kernel.Invalid("sidecar_write_failed", "The sidecar was not written: %s.", err.Error())
		}
		return s.afterChange(ctx)
	}
	op := source.Op{Kind: source.OpWrite, Path: source.SidecarPath(b.DocPath), Content: content}
	if len(content) == 0 {
		op = source.Op{Kind: source.OpDelete, Path: source.SidecarPath(b.DocPath)}
	}
	var base uuid.UUID
	if b.CurrentVersionID.Valid {
		base = b.CurrentVersionID.UUID
	}
	_, _, err = s.Change(ctx, b.ID, base, op, by, message)
	return err
}
