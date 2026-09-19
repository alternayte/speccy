package http

import (
	"context"
	"net/url"
	"path"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/render"
)

// RenderMarkdown renders markdown to HTML on the server (DEC-017), with the parser that the
// review engine uses. Each block carries its source position for overlays and scroll sync.
func (Core) RenderMarkdown(_ context.Context, req api.RenderMarkdownRequestObject) (api.RenderMarkdownResponseObject, error) {
	var dir string
	if req.Body.Path != nil {
		dir = path.Dir(*req.Body.Path)
	}
	links := render.Links{Dir: dir}
	if b := req.Body.BundleId; b != nil {
		links.MarkFiles = true
		links.Image = func(p string) string {
			return "/api/v1/bundles/" + b.String() + "/files/content?path=" + url.QueryEscape(p)
		}
	}
	out, err := render.HTML([]byte(req.Body.Markdown), links)
	if err != nil {
		return nil, err
	}
	return api.RenderMarkdown200JSONResponse{Html: out}, nil
}
