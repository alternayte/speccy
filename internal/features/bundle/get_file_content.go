package bundle

import (
	"bytes"
	"context"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// GetFileContent returns the bytes of one file in a version.
func (a *API) GetFileContent(ctx context.Context, req api.GetFileContentRequestObject) (api.GetFileContentResponseObject, error) {
	q := a.Service.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Service.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	v, err := version.Get(ctx, q, b, req.Params.Version)
	if err != nil {
		return nil, err
	}
	p, err := source.CleanPath(req.Params.Path)
	if err != nil {
		return nil, kernel.Invalid("bad_path", "%s.", err.Error())
	}
	rows, err := q.ListVersionFiles(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Path != p {
			continue
		}
		content, err := q.GetBlob(ctx, r.Sha256)
		if err != nil {
			return nil, err
		}
		return rawFile{name: p, content: content, etag: r.Sha256}, nil
	}
	return nil, kernel.NotFound("file_not_found", "Version %d of the bundle has no file %s.", v.Number, p)
}

// rawFile serves file bytes with a content type from the file extension. The doc text is
// untrusted (SDD §14.3): the sandbox policy stops an HTML or SVG file from running script.
type rawFile struct {
	name    string
	content []byte
	etag    string
}

func (r rawFile) VisitGetFileContentResponse(w http.ResponseWriter) error {
	ct := contentType(r.name)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(r.content)))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+r.etag+`"`)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, err := bytes.NewReader(r.content).WriteTo(w)
	return err
}

func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".yaml", ".yml":
		return "text/yaml; charset=utf-8"
	case ".sql", ".txt", ".csv", ".mmd":
		return "text/plain; charset=utf-8"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
