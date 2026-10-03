// Package app builds Speccy's services and API on an open store, for local and hosted mode.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/admin"
	"github.com/alternayte/speccy/internal/features/approval"
	"github.com/alternayte/speccy/internal/features/bundle"
	"github.com/alternayte/speccy/internal/features/export"
	"github.com/alternayte/speccy/internal/features/handoff"
	"github.com/alternayte/speccy/internal/features/inbox"
	"github.com/alternayte/speccy/internal/features/insights"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/prreview"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/thread"
	"github.com/alternayte/speccy/internal/features/tour"
	"github.com/alternayte/speccy/internal/features/verify"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/features/waiver"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/sourceresolve"
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
	PRs       *prreview.API
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
		DB: db, Workspace: ws, Profiles: profiles.Current, Repo: svc.RepoConfig, Decisions: svc.Decisions,
		Gateway: gateway, Search: adminAPI.SearchSource, Fetch: adminAPI.FetchSource, Progress: review.NewBroker(),
		// REQ-105: the admin sets the parallel model calls.
		Parallel: func(ctx context.Context) int { return settings(ctx).ParallelCalls },
		// The metadata resolver reads a grounding source's redirect chain and dates. The admin
		// can turn it off.
		Resolve:        sourceresolve.New(),
		ResolveSources: func(ctx context.Context) bool { return settings(ctx).ResolveSourcesOn() },
		ES:             events,
	}
	if root != nil {
		// REQ-129: local mode reads GitHub with the machine's gh login, and falls back to a
		// token pasted in the app.
		svc.GitHub = adminAPI.LocalGitHubClient
		reviews.GitHub = adminAPI.LocalGitHubClient
	}
	if root == nil {
		// REQ-123: hosted mode reads bundles from GitHub with the workspace token.
		svc.GitHub = adminAPI.GitHubClient
		reviews.GitHub = adminAPI.GitHubClient
		// REQ-009: the admin sets the size limits of hosted mode.
		svc.Limits = func(ctx context.Context) source.Limits { return settings(ctx).Limits() }
	}
	// After every change: lint (DEC-027), then end the waivers whose section changed (REQ-074),
	// then revoke the approvals of changed bundles (REQ-077).
	svc.AfterChange = func(ctx context.Context) error {
		if err := reviews.EnsureLinted(ctx); err != nil {
			return err
		}
		if err := invalidateWaivers(ctx, db, events, ws, svc, profiles.Current); err != nil {
			return err
		}
		return approval.OnNewVersions(ctx, db, events, ws)
	}
	// A full review that passes a whole-doc check ends the waiver of that check, and one that
	// fails it again brings the waiver back.
	reviews.AfterReview = func(ctx context.Context, b pgdb.SpecDoc) error {
		cur, err := db.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: ws, ID: b.ID})
		if err != nil {
			return err
		}
		return waiver.Invalidate(ctx, db, events, cur, svc.Decisions, profiles.Current)
	}
	// Local mode has one user, who asked for the review: a link to an upstream doc on GitHub
	// adds its source (#97). In hosted mode only an admin adds a source. The bundle API that
	// adds it is built below, after the first sync, whose lint already reads the setting.
	var addSource func(ctx context.Context, url string) error
	if root != nil {
		reviews.AddSource = func(ctx context.Context, url string) error { return addSource(ctx, url) }
	}
	if err := svc.Sync(ctx); err != nil {
		return nil, err
	}
	// REQ-123: both modes keep their GitHub sources in step.
	go svc.WatchGitHub(ctx, 5*time.Minute)
	shareAPI := &share.API{DB: db, Workspace: ws}
	reviewAPI := &review.API{DB: db, Workspace: ws, Service: reviews, Change: svc.Change}
	prs := &prreview.API{DB: db, Workspace: ws, Reviews: reviews, ReviewAPI: reviewAPI, Gateway: gateway, Profiles: profiles.Current,
		GitHub: reviews.GitHub, LocalMode: root != nil, Life: ctx}
	if root != nil {
		// A batch that a stopped process left runs nowhere (docs/specs/pr-review-batch.md).
		if err := prs.EndOrphans(ctx); err != nil {
			return nil, err
		}
	}
	threadAPI := &thread.API{DB: db, ES: events, Workspace: ws, People: people, Ask: reviews.Ask, Answering: reviews.Answering}
	waiverAPI := &waiver.API{DB: db, ES: events, Workspace: ws, Profiles: profiles.Current, People: people, Change: svc.Change,
		Decisions: svc.Decisions, SetDecisions: svc.SetDecisions}
	approvalAPI := &approval.API{DB: db, ES: events, Workspace: ws, Profiles: profiles.Current, People: people}
	handoffAPI := &handoff.API{DB: db, Workspace: ws, Profiles: profiles.Current, Reviews: reviews, Questions: reviewAPI, People: people, Threads: threadAPI}
	tourAPI := &tour.API{DB: db, Workspace: ws, Reviews: reviewAPI, Threads: threadAPI, Waivers: waiverAPI}
	verifyAPI := &verify.API{DB: db, Workspace: ws, Profiles: profiles.Current, Gateway: gateway,
		GitHub: reviews.GitHub, Threads: threadAPI, Progress: reviews.Progress, Wake: reviews.Wake, Local: root != nil}
	// SDD §7.2: one worker runs queued reviews, and the queued verification runs with them.
	reviews.Jobs = map[string]func(context.Context, []byte) error{verify.JobKind: verifyAPI.Execute}
	if root != nil {
		// One process owns a local state. A job it finds at its start belongs to a process that
		// stopped, so its run ends now and the doc can be reviewed again at once (#87).
		if err := reviews.EndOrphans(ctx, map[string]func(context.Context, []byte) error{verify.JobKind: verifyAPI.EndOrphan}); err != nil {
			return nil, err
		}
	}
	go reviews.Work(ctx)
	bundleAPI := &bundle.API{Service: svc, Profiles: profiles.Current, Deps: bundle.Deps{
		Waiting:    waiverAPI.Waiting,
		FirstPoint: tourAPI.FirstPoint,
		FirstMust:  reviewAPI.FirstMust,
		Approvals:  approvalAPI.ApprovalCount,
		Handoffs:   handoffAPI.CountForVersion,
	}}
	addSource = func(ctx context.Context, url string) error {
		_, err := bundleAPI.AddGithubSource(ctx, api.AddGithubSourceRequestObject{Body: &api.AddGithubSourceJSONRequestBody{Url: url}})
		return err
	}
	return &App{
		Workspace: ws, Bundles: svc, Profiles: profiles, Reviews: reviews, Admin: adminAPI, Share: shareAPI, PRs: prs,
		API: speccyhttp.API{
			Core: speccyhttp.Core{Maintainer: func(ctx context.Context, userID string) bool {
				ok, _ := db.Queries().IsAnyMaintainer(ctx, pgdb.IsAnyMaintainerParams{WorkspaceID: ws, UserID: userID})
				return ok
			}, CarriedPaths: func(ctx context.Context, docID uuid.UUID) map[string]string {
				d, err := db.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: ws, ID: docID})
				if err != nil || !d.CurrentVersionID.Valid {
					return nil
				}
				v, err := db.Queries().GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: d.ID, ID: d.CurrentVersionID.UUID})
				if err != nil {
					return nil
				}
				return version.CarriedPaths(v)
			}},
			BundleAPI:   bundleAPI,
			VersionAPI:  &version.API{DB: db, Workspace: ws},
			ExportAPI:   &export.API{DB: db, Workspace: ws, Reviews: reviewAPI},
			ProfileAPI:  &profile.API{Registry: profiles, People: people},
			ReviewAPI:   reviewAPI,
			AdminAPI:    adminAPI,
			ShareAPI:    shareAPI,
			ThreadAPI:   threadAPI,
			WaiverAPI:   waiverAPI,
			ApprovalAPI: approvalAPI,
			InboxAPI:    &inbox.API{DB: db, Workspace: ws, People: people, Waivers: waiverAPI},
			InsightsAPI: &insights.API{DB: db, Workspace: ws, Profiles: profiles.Current, Decisions: svc.Decisions},
			TourAPI:     tourAPI,
			HandoffAPI:  handoffAPI,
			VerifyAPI:   verifyAPI,
			PrReviewAPI: prs,
		},
	}, nil
}

// Idle reports whether no review job and no pull request batch runs. An owner that wants to
// exit waits for it.
func (a *App) Idle(ctx context.Context) (bool, error) {
	if !a.PRs.Idle() {
		return false, nil
	}
	return a.Reviews.Idle(ctx)
}

// invalidateWaivers ends the approved waivers of every bundle whose section changed (REQ-074),
// and brings back an ended one whose section returned to its approved text.
func invalidateWaivers(ctx context.Context, db *store.DB, events *es.Store, ws uuid.UUID, svc *bundle.Service, profiles func() map[string]profile.Versioned) error {
	rows, err := db.Queries().ListWorkspaceWaivers(ctx, ws)
	if err != nil {
		return err
	}
	seen := map[uuid.UUID]bool{}
	for _, w := range rows {
		if w.Status != waiver.StatusApproved && w.Status != waiver.StatusInvalidated || seen[w.SpecDocID] {
			continue
		}
		seen[w.SpecDocID] = true
		b, err := db.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: ws, ID: w.SpecDocID})
		if err != nil {
			return err
		}
		if err := waiver.Invalidate(ctx, db, events, b, svc.Decisions, profiles); err != nil {
			return err
		}
	}
	return nil
}
