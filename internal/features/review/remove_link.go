package review

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// removable says whether Speccy can take a link of origin off b: an adopted link lives in the
// store, and a frontmatter link lives in a doc Speccy writes. A repo doc's frontmatter belongs
// to the repo, and a link rule to .speccy.yaml.
func (a *API) removable(b pgdb.SpecDoc, origin string) bool {
	switch origin {
	case originAdopted:
		return true
	case originFrontmatter:
		var r bundleRef
		_ = json.Unmarshal(b.SourceRef, &r)
		return r.Source == uuid.Nil && a.Change != nil
	}
	return false
}

// RemoveLink takes one outgoing link off the doc. An adopted link leaves the store and the
// current version takes a new lint run; a frontmatter link leaves the main doc as a new version.
func (a *API) RemoveLink(ctx context.Context, req api.RemoveLinkRequestObject) (api.RemoveLinkResponseObject, error) {
	b, err := a.bundle(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	if _, err := a.DB.Queries().GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: req.Params.BaseVersion}); err != nil {
		return nil, kernel.NotFound("version_not_found", "The bundle has no version %s.", req.Params.BaseVersion)
	}
	if !b.CurrentVersionID.Valid || b.CurrentVersionID.UUID != req.Params.BaseVersion {
		return nil, version.ErrConflict
	}
	s := a.Service
	in, err := s.load(ctx, b, req.Params.BaseVersion, s.Profiles()[b.ProfileKey])
	if err != nil {
		return nil, err
	}
	kind := string(req.Params.Kind)
	var l *link
	for i := range in.links {
		if in.links[i].kind == kind && in.links[i].ref == req.Params.Target {
			l = &in.links[i]
		}
	}
	if l == nil {
		return nil, kernel.NotFound("link_not_found", "This doc has no %s link to %q. Reload the page.", kind, req.Params.Target)
	}
	if !a.removable(b, l.origin) {
		if l.origin == originRule {
			return nil, kernel.Invalid("rule_link", "A link rule in .speccy.yaml makes this link. Change the rule to remove it.")
		}
		return nil, kernel.Invalid("repo_link", "The frontmatter of %s in the repo names this link. Remove it there.", b.DocPath)
	}
	if l.origin == originAdopted {
		var r bundleRef
		_ = json.Unmarshal(b.SourceRef, &r)
		place, err := s.places(ctx)
		if err != nil {
			return nil, err
		}
		if err := a.DB.Queries().DeleteAdoptedLink(ctx, pgdb.DeleteAdoptedLinkParams{SourceID: r.Source,
			Path: mainDocPath(b, place), Kind: kind}); err != nil {
			return nil, err
		}
		// The link changes what the lint reads, so the current version takes a new lint run.
		s.mu.Lock()
		_, err = s.Lint(ctx, b, req.Params.BaseVersion)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return api.RemoveLink200JSONResponse{}, nil
	}
	next, ok, err := source.RemoveLink(in.main, kind, req.Params.Target)
	if err != nil {
		return nil, kernel.Invalid("frontmatter_unread", "%s: %s. Fix the frontmatter first, then remove the link.", b.DocPath, err.Error())
	}
	if !ok {
		return nil, kernel.NotFound("link_not_found", "The frontmatter names no %s link to %q. Reload the page.", kind, req.Params.Target)
	}
	v, _, err := a.Change(ctx, b.ID, req.Params.BaseVersion, source.Op{Kind: source.OpWrite, Path: b.DocPath, Content: next},
		kernel.ActorFrom(ctx).UserID, fmt.Sprintf("Removed the %s link to %s", kind, req.Params.Target))
	if err != nil {
		return nil, err
	}
	ver := version.ToAPI(v)
	return api.RemoveLink200JSONResponse{Version: &ver}, nil
}
