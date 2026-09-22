package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// DeleteProfile removes a profile. A built-in ships in the binary, and a profile a bundle names
// still has to answer for that bundle's rubric, template and verdict rule.
func (a *API) DeleteProfile(ctx context.Context, req api.DeleteProfileRequestObject) (api.DeleteProfileResponseObject, error) {
	r := a.Registry
	if r == nil {
		return nil, errors.New("no profile registry")
	}
	if builtinKey(req.Key) {
		return nil, kernel.Invalid("builtin_profile",
			"%s ships in the binary, so it cannot be deleted. An edit of it is a new version of it.", req.Key)
	}
	q := r.DB.Queries()
	rows, err := q.ListBundlesUsingProfile(ctx, pgdb.ListBundlesUsingProfileParams{WorkspaceID: r.Workspace, ProfileKey: req.Key})
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		n, err := q.CountBundlesUsingProfile(ctx, pgdb.CountBundlesUsingProfileParams{WorkspaceID: r.Workspace, ProfileKey: req.Key})
		if err != nil {
			return nil, err
		}
		slugs := make([]string, 0, len(rows))
		for _, b := range rows {
			slugs = append(slugs, b.Slug)
		}
		names := "1 bundle names"
		if n != 1 {
			names = fmt.Sprintf("%d bundles name", n)
		}
		return nil, kernel.Conflict("profile_in_use",
			"%s %s: %s. Give them another doc type first.", names, req.Key, strings.Join(slugs, ", "))
	}
	row, err := q.GetProfileByKey(ctx, pgdb.GetProfileByKeyParams{WorkspaceID: r.Workspace, Key: req.Key})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("profile_not_found", "There is no doc type %q.", req.Key)
	}
	if err != nil {
		return nil, err
	}
	// An old review run keeps the profile version it used, so the version rows stay until the
	// profile itself goes. They go with it here.
	if err := q.DeleteProfileMaintainersOf(ctx, row.ID); err != nil {
		return nil, err
	}
	if err := q.DeleteProfileVersionsOf(ctx, row.ID); err != nil {
		return nil, err
	}
	if err := q.DeleteProfileRow(ctx, pgdb.DeleteProfileRowParams{WorkspaceID: r.Workspace, ID: row.ID}); err != nil {
		return nil, err
	}
	if err := r.removeFiles(req.Key); err != nil {
		return nil, err
	}
	if err := r.Reload(ctx); err != nil {
		return nil, err
	}
	return api.DeleteProfile204Response{}, nil
}

// DiffProfileVersions returns the YAML diff and the template diff between two versions.
func (a *API) DiffProfileVersions(ctx context.Context, req api.DiffProfileVersionsRequestObject) (api.DiffProfileVersionsResponseObject, error) {
	from, err := a.versionText(ctx, req.Key, int64(req.Params.FromVersion))
	if err != nil {
		return nil, err
	}
	to, err := a.versionText(ctx, req.Key, int64(req.Params.ToVersion))
	if err != nil {
		return nil, err
	}
	return api.DiffProfileVersions200JSONResponse(api.ProfileDiff{
		From: req.Params.FromVersion, To: req.Params.ToVersion,
		Yaml:     version.LineDiff(from.yaml, to.yaml),
		Template: version.LineDiff(from.template, to.template),
	}), nil
}

// RollbackProfile writes a new version whose text equals an earlier one. It moves no pointer
// and reuses no number, because a review run pins the version it used (REQ-012).
func (a *API) RollbackProfile(ctx context.Context, req api.RollbackProfileRequestObject) (api.RollbackProfileResponseObject, error) {
	old, err := a.versionText(ctx, req.Key, int64(req.Body.Version))
	if err != nil {
		return nil, err
	}
	by := actorID(ctx)
	origin := fmt.Sprintf("rolled back to v%d by %s", req.Body.Version, by)
	if _, err := a.Registry.SaveAs(ctx, req.Key, []byte(old.yaml), []byte(old.template), by, origin); err != nil {
		return nil, err
	}
	out, err := a.detail(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return api.RollbackProfile200JSONResponse(out), nil
}

type profileText struct{ yaml, template string }

// versionText reads one stored version of a profile.
func (a *API) versionText(ctx context.Context, key string, v int64) (profileText, error) {
	r := a.Registry
	q := r.DB.Queries()
	row, err := q.GetProfileByKey(ctx, pgdb.GetProfileByKeyParams{WorkspaceID: r.Workspace, Key: key})
	if errors.Is(err, sql.ErrNoRows) {
		return profileText{}, kernel.NotFound("profile_not_found", "There is no doc type %q.", key)
	}
	if err != nil {
		return profileText{}, err
	}
	pv, err := q.GetProfileVersion(ctx, pgdb.GetProfileVersionParams{ProfileID: row.ID, Version: v})
	if errors.Is(err, sql.ErrNoRows) {
		return profileText{}, kernel.NotFound("version_not_found", "%s has no version %d.", key, v)
	}
	if err != nil {
		return profileText{}, err
	}
	return profileText{yaml: pv.Yaml, template: pv.Template}, nil
}

// builtinKey reports whether a profile ships in the binary.
func builtinKey(key string) bool {
	bs, err := Builtins()
	if err != nil {
		return false
	}
	for _, b := range bs {
		if b.Profile.Key == key {
			return true
		}
	}
	return false
}
