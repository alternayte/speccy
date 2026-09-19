package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the run endpoints.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Service   *Service
	// Change writes accepted trace IDs (REQ-052); the bundle feature provides it.
	Change Change
}

// Summary returns the verdict to show for a bundle, and the error of the latest run when it
// failed. The verdict is stale when its run is not on the current version (SDD §8.6).
func Summary(ctx context.Context, q store.Querier, b pgdb.Bundle) (*api.BundleVerdict, *string, error) {
	runs, err := q.ListRuns(ctx, pgdb.ListRunsParams{BundleID: b.ID, Before: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), PageSize: 20})
	if err != nil {
		return nil, nil, err
	}
	var runErr *string
	failedAfter := false
	for _, r := range runs {
		switch r.Status {
		case "queued", "running":
			continue
		case "failed":
			// REQ-024: a failed run on the current version makes the previous verdict stale.
			if r.VersionID == b.CurrentVersionID.UUID {
				if runErr == nil {
					e := r.Error
					runErr = &e
				}
				failedAfter = true
			}
			continue
		}
		v, err := runVerdict(ctx, q, b, r)
		if err != nil || v == nil {
			return v, runErr, err
		}
		if failedAfter {
			v.Result = api.VerdictResult(verdict.Stale)
		}
		// §8.6 rule 2 holds now, not only when the run ended: a thread marked blocking after the
		// run makes the verdict Not Build Ready at once, and resolving it restores the run's result.
		n, err := q.CountOpenBlockingThreads(ctx, uuid.NullUUID{UUID: b.ID, Valid: true})
		if err != nil {
			return nil, nil, err
		}
		v.BlockingThreads = ptrInt(int(n))
		if n > 0 && v.Result == api.VerdictResult(verdict.BuildReady) {
			v.Result = api.VerdictResult(verdict.NotBuildReady)
		}
		return v, runErr, nil
	}
	return nil, runErr, nil
}

func runVerdict(ctx context.Context, q store.Querier, b pgdb.Bundle, run pgdb.ReviewRun) (*api.BundleVerdict, error) {
	vd, err := q.GetVerdict(ctx, run.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: run.VersionID})
	if err != nil {
		return nil, err
	}
	fs, err := q.ListFindings(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	moved, err := upstreamMoved(ctx, q, b.WorkspaceID, run.ID)
	if err != nil {
		return nil, err
	}
	current := run.VersionID == b.CurrentVersionID.UUID
	out := &api.BundleVerdict{
		RunId: run.ID, VersionNumber: ver.Number, Kind: api.BundleVerdictKind(run.Kind),
		// §8.6 rule 4, REQ-056: a verdict that read an old version of a linked bundle is stale.
		Result: api.VerdictResult(verdict.For(verdict.Result(vd.Result), current && !moved)),
		Score:  int(vd.Score), WaiverCount: int(vd.WaiverCount), RelaxedCount: int(vd.RelaxedCount), Radar: map[string]int{},
	}
	if current && moved {
		reason := api.UpstreamChanged
		out.StaleReason = &reason
	}
	_ = json.Unmarshal(vd.Radar, &out.Radar)
	for _, f := range fs {
		if f.Waived {
			continue
		}
		switch kernel.Level(f.Level) {
		case kernel.Must:
			out.Must++
		case kernel.Should:
			out.Should++
		default:
			out.Info++
		}
	}
	return out, nil
}

func (a *API) run(ctx context.Context, id uuid.UUID) (pgdb.ReviewRun, pgdb.Bundle, error) {
	q := a.DB.Queries()
	run, err := q.GetRun(ctx, pgdb.GetRunParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return run, pgdb.Bundle{}, kernel.NotFound("run_not_found", "No review run has the ID %s.", id)
	}
	if err != nil {
		return run, pgdb.Bundle{}, err
	}
	b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: run.BundleID})
	return run, b, err
}

