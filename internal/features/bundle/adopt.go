package bundle

import (
	"context"
	"encoding/json"
	"path"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// adoptOf is the frontmatter keys the main doc does not name, with the values a review uses
// for them (REQ-135). It is nil when the doc names both, so the UI offers nothing.
func adoptOf(ctx context.Context, q store.Querier, b pgdb.SpecDoc, profiles map[string]profile.Versioned) *api.Adopt {
	main, err := mainDocContent(ctx, q, b)
	if err != nil {
		return nil
	}
	fm, _, err := source.ReadFrontmatter(main)
	if err != nil {
		return nil
	}
	out := api.Adopt{}
	if fm.Type == "" {
		key := b.ProfileKey
		if key == "" {
			if k, ok := profile.Guess(profiles, main); ok {
				key = k
			}
		}
		if key != "" {
			out.Type = &key
		}
	}
	if _, ok := kernel.ParseSize(fm.Size); !ok {
		size := string(review.InferSize(main, len(fm.Links)))
		out.Size = &size
	}
	if out.Type == nil && out.Size == nil {
		return nil
	}
	return &out
}

// mainDocContent reads the main doc of the bundle's current version.
func mainDocContent(ctx context.Context, q store.Querier, b pgdb.SpecDoc) ([]byte, error) {
	if !b.CurrentVersionID.Valid {
		return nil, kernel.NotFound("no_version", "The bundle has no version.")
	}
	files, err := version.Files(ctx, q, b.CurrentVersionID.UUID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Path == b.DocPath {
			return f.Content, nil
		}
	}
	return nil, kernel.NotFound("no_main_doc", "The bundle's current version has no main doc.")
}

// AdoptFrontmatter writes the type and the size that the review used into the main doc's
// frontmatter, as a new version (REQ-135). The author chooses it after a verdict, so the next
// review needs no guess.
func (a *API) AdoptFrontmatter(ctx context.Context, req api.AdoptFrontmatterRequestObject) (api.AdoptFrontmatterResponseObject, error) {
	q := a.Service.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Service.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	ad := adoptOf(ctx, q, b, a.Profiles())
	if ad == nil {
		return nil, kernel.Invalid("nothing_to_adopt", "The main doc already names its type and its size.")
	}
	main, err := mainDocContent(ctx, q, b)
	if err != nil {
		return nil, err
	}
	var keys [][2]string
	if ad.Type != nil {
		keys = append(keys, [2]string{"type", *ad.Type})
	}
	if ad.Size != nil {
		keys = append(keys, [2]string{"size", *ad.Size})
	}
	next, err := source.SetKeys(main, keys)
	if err != nil {
		return nil, kernel.Invalid("bad_frontmatter", "%s", err.Error())
	}
	v, changed, err := a.Service.Change(ctx, b.ID, b.CurrentVersionID.UUID,
		source.Op{Kind: source.OpWrite, Path: b.DocPath, Content: next},
		kernel.ActorFrom(ctx).UserID, "Wrote the type and the size into the frontmatter")
	if err != nil {
		return nil, err
	}
	return api.AdoptFrontmatter200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}

// SetBundleProfile changes the profile of the bundle's main doc. A doc Speccy owns takes the
// type in its frontmatter, as a new version. A doc in a repo source takes an adopted type, so
// the repo gets no commit; a repo doc that names its own type is changed in the repo.
func (a *API) SetBundleProfile(ctx context.Context, req api.SetBundleProfileRequestObject) (api.SetBundleProfileResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	b, err := version.Bundle(ctx, q, s.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	key := req.Body.Profile
	if _, ok := a.Profiles()[key]; !ok {
		return nil, kernel.Invalid("no_profile", "There is no doc type %q.", key)
	}
	if key == b.ProfileKey {
		return nil, kernel.Invalid("same_profile", "The doc already uses the %s profile.", key)
	}
	main, err := mainDocContent(ctx, q, b)
	if err != nil {
		return nil, err
	}
	if b.SourceKind == KindGitHub {
		fm, _, _ := source.ReadFrontmatter(main)
		if fm.Type != "" {
			return nil, kernel.Invalid("repo_names_type", "The doc names type %s in the repo, and the repo wins. Change the type field in the repo.", fm.Type)
		}
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if err := q.SetAdoptedType(ctx, pgdb.SetAdoptedTypeParams{SourceID: ref.Source, Path: path.Join(ref.Dir, b.DocPath), Profile: key}); err != nil {
			return nil, err
		}
		if err := s.SyncSource(ctx, ref.Source, true); err != nil {
			return nil, err
		}
	} else {
		next, err := source.SetKeys(main, [][2]string{{"type", key}})
		if err != nil {
			return nil, kernel.Invalid("bad_frontmatter", "%s", err.Error())
		}
		if _, _, err := s.Change(ctx, b.ID, b.CurrentVersionID.UUID,
			source.Op{Kind: source.OpWrite, Path: b.DocPath, Content: next},
			kernel.ActorFrom(ctx).UserID, "Changed the doc type to "+key); err != nil {
			return nil, err
		}
	}
	if b, err = version.Bundle(ctx, q, s.Workspace, b.ID); err != nil {
		return nil, err
	}
	out, err := toAPI(ctx, q, b)
	if err != nil {
		return nil, err
	}
	return api.SetBundleProfile200JSONResponse(out), nil
}
