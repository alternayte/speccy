package review

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// upstream is what a fix for a missing upstream link offers: the docs the link may name, the
// link kind, and where each doc sits.
type upstream struct {
	kind   string
	docs   []pgdb.SpecDoc
	place  docPlace
	source uuid.UUID // set when b is a doc of a repo source: the link stays in Speccy
}

// upstreamChoices returns the live spec docs that b's missing upstream link may name. A doc of
// a repo source may name only docs of the same source, because an adopted link resolves there.
func (s *Service) upstreamChoices(ctx context.Context, b pgdb.SpecDoc) (upstream, error) {
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		return upstream{}, kernel.Invalid("no_profile", "%s", s.noProfile(b.ProfileKey))
	}
	up := p.Profile.Links.Upstream
	if up == nil || len(up.Kinds) == 0 {
		return upstream{}, kernel.Invalid("no_upstream", "The %s profile names no upstream link.", b.ProfileKey)
	}
	all, err := s.allBundles(ctx)
	if err != nil {
		return upstream{}, err
	}
	place, err := s.places(ctx)
	if err != nil {
		return upstream{}, err
	}
	var ref bundleRef
	_ = json.Unmarshal(b.SourceRef, &ref)
	out := upstream{kind: up.Kinds[0], place: place, source: ref.Source}
	for _, d := range all {
		if d.ID == b.ID || d.ArchivedAt.Valid || (len(up.Types) > 0 && !slices.Contains(up.Types, d.ProfileKey)) {
			continue
		}
		if out.source != uuid.Nil {
			var r bundleRef
			if json.Unmarshal(d.SourceRef, &r) != nil || r.Source != out.source {
				continue
			}
		}
		out.docs = append(out.docs, d)
	}
	// The docs in the same bundle come first: a PRD beside the SDD is the likely one.
	sort.SliceStable(out.docs, func(i, j int) bool {
		si, sj := out.docs[i].BundleID == b.BundleID, out.docs[j].BundleID == b.BundleID
		if si != sj {
			return si
		}
		return mainDocPath(out.docs[i], place) < mainDocPath(out.docs[j], place)
	})
	if len(out.docs) == 0 {
		types := strings.ToUpper(strings.Join(up.Types, " or "))
		if out.source != uuid.Nil {
			return out, kernel.Invalid("no_upstream", "No %s in this source can take the link. Add the %s to the repo, or add a standalone: entry with the reason in the sidecar.", types, types)
		}
		return out, kernel.Invalid("no_upstream", "No %s in the workspace can take the link. Add the %s first, or add a standalone: entry with the reason in the sidecar.", types, types)
	}
	return out, nil
}

// suggestLink offers the docs a missing upstream link may name. Speccy writes the link itself
// when the person picks one, so the model never guesses the link format (#75).
func (a *API) suggestLink(ctx context.Context, b pgdb.SpecDoc, f pgdb.Finding, cur *version.Current) (api.SuggestFixResponseObject, error) {
	up, err := a.Service.upstreamChoices(ctx, b)
	if err != nil {
		return nil, err
	}
	if up.source == uuid.Nil {
		if problem, _ := source.FrontmatterProblem(cur.File(b.DocPath)); problem != "" {
			return nil, kernel.Invalid("frontmatter_unread", "%s Fix the frontmatter first, then add the link.", problem)
		}
	}
	names := map[string]bool{}
	choices := make([]api.LinkChoice, 0, len(up.docs))
	for _, d := range up.docs {
		names[strings.ToUpper(d.ProfileKey)] = true
		choices = append(choices, api.LinkChoice{DocId: d.ID, Title: d.Title, Path: mainDocPath(d, up.place), Profile: d.ProfileKey})
	}
	var kinds []string
	for k := range names {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	explanation := fmt.Sprintf("Pick the %s that this %s %s. Speccy adds the link to the frontmatter.", strings.Join(kinds, " or "), strings.ToUpper(b.ProfileKey), up.kind)
	if up.source != uuid.Nil {
		explanation = fmt.Sprintf("Pick the %s that this %s %s. Speccy keeps the link, and the repo takes no commit.", strings.Join(kinds, " or "), strings.ToUpper(b.ProfileKey), up.kind)
	}
	return api.SuggestFix200JSONResponse{FindingId: f.ID, File: b.DocPath, Explanation: explanation, VersionId: cur.ID, LinkChoices: &choices}, nil
}

// acceptLink writes the picked upstream link. A doc Speccy owns takes it in its frontmatter, in
// the form the block has. A doc of a repo source keeps it as an adopted link, and the repo takes
// no commit.
func (a *API) acceptLink(ctx context.Context, b pgdb.SpecDoc, f pgdb.Finding, to uuid.UUID) (api.AcceptFixResponseObject, error) {
	up, err := a.Service.upstreamChoices(ctx, b)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(up.docs, func(d pgdb.SpecDoc) bool { return d.ID == to })
	if i < 0 {
		return nil, kernel.Invalid("bad_link", "That doc cannot take this link. Ask for the fix again.")
	}
	target := up.docs[i]
	cur, err := version.LoadCurrent(ctx, a.DB.Queries(), b)
	if err != nil {
		return nil, err
	}
	before, err := version.Files(ctx, a.DB.Queries(), cur.ID)
	if err != nil {
		return nil, err
	}
	an, was, err := a.lintBefore(ctx, b, f, cur, before)
	if err != nil {
		return nil, err
	}
	if up.source != uuid.Nil {
		if err := a.DB.Queries().SetAdoptedLink(ctx, pgdb.SetAdoptedLinkParams{SourceID: up.source,
			Path: mainDocPath(b, up.place), Kind: up.kind, Target: mainDocPath(target, up.place)}); err != nil {
			return nil, err
		}
		// The link changes what the lint reads, so the current version takes a new lint run.
		a.Service.mu.Lock()
		_, err := a.Service.Lint(ctx, b, cur.ID)
		a.Service.mu.Unlock()
		if err != nil {
			return nil, err
		}
		out := api.AcceptFix200JSONResponse{Changed: false}
		out.Result, out.Message, err = a.fixResult(ctx, b, f, an, was, before)
		return out, err
	}
	if a.Change == nil {
		return nil, kernel.Invalid("not_supported", "This server cannot change bundle files.")
	}
	rel, err := filepath.Rel(folderOf(b, up.place), mainDocPath(target, up.place))
	if err != nil {
		return nil, kernel.Invalid("bad_link", "%s is not a path Speccy can link to.", mainDocPath(target, up.place))
	}
	next, err := source.AddLink(cur.File(b.DocPath), up.kind, filepath.ToSlash(rel))
	if err != nil {
		return nil, kernel.Invalid("frontmatter_unread", "%s: %s. Fix the frontmatter first, then add the link.", b.DocPath, err.Error())
	}
	by := kernel.ActorFrom(ctx).UserID
	v, changed, err := a.Change(ctx, b.ID, cur.ID, source.Op{Kind: source.OpWrite, Path: b.DocPath, Content: next}, by,
		fmt.Sprintf("Linked to %s for %s", path.Base(target.DocPath), f.CheckSlug))
	if err != nil {
		return nil, err
	}
	ver := version.ToAPI(v)
	out := api.AcceptFix200JSONResponse{Version: &ver, Changed: changed}
	out.Result, out.Message, err = a.fixResult(ctx, b, f, an, was, withFile(before, b.DocPath, next))
	return out, err
}
