package bundle

import (
	"context"
	"encoding/json"
	"path"
	"sort"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
)

// DocChoice is one doc and the profile a person gave it. An empty profile is not a spec.
type DocChoice struct {
	Path    string
	Profile string
}

// SuggestedLink is a link Speccy offers between two docs, for a person to confirm.
type SuggestedLink struct {
	From, To, Kind string
}

// SuggestLinks offers a link for each doc in docs whose profile names an upstream type, when
// exactly one other doc in its folder has that type. existing are docs already in the
// workspace: they can be a target, and they never get a link of their own here. Speccy never
// makes a link from a suggestion by itself.
func SuggestLinks(profiles map[string]profile.Versioned, docs, existing []DocChoice) []SuggestedLink {
	all := append(append([]DocChoice(nil), docs...), existing...)
	var out []SuggestedLink
	for _, d := range docs {
		p, ok := profiles[d.Profile]
		if !ok || p.Profile.Links.Upstream == nil || len(p.Profile.Links.Upstream.Types) == 0 {
			continue
		}
		up := p.Profile.Links.Upstream
		var match []string
		seen := map[string]bool{}
		for _, o := range all {
			if o.Path == d.Path || seen[o.Path] || path.Dir(o.Path) != path.Dir(d.Path) {
				continue
			}
			for _, t := range up.Types {
				if o.Profile == t {
					match = append(match, o.Path)
					seen[o.Path] = true
				}
			}
		}
		if len(match) != 1 {
			continue // no upstream doc, or more than one: the person links it by hand
		}
		kind := "implements"
		if len(up.Kinds) > 0 {
			kind = up.Kinds[0]
		}
		out = append(out, SuggestedLink{From: d.Path, To: match[0], Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

// SuggestLinks returns the links Speccy offers for a set of docs.
func (a *API) SuggestLinks(ctx context.Context, req api.SuggestLinksRequestObject) (api.SuggestLinksResponseObject, error) {
	var docs []DocChoice
	for _, d := range req.Body.Docs {
		docs = append(docs, DocChoice{Path: d.Path, Profile: d.Profile})
	}
	var existing []DocChoice
	if req.Body.SourceId != nil || (req.Body.Local != nil && *req.Body.Local) {
		bundles, err := a.Service.DB.Queries().ListBundles(ctx, pgdb.ListBundlesParams{WorkspaceID: a.Service.Workspace, PageSize: 10000})
		if err != nil {
			return nil, err
		}
		for _, b := range bundles {
			if b.ArchivedAt.Valid {
				continue
			}
			var ref githubRef
			_ = json.Unmarshal(b.SourceRef, &ref)
			switch {
			case req.Body.SourceId != nil && (b.SourceKind != KindGitHub || ref.Source != *req.Body.SourceId):
				continue
			case req.Body.SourceId == nil && b.SourceKind != KindLocal:
				continue
			}
			existing = append(existing, DocChoice{Path: path.Join(ref.Dir, b.MainDoc), Profile: b.ProfileKey})
		}
	}
	out := api.SuggestLinks200JSONResponse{Items: []api.SuggestedLink{}}
	for _, l := range SuggestLinks(a.Profiles(), docs, existing) {
		out.Items = append(out.Items, api.SuggestedLink{From: l.From, To: l.To, Kind: l.Kind})
	}
	return out, nil
}

// checkLinkKind reports whether kind can link two bundles.
func checkLinkKind(kind string) bool {
	for _, k := range source.BundleLinkKinds {
		if k == kind {
			return true
		}
	}
	return false
}