func (a *API) toAPI(ctx context.Context, b pgdb.Bundle, run pgdb.ReviewRun) (api.Run, error) {
	q := a.DB.Queries()
	ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: run.VersionID})
	if err != nil {
		return api.Run{}, err
	}
	out := api.Run{
		Id: run.ID, BundleId: run.BundleID, VersionId: run.VersionID, VersionNumber: ver.Number,
		ProfileKey: run.ProfileKey, ProfileVersion: run.ProfileVersion, Kind: api.RunKind(run.Kind),
		Status: api.RunStatus(run.Status), Stage: run.Stage, Error: run.Error, StartedAt: run.StartedAt.UTC(),
	}
	if run.FinishedAt.Valid {
		t := run.FinishedAt.Time.UTC()
		out.FinishedAt = &t
	}
	out.TokensIn, out.TokensOut, out.CacheHits = &run.TokensIn, &run.TokensOut, &run.CacheHits
	cost := float32(run.CostEstimate)
	out.CostEstimate = &cost
	notes := []string{}
	_ = json.Unmarshal(run.Notes, &notes)
	out.Notes = &notes
	if run.Status == "complete" {
		if out.Verdict, err = runVerdict(ctx, q, b, run); err != nil {
			return api.Run{}, err
		}
	}
	return out, nil
}

// ListRuns lists the runs of a bundle, newest first.
func (a *API) ListRuns(ctx context.Context, req api.ListRunsRequestObject) (api.ListRunsResponseObject, error) {
	q := a.DB.Queries()
	b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: req.BundleId})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("bundle_not_found", "No bundle has the ID %s.", req.BundleId)
	}
	if err != nil {
		return nil, err
	}
	limit := int64(20)
	if req.Params.Limit != nil {
		limit = int64(*req.Params.Limit)
	}
	runs, err := q.ListRuns(ctx, pgdb.ListRunsParams{BundleID: b.ID, Before: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), PageSize: limit})
	if err != nil {
		return nil, err
	}
	out := api.RunList{Items: []api.Run{}}
	for _, r := range runs {
		ar, err := a.toAPI(ctx, b, r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, ar)
	}
	return api.ListRuns200JSONResponse(out), nil
}

// GetRun returns one run with its verdict.
func (a *API) GetRun(ctx context.Context, req api.GetRunRequestObject) (api.GetRunResponseObject, error) {
	run, b, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	out, err := a.toAPI(ctx, b, run)
	if err != nil {
		return nil, err
	}
	return api.GetRun200JSONResponse(out), nil
}

// ListFindings returns the findings of a run in document order.
func (a *API) ListFindings(ctx context.Context, req api.ListFindingsRequestObject) (api.ListFindingsResponseObject, error) {
	run, _, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Queries().ListFindings(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := api.FindingList{Items: make([]api.Finding, 0, len(rows))}
	for _, f := range rows {
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		var sugg struct {
			Fix string `json:"fix"`
		}
		_ = json.Unmarshal(f.Suggestion, &sugg)
		af := api.Finding{
			Id: f.ID, CheckSlug: f.CheckSlug, Level: api.FindingLevel(f.Level), Stage: f.Stage, Relaxed: f.Relaxed, Message: f.Message, Waived: f.Waived,
			Anchor: api.Anchor{File: an.File, HeadingPath: an.HeadingPath, Quote: an.Quote, Prefix: an.Prefix, Suffix: an.Suffix, Start: an.Start, End: an.End},
		}
		if af.Anchor.HeadingPath == nil {
			af.Anchor.HeadingPath = []string{}
		}
		if sugg.Fix != "" {
			af.Fix = &sugg.Fix
		}
		out.Items = append(out.Items, af)
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if out.Items[i].Anchor.File != out.Items[j].Anchor.File {
			return out.Items[i].Anchor.File < out.Items[j].Anchor.File
		}
		return out.Items[i].Anchor.Start < out.Items[j].Anchor.Start
	})
	return api.ListFindings200JSONResponse(out), nil
}

func ptrInt(n int) *int { return &n }
