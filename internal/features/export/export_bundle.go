// Package export writes bundles out of Speccy (REQ-008): a .zip of a version, and a
// self-contained HTML report with the verdict.
package export

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the export endpoints.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	// Reviews lists a run's findings for the HTML report, with anchors in the current version.
	Reviews *review.API
}

// ExportBundle returns a version of a bundle as a .zip file. The files are inside one folder
// named after the bundle, so the archive unpacks to a bundle folder.
func (a *API) ExportBundle(ctx context.Context, req api.ExportBundleRequestObject) (api.ExportBundleResponseObject, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	if req.Params.Format != nil && *req.Params.Format == api.Html {
		return a.report(ctx, b)
	}
	v, err := version.Get(ctx, q, b, req.Params.Version)
	if err != nil {
		return nil, err
	}
	files, err := version.Files(ctx, q, v.ID)
	if err != nil {
		return nil, err
	}
	folder := path.Base(b.Slug)
	if folder == "." || folder == "/" {
		folder = "bundle"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: folder + "/" + f.Path, Method: zip.Deflate, Modified: v.CreatedAt.In(time.UTC)})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zipFile{name: fmt.Sprintf("%s-v%d.zip", folder, v.Number), data: buf.Bytes()}, nil
}

type zipFile struct {
	name string
	data []byte
}

func (z zipFile) VisitExportBundleResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.Itoa(len(z.data)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", z.name))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(z.data)
	return err
}
