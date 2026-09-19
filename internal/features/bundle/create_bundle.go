package bundle

import (
	"bytes"
	"context"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

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
// and the title in the frontmatter, and the title in the first level-1 heading.
func fromTemplate(p profile.Versioned, title string) []byte {
	doc := profile.StripMarks(p.TemplateText)
	fmRaw, bodyStart := section.SplitFrontmatter(doc)
	body := doc[bodyStart:]

	var fm yaml.Node
	if fmRaw != nil && yaml.Unmarshal(fmRaw, &fm) == nil && len(fm.Content) == 1 && fm.Content[0].Kind == yaml.MappingNode {
		setKey(fm.Content[0], "title", title)
		setKey(fm.Content[0], "type", p.Profile.Key)
	} else {
		fm = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		setKey(fm.Content[0], "type", p.Profile.Key)
		setKey(fm.Content[0], "title", title)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(&fm)
	_ = enc.Close()
	out := buf.Bytes()

	for _, sec := range section.Parse(body).Sections {
		if sec.Level == 1 {
			rest := body[sec.BodyStart:]
			body = append(append(append([]byte{}, body[:sec.Start]...), "# "+title+"\n"...), rest...)
			break
		}
	}
	return append(append([]byte("---\n"), out...), append([]byte("---\n"), body...)...)
}

// setKey sets key to value in a YAML mapping, keeping its place when it exists.
func setKey(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: value}
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
}
