// Package app builds Speccy's services and API on an open store, for local and hosted mode.
package app

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/features/admin"
	"github.com/alternayte/speccy/internal/features/bundle"
	"github.com/alternayte/speccy/internal/features/export"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/version"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store"
)

// App is the services and API of one workspace, in either mode.
type App struct {
	Workspace uuid.UUID
	Bundles   *bundle.Service
	Profiles  *profile.Registry
	Reviews   *review.Service
	Admin     *admin.API
	Share     *share.API
	API       speccyhttp.API
}

// New builds the services on an open, migrated store. root is nil in hosted mode, where
// bundles live in the db source. The review worker runs until ctx ends.
func New(ctx context.Context, db *store.DB, sealer *kernel.Sealer, root *local.Root, profilesDir string) (*App, error) {
	ws, err := db.Workspace(ctx)
	if err != nil {
		return nil, err
	}
	settings := func(ctx context.Context) admin.Settings {
		s, err := admin.LoadSettings(ctx, db.Queries(), ws)
		if err != nil {
			slog.ErrorContext(ctx, "read of the workspace settings failed; the defaults apply", "err", err)
		}
		return s
	}
	gateway := &model.Gateway{DB: db, Workspace: ws, Sealer: sealer}
	profiles := &profile.Registry{DB: db, Workspace: ws, Dir: profilesDir}
	if err := profiles.Reload(ctx); err != nil {
		return nil, err
	}
	svc := &bundle.Service{DB: db, Workspace: ws, Local: root}
	adminAPI := &admin.API{DB: db, Workspace: ws, Sealer: sealer, Gateway: gateway}
	reviews := &review.Service{
		DB: db, Workspace: ws, Profiles: profiles.Current, Repo: svc.RepoConfig,
		Gateway: gateway, Search: adminAPI.SearchSource, Progress: review.NewBroker(),
		// REQ-105: the admin sets the parallel model calls.
		Parallel: func(ctx context.Context) int { return settings(ctx).ParallelCalls },
	}
	if root == nil {
		// REQ-009: the admin sets the size limits of hosted mode.
		svc.Limits = func(ctx context.Context) source.Limits { return settings(ctx).Limits() }
	}
	// DEC-027: lint runs on every new version.
	svc.AfterChange = reviews.EnsureLinted
	if err := svc.Sync(ctx); err != nil {
		return nil, err
	}
	// SDD §7.2: one worker runs queued reviews.
	go reviews.Work(ctx)
	shareAPI := &share.API{DB: db, Workspace: ws}
	return &App{
		Workspace: ws, Bundles: svc, Profiles: profiles, Reviews: reviews, Admin: adminAPI, Share: shareAPI,
		API: speccyhttp.API{
			BundleAPI:  &bundle.API{Service: svc, Profiles: profiles.Current},
			VersionAPI: &version.API{DB: db, Workspace: ws},
			ExportAPI:  &export.API{DB: db, Workspace: ws},
			ProfileAPI: &profile.API{Registry: profiles},
			ReviewAPI:  &review.API{DB: db, Workspace: ws, Service: reviews, Change: svc.Change},
			AdminAPI:   adminAPI,
			ShareAPI:   shareAPI,
		},
	}, nil
}
