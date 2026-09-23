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
	"github.com/alternayte/speccy/internal/features/version"
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
func Summary(ctx context.Context, q store.Querier, b pgdb.SpecDoc) (*api.BundleVerdict, *string, error) {
	runs, err := q.ListRuns(ctx, pgdb.ListRunsParams{SpecDocID: b.ID, Before: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), PageSize: 20})
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

func runVerdict(ctx context.Context, q store.Querier, b pgdb.SpecDoc, run pgdb.ReviewRun) (*api.BundleVerdict, error) {
	vd, err := q.GetVerdict(ctx, run.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: run.VersionID})
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
	carried, err := carriedRows(ctx, q, run.ID)
	if err != nil {
		return nil, err
	}
	fs = append(fs, carried...)
	if run.Kind == "full" {
		out.AiRunId = &run.ID
	}
	if vd.CarriedRunID.Valid {
		if full, err := q.GetRunByID(ctx, vd.CarriedRunID.UUID); err == nil && full.VersionID != run.VersionID {
			if fv, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: full.VersionID}); err == nil {
				n, changed := fv.Number, int(vd.SectionsChanged)
				out.AiRunId, out.AiVersionNumber, out.SectionsChanged = &full.ID, &n, &changed
			}
		} else if err == nil {
			out.AiRunId = &full.ID
		}
	}
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

func (a *API) run(ctx context.Context, id uuid.UUID) (pgdb.ReviewRun, pgdb.SpecDoc, error) {
	q := a.DB.Queries()
	run, err := q.GetRun(ctx, pgdb.GetRunParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return run, pgdb.SpecDoc{}, kernel.NotFound("run_not_found", "No review run has the ID %s.", id)
	}
	if err != nil {
		return run, pgdb.SpecDoc{}, err
	}
	b, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: run.SpecDocID})
	return run, b, err
}

func (a *API) toAPI(ctx context.Context, b pgdb.SpecDoc, run pgdb.ReviewRun) (api.Run, error) {
	q := a.DB.Queries()
	ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: run.VersionID})
	if err != nil {
		return api.Run{}, err
	}
	out := api.Run{
		Id: run.ID, BundleId: run.SpecDocID, VersionId: run.VersionID, VersionNumber: ver.Number,
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
	b, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: req.BundleId})
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
	runs, err := q.ListRuns(ctx, pgdb.ListRunsParams{SpecDocID: b.ID, Before: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), PageSize: limit})
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

// ListFindings returns the findings of a run in document order. When the run read an older
// version, each anchor moves to the current version, or is marked detached (SDD §8.8).
func (a *API) ListFindings(ctx context.Context, req api.ListFindingsRequestObject) (api.ListFindingsResponseObject, error) {
	run, b, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	q := a.DB.Queries()
	rows, err := q.ListFindings(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	// A lint run's verdict counts the AI findings of the last full review whose sections did not
	// change. Their rows stay in the full run, so each keeps its ID; the waiver is today's.
	carried, err := carriedRows(ctx, q, run.ID)
	if err != nil {
		return nil, err
	}
	rows = append(rows, carried...)
	var cur *version.Current
	if b.CurrentVersionID.Valid && (b.CurrentVersionID.UUID != run.VersionID || len(carried) > 0) {
		if cur, err = version.LoadCurrent(ctx, q, b); err != nil {
			return nil, err
		}
	}
	out := api.FindingList{Items: make([]api.Finding, 0, len(rows))}
	for _, f := range rows {
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		detached := false
		if cur != nil {
			var ok bool
			an, ok = cur.Anchor(an)
			detached = !ok
		}
		var sugg struct {
			Fix string `json:"fix"`
		}
		_ = json.Unmarshal(f.Suggestion, &sugg)
		af := api.Finding{
			Id: f.ID, RunId: f.RunID, CheckSlug: f.CheckSlug, Level: api.FindingLevel(f.Level), Stage: f.Stage, Relaxed: f.Relaxed, Message: f.Message, Waived: f.Waived,
			Anchor: anchorAPI(an),
		}
		if detached {
			af.Anchor.Detached = &detached
		}
		if l := Layer(f.CheckSlug, f.Stage, kernel.Level(f.Level)); l != "" {
			layer := api.FindingLayer(l)
			af.Layer = &layer
		}
		if sugg.Fix != "" {
			af.Fix = &sugg.Fix
		}
		if f.CheckSlug == CodeDriftSlug {
			var ev map[string]string
			_ = json.Unmarshal(f.Evidence, &ev)
			if t := ev["verify"]; t != "" {
				af.VerifyTarget = &t
			}
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

// Layer is the overlay layer of a finding (SDD §13.2): ambiguous for divergence and gaps,
// contradicted and unverified for claims and conflicts, risk for other MUST findings, and slop
// for other lint findings. Other findings have no layer.
func Layer(slug, stage string, level kernel.Level) string {
	switch {
	case slug == DivergenceAmbiguous || slug == DivergenceGap:
		return "ambiguous"
	case slug == GroundingContradicted || slug == ContradictionSlug:
		return "contradicted"
	case slug == GroundingUnverified:
		return "unverified"
	case level == kernel.Must:
		return "risk"
	case stage == StageLint:
		return "slop"
	}
	return ""
}

func ptrInt(n int) *int { return &n }

// FirstMust returns the first MUST finding of a run that no waiver covers, or nil. The next
// action reads it (SDD §13.4).
func (a *API) FirstMust(ctx context.Context, runID uuid.UUID) (*api.Finding, error) {
	res, err := a.ListFindings(ctx, api.ListFindingsRequestObject{RunId: runID})
	if err != nil {
		return nil, err
	}
	list, ok := res.(api.ListFindings200JSONResponse)
	if !ok {
		return nil, nil
	}
	for i, f := range list.Items {
		if f.Level == api.FindingLevelMUST && !f.Waived {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// carriedRows returns the full-run findings that the verdict of runID counts, with the waiver
// the verdict found for each.
func carriedRows(ctx context.Context, q store.Querier, runID uuid.UUID) ([]pgdb.Finding, error) {
	vd, err := q.GetVerdict(ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var carried []carriedFinding
	_ = json.Unmarshal(vd.CarriedFindings, &carried)
	var out []pgdb.Finding
	for _, c := range carried {
		f, err := q.GetFinding(ctx, c.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		f.Waived = c.Waived
		out = append(out, f)
	}
	return out, nil
}
