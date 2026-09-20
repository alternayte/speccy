package review

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// Link origins.
const (
	originFrontmatter = "frontmatter"
	originRule        = "rule"
)

// link is one link of a bundle's version (REQ-050, REQ-132). target is nil for an external
// target and for a bundle target that no bundle matches.
type link struct {
	kind       string
	targetKind string // bundle | external
	ref        string // as written: a slug, a path, or a URL
	origin     string
	target     *pgdb.Bundle
}

// linked is a link to a bundle, with the target's current version loaded.
type linked struct {
	link
	version uuid.UUID
	files   []source.File
	main    []byte
	doc     section.Doc
}

// bundleRef is a local bundle's place on disk (the bundle feature writes it).
type bundleRef struct {
	Dir  string `json:"dir"`
	File string `json:"file,omitempty"`
}

// bundlePath is what link rules and relative targets match: a single-file bundle's file, or
// a folder bundle's folder, relative to the root. A bundle with no place on disk uses its slug.
func bundlePath(b pgdb.Bundle) string {
	var r bundleRef
	if json.Unmarshal(b.SourceRef, &r) != nil || r.Dir == "" {
		return b.Slug
	}
	if r.File != "" {
		return path.Join(r.Dir, r.File)
	}
	return r.Dir
}

// mainDocPath is the main doc's path relative to the root.
func mainDocPath(b pgdb.Bundle) string {
	var r bundleRef
	if json.Unmarshal(b.SourceRef, &r) != nil || r.Dir == "" {
		return path.Join(b.Slug, b.MainDoc)
	}
	return path.Join(r.Dir, b.MainDoc)
}

func (s *Service) allBundles(ctx context.Context) ([]pgdb.Bundle, error) {
	var out []pgdb.Bundle
	after := ""
	for {
		page, err := s.DB.Queries().ListBundles(ctx, pgdb.ListBundlesParams{WorkspaceID: s.Workspace, AfterSlug: after, PageSize: 100})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 100 {
			return out, nil
		}
		after = page[len(page)-1].Slug
	}
}

// resolveLinks returns the links of b whose main doc is main.
func (s *Service) resolveLinks(ctx context.Context, b pgdb.Bundle, main []byte) ([]link, error) {
	all, err := s.allBundles(ctx)
	if err != nil {
		return nil, err
	}
	return resolveLinksIn(all, b, main, s.linkRules()), nil
}

// linkRules returns the valid link rules of .speccy.yaml. The scan reports a bad rule.
func (s *Service) linkRules() []source.LinkRule {
	if s.Repo == nil {
		return nil
	}
	var out []source.LinkRule
	for _, raw := range s.Repo().LinkRules {
		if r, err := source.ParseLinkRule(raw); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// resolveLinksIn resolves b's links against all bundles: frontmatter links first, then link
// rules. A rule creates a link only when its target bundle exists (REQ-132). A link to the
// same bundle with the same kind appears once.
func resolveLinksIn(all []pgdb.Bundle, b pgdb.Bundle, main []byte, rules []source.LinkRule) []link {
	var out []link
	seen := map[string]bool{}
	add := func(l link) {
		key := l.kind + "|" + l.ref
		if l.target != nil {
			key = l.kind + "|" + l.target.ID.String()
			if l.target.ID == b.ID {
				return
			}
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, l)
		}
	}
	fm, _, _ := source.ReadFrontmatter(main)
	for _, fl := range fm.Links {
		target := strings.TrimSpace(fl.Target)
		if target == "" || !slices.Contains(source.LinkKinds, fl.Kind) {
			continue
		}
		l := link{kind: fl.Kind, targetKind: "bundle", ref: target, origin: originFrontmatter}
		if strings.Contains(target, "://") {
			l.targetKind = "external" // DEC-021: stored, not read in 0.1.0
		} else {
			l.target = findTarget(all, b, target)
		}
		add(l)
	}
	from := bundlePath(b)
	for _, rule := range rules {
		to, ok := rule.Target(from)
		if !ok {
			continue
		}
		for i := range all {
			if bundlePath(all[i]) == to && !all[i].ArchivedAt.Valid {
				add(link{kind: rule.Kind, targetKind: "bundle", ref: to, origin: originRule, target: &all[i]})
				break
			}
		}
	}
	return out
}

// findTarget resolves a frontmatter target (SDD §10.2): a bundle slug, or in local mode a
// path relative to the main doc's folder, to a bundle folder or a main doc file.
func findTarget(all []pgdb.Bundle, from pgdb.Bundle, target string) *pgdb.Bundle {
	live := func(yield func(*pgdb.Bundle) bool) {
		for i := range all {
			if !all[i].ArchivedAt.Valid && !yield(&all[i]) {
				return
			}
		}
	}
	for b := range live {
		if b.Slug == target {
			return b
		}
	}
	rel := path.Clean(path.Join(path.Dir(mainDocPath(from)), target))
	for b := range live {
		if bundlePath(*b) == rel || mainDocPath(*b) == rel || b.Slug == strings.TrimSuffix(rel, path.Ext(rel)) {
			return b
		}
	}
	return nil
}

// loadLinked loads the current version of each link target.
func (s *Service) loadLinked(ctx context.Context, links []link) ([]linked, error) {
	var out []linked
	for _, l := range links {
		if l.target == nil || !l.target.CurrentVersionID.Valid {
			continue
		}
		files, err := version.Files(ctx, s.DB.Queries(), l.target.CurrentVersionID.UUID)
		if err != nil {
			return nil, err
		}
		ld := linked{link: l, version: l.target.CurrentVersionID.UUID, files: files}
		for _, f := range files {
			if f.Path == l.target.MainDoc {
				ld.main = f.Content
			}
		}
		ld.doc = section.Parse(ld.main)
		out = append(out, ld)
	}
	return out, nil
}

// storeLinks replaces the stored links of b with links (REQ-050). Only the current version's
// links are stored.
func storeLinks(ctx context.Context, q store.Querier, workspace uuid.UUID, b pgdb.Bundle, links []link) error {
	if err := q.DeleteLinksFrom(ctx, b.ID); err != nil {
		return err
	}
	for _, l := range links {
		p := pgdb.InsertLinkParams{ID: kernel.NewID(), WorkspaceID: workspace, FromBundleID: b.ID, Kind: l.kind,
			TargetKind: l.targetKind, TargetRef: l.ref, Origin: l.origin}
		if l.target != nil {
			p.TargetBundleID = uuid.NullUUID{UUID: l.target.ID, Valid: true}
		}
		if err := q.InsertLink(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// Linked is a bundle that b links to, with the kind of the link.
type Linked struct {
	Kind   string
	Bundle pgdb.Bundle
}

// LinkedBundles returns the bundles that b links to, in the order the links appear. It serves
// the build packet, which carries the main doc of each linked bundle (REQ-136).
func (s *Service) LinkedBundles(ctx context.Context, b pgdb.Bundle, main []byte) ([]Linked, error) {
	links, err := s.resolveLinks(ctx, b, main)
	if err != nil {
		return nil, err
	}
	var out []Linked
	for _, l := range links {
		if l.target != nil {
			out = append(out, Linked{Kind: l.kind, Bundle: *l.target})
		}
	}
	return out, nil
}
