// Package app builds Speccy's services and API on an open store, for local and hosted mode.
package app

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/admin"
	"github.com/alternayte/speccy/internal/features/approval"
	"github.com/alternayte/speccy/internal/features/bundle"
	"github.com/alternayte/speccy/internal/features/export"
	"github.com/alternayte/speccy/internal/features/inbox"
	"github.com/alternayte/speccy/internal/features/insights"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/thread"
	"github.com/alternayte/speccy/internal/features/tour"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/features/waiver"
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

// Options are what differs between local and hosted mode.
type Options struct {
	// Root is the served folder in local mode, and nil in hosted mode, where bundles live in the
	// db source. ProfilesDir is its .speccy/profiles.
	Root        *local.Root
	ProfilesDir string
	// People lists the members. Nil means local mode's one person.
	People kernel.Directory
}

// New builds the services on an open, migrated store. The review worker runs until ctx ends.
func New(ctx context.Context, db *store.DB, sealer *kernel.Sealer, o Options) (*App, error) {
	ws, err := db.Workspace(ctx)
	if err != nil {
		return nil, err
	}
	root := o.Root
	people := o.People
	if people == nil {
		people = kernel.LocalDirectory{}
	}
	// DEC-008: threads, waivers, and bundle status are event streams with inline projections.
	events := es.New(db, map[string][]es.Projection{
		thread.StreamType:   {thread.Projection(ws)},
		waiver.StreamType:   {waiver.Projection(ws)},
		approval.StreamType: {approval.Projection},
	})
	settings := func(ctx context.Context) admin.Settings {
		s, err := admin.LoadSettings(ctx, db.Queries(), ws)
		if err != nil {
			slog.ErrorContext(ctx, "read of the workspace settings failed; the defaults apply", "err", err)
		}
		return s
	}
	gateway := &model.Gateway{DB: db, Workspace: ws, Sealer: sealer}
	profiles := &profile.Registry{DB: db, Workspace: ws, Dir: o.ProfilesDir, Hosted: root == nil}
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
		ES:       events,
	}
	if root == nil {
		// REQ-009: the admin sets the size limits of hosted mode.
		svc.Limits = func(ctx context.Context) source.Limits { return settings(ctx).Limits() }
	}
	// After every change: lint (DEC-027), then end the waivers whose section changed (REQ-074),
	// then revoke the approvals of changed bundles (REQ-077).
	svc.AfterChange = func(ctx context.Context) error {
		if err := reviews.EnsureLinted(ctx); err != nil {
			return err
		}
		if err := invalidateWaivers(ctx, db, events, ws); err != nil {
			return err
		}
		return approval.OnNewVersions(ctx, db, events, ws)
	}
	if err := svc.Sync(ctx); err != nil {
		return nil, err
	}
	// SDD §7.2: one worker runs queued reviews.
	go reviews.Work(ctx)
	shareAPI := &share.API{DB: db, Workspace: ws}
	reviewAPI := &review.API{DB: db, Workspace: ws, Service: reviews, Change: svc.Change}
	threadAPI := &thread.API{DB: db, ES: events, Workspace: ws, People: people, Ask: reviews.Ask, Answering: reviews.Answering}
	waiverAPI := &waiver.API{DB: db, ES: events, Workspace: ws, Profiles: profiles.Current, People: people, Change: svc.Change}
	return &App{
		Workspace: ws, Bundles: svc, Profiles: profiles, Reviews: reviews, Admin: adminAPI, Share: shareAPI,
		API: speccyhttp.API{
			Core: speccyhttp.Core{Maintainer: func(ctx context.Context, userID string) bool {
				ok, _ := db.Queries().IsAnyMaintainer(ctx, pgdb.IsAnyMaintainerParams{WorkspaceID: ws, UserID: userID})
				return ok
			}},
			BundleAPI:   &bundle.API{Service: svc, Profiles: profiles.Current},
			VersionAPI:  &version.API{DB: db, Workspace: ws},
			ExportAPI:   &export.API{DB: db, Workspace: ws},
			ProfileAPI:  &profile.API{Registry: profiles, People: people},
			ReviewAPI:   reviewAPI,
			AdminAPI:    adminAPI,
			ShareAPI:    shareAPI,
			ThreadAPI:   threadAPI,
			WaiverAPI:   waiverAPI,
			ApprovalAPI: &approval.API{DB: db, ES: events, Workspace: ws, Profiles: profiles.Current, People: people},
			InboxAPI:    &inbox.API{DB: db, Workspace: ws, People: people},
			InsightsAPI: &insights.API{DB: db, Workspace: ws, Profiles: profiles.Current},
			TourAPI:     &tour.API{DB: db, Workspace: ws, Reviews: reviewAPI, Threads: threadAPI, Waivers: waiverAPI},
		},
	}, nil
}

// invalidateWaivers ends the approved waivers of every bundle whose section changed (REQ-074).
func invalidateWaivers(ctx context.Context, db *store.DB, events *es.Store, ws uuid.UUID) error {
	rows, err := db.Queries().ListWorkspaceWaivers(ctx, ws)
	if err != nil {
		return err
	}
	seen := map[uuid.UUID]bool{}
	for _, w := range rows {
		if w.Status != waiver.StatusApproved || seen[w.BundleID] {
			continue
		}
		seen[w.BundleID] = true
		b, err := db.Queries().GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: ws, ID: w.BundleID})
		if err != nil {
			return err
		}
		if err := waiver.Invalidate(ctx, db, events, b); err != nil {
			return err
		}
	}
	return nil
}
