package bundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// ListSkipped returns the markdown files the local scan passed over, with the profile Speccy
// guesses for each (REQ-001). A person in the app adopts one without a terminal.
func (a *API) ListSkipped(ctx context.Context, _ api.ListSkippedRequestObject) (api.ListSkippedResponseObject, error) {
	out := api.ListSkipped200JSONResponse{Items: []api.SkippedDoc{}}
	root := a.Service.Local
	if root == nil {
		return out, nil
	}
	gone, err := a.dismissed(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range a.Service.SkippedDocs() {
		if gone[localSource][p] {
			continue // the person marked it as not a spec (REQ-133)
		}
		item := api.SkippedDoc{Path: p}
		content, err := os.ReadFile(filepath.Join(root.Dir(), filepath.FromSlash(p)))
		if err == nil {
			if key, ok := profile.Guess(a.Profiles(), content); ok {
				item.Profile = &key
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// AdoptSkipped writes the type into a skipped file. The file becomes a bundle on the next scan.
func (a *API) AdoptSkipped(ctx context.Context, req api.AdoptSkippedRequestObject) (api.AdoptSkippedResponseObject, error) {
	root := a.Service.Local
	if root == nil {
		return nil, kernel.Invalid("not_local", "Speccy adopts files on disk in local mode only.")
	}
	if _, ok := a.Profiles()[req.Body.Profile]; !ok {
		return nil, kernel.Invalid("no_profile", "There is no doc type %q.", req.Body.Profile)
	}
	want := strings.TrimPrefix(filepath.ToSlash(req.Body.Path), "./")
	found := false
	for _, p := range a.Service.SkippedDocs() {
		if p == want {
			found = true
			break
		}
	}
	if !found {
		return nil, kernel.Invalid("not_skipped", "%s is not a file the scan skipped.", req.Body.Path)
	}
	file := filepath.Join(root.Dir(), filepath.FromSlash(want))
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	// Adopting contradicts the mark, so the newer act wins (REQ-133).
	if err := a.Service.DB.Queries().DeleteDismissedDoc(ctx, pgdb.DeleteDismissedDocParams{
		WorkspaceID: a.Service.Workspace, Path: want}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, source.AddTypeLine(content, req.Body.Profile), 0o644); err != nil {
		return nil, err
	}
	if err := a.Service.Sync(ctx); err != nil {
		return nil, err
	}
	// A folder with one typed markdown file is a bundle, and its slug is the folder (REQ-001).
	q := a.Service.DB.Queries()
	slug := filepath.ToSlash(filepath.Dir(want))
	b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: a.Service.Workspace, Slug: slug})
	if err != nil {
		return nil, err
	}
	out, err := toAPI(ctx, q, b)
	if err != nil {
		return nil, err
	}
	if err := a.brief(ctx, q, b, &out, nil); err != nil {
		return nil, err
	}
	return api.AdoptSkipped201JSONResponse(out), nil
}

// GuessProfile names the profile that fits a doc, from its headings (REQ-008). The import
// dialog shows the guess before it imports, so no doc gets the wrong checks in silence.
func (a *API) GuessProfile(ctx context.Context, req api.GuessProfileRequestObject) (api.GuessProfileResponseObject, error) {
	out := api.GuessProfile200JSONResponse{}
	if key, ok := profile.Guess(a.Profiles(), []byte(req.Body.Text)); ok {
		out.Profile = &key
	}
	return out, nil
}
