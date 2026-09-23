package bundle

import (
	"context"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// CreateBundle creates a bundle whose main doc is the profile's template (REQ-016).
func (a *API) CreateBundle(ctx context.Context, req api.CreateBundleRequestObject) (api.CreateBundleResponseObject, error) {
	profiles := a.Profiles()
	p, ok := profiles[req.Body.Profile]
	if !ok {
		keys := make([]string, 0, len(profiles))
		for k := range profiles {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, kernel.Invalid("unknown_profile", "No profile has the key %q. Choose one of: %s.", req.Body.Profile, strings.Join(keys, ", "))
	}
	name := slugify(req.Body.Name)
	if name == "" {
		return nil, kernel.Invalid("no_name", "Give the bundle a name with letters or digits.")
	}
	title := ""
	if req.Body.Title != nil {
		title = strings.TrimSpace(*req.Body.Title)
	}
	if title == "" {
		title = strings.TrimSpace(req.Body.Name)
	}
	doc := fromTemplate(p, title)
	files := []source.File{{Path: "SPEC.md", Content: doc}}

	s := a.Service
	if s.Local == nil {
		b, err := s.CreateDB(ctx, name, files, a.user(ctx))
		if err != nil {
			return nil, err
		}
		out, err := toAPI(ctx, s.DB.Queries(), b)
		if err != nil {
			return nil, err
		}
		return api.CreateBundle201JSONResponse(out), nil
	}
	b, err := s.createLocal(ctx, name, files)
	if err != nil {
		return nil, err
	}
	out, err := toAPI(ctx, s.DB.Queries(), b)
	if err != nil {
		return nil, err
	}
	return api.CreateBundle201JSONResponse(out), nil
}

// fromTemplate returns the template without its required markers, with the profile's type
// and the title in the frontmatter, and the title in the first level-1 heading. The block keeps
// the template's form, so a template that hides it in an HTML comment makes docs that do (#76).
func fromTemplate(p profile.Versioned, title string) []byte {
	doc := profile.StripMarks(p.TemplateText)
	keys := [][2]string{{"type", p.Profile.Key}, {"title", title}}
	out, err := source.SetKeys(doc, keys)
	if err != nil {
		// A template block that does not parse gives way to a new one.
		_, bodyStart := section.SplitFrontmatter(doc)
		out, _ = source.SetKeys(doc[bodyStart:], keys)
	}
	_, bodyStart := section.SplitFrontmatter(out)
	body := out[bodyStart:]
	for _, sec := range section.Parse(body).Sections {
		if sec.Level == 1 {
			rest := body[sec.BodyStart:]
			body = append(append(append([]byte{}, body[:sec.Start]...), "# "+title+"\n"...), rest...)
			break
		}
	}
	return append(append([]byte{}, out[:bodyStart]...), body...)
}
