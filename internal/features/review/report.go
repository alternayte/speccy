package review

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// GetRunReport returns the run report (SDD §13.1): when each stage ran, how diverse the
// readers were, and the open findings by radar category. Tokens, cost, and cache hits are on
// the run itself (REQ-022).
func (a *API) GetRunReport(ctx context.Context, req api.GetRunReportRequestObject) (api.GetRunReportResponseObject, error) {
	run, _, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	out := api.RunReport{Stages: []api.StageTiming{}, Categories: []api.CategoryCount{}}
	var stages []stageTiming
	_ = json.Unmarshal(run.Stages, &stages)
	for _, st := range stages {
		out.Stages = append(out.Stages, api.StageTiming{Stage: st.Stage, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt})
	}

	// REQ-046: the models behind the reader roles, counted but not named (DEC-013).
	roles := map[string]string{}
	_ = json.Unmarshal(run.Roles, &roles)
	readers, models := 0, map[string]bool{}
	for role, fp := range roles {
		if strings.HasPrefix(role, "reader_") {
			readers++
			models[fp] = true
		}
	}
	if readers > 0 {
		out.Readers = &api.ReaderDiversity{Readers: readers, DistinctModels: len(models), Low: len(models) < 2}
	}

	var radar map[string]int
	if vd, err := a.DB.Queries().GetVerdict(ctx, run.ID); err == nil {
		_ = json.Unmarshal(vd.Radar, &radar)
	}
	rows, err := a.DB.Queries().ListFindings(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	counts := map[verdict.Category]*api.CategoryCount{}
	for _, c := range verdict.Categories {
		cc := &api.CategoryCount{Category: string(c)}
		if score, ok := radar[string(c)]; ok {
			cc.Score = &score
		}
		counts[c] = cc
	}
	for _, f := range rows {
		if f.Waived {
			continue
		}
		cc := counts[findingCategory(f.CheckSlug, f.Stage)]
		if cc == nil {
			continue
		}
		switch kernel.Level(f.Level) {
		case kernel.Must:
			cc.Must++
		case kernel.Should:
			cc.Should++
		default:
			cc.Info++
		}
	}
	for _, c := range verdict.Categories {
		out.Categories = append(out.Categories, *counts[c])
	}
	return api.GetRunReport200JSONResponse(out), nil
}

// findingCategory is the radar axis of a finding's check (SDD §8.7).
func findingCategory(slug, stage string) verdict.Category {
	switch stage {
	case StageLint:
		if c, ok := categories[slug]; ok {
			return c
		}
	case StageRubric:
		return verdict.Completeness
	case StageGrounding:
		return verdict.Evidence
	case StageDivergence:
		return verdict.Precision
	case StageCoherence:
		return verdict.Coherence
	}
	return verdict.Structure
}
