package verify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/verify"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// RunVerification verifies one build of a bundle against a code repo at one commit.
func (a *API) RunVerification(ctx context.Context, req api.RunVerificationRequestObject) (api.RunVerificationResponseObject, error) {
	in := Input{BundleID: req.BundleId}
	if b := req.Body; b != nil {
		in.Repo, in.SHA, in.Path = str(b.Repo), str(b.Sha), str(b.Path)
		in.HandoffID = b.HandoffId
		if b.Claims != nil {
			for _, c := range *b.Claims {
				cl := Claim{TraceID: c.TraceId}
				for _, t := range c.Targets {
					cl.Targets = append(cl.Targets, verify.Target{
						Kind: verify.Kind(t.Kind), Path: t.Path, Quote: t.Quote})
				}
				in.Claims = append(in.Claims, cl)
			}
		}
	}
	run, err := a.Verify(ctx, in)
	if err != nil {
		return nil, err
	}
	return api.RunVerification200JSONResponse(runAPI(req.BundleId, run, nil, false)), nil
}

// GetVerification returns one stored run with the outcome of each trace ID.
func (a *API) GetVerification(ctx context.Context, req api.GetVerificationRequestObject) (api.GetVerificationResponseObject, error) {
	q := a.DB.Queries()
	row, err := q.GetVerificationRun(ctx, pgdb.GetVerificationRunParams{WorkspaceID: a.Workspace, ID: req.RunId})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("verification_not_found", "No verification run has this ID.")
	}
	if err != nil {
		return nil, err
	}
	out, err := a.stored(ctx, row)
	if err != nil {
		return nil, err
	}
	return api.GetVerification200JSONResponse(out), nil
}

// ListVerifications lists the runs of a bundle, newest first.
func (a *API) ListVerifications(ctx context.Context, req api.ListVerificationsRequestObject) (api.ListVerificationsResponseObject, error) {
	q := a.DB.Queries()
	if _, err := version.Bundle(ctx, q, a.Workspace, req.BundleId); err != nil {
		return nil, err
	}
	rows, err := q.ListVerificationRuns(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	out := api.ListVerifications200JSONResponse{Items: []api.Verification{}}
	for _, r := range rows {
		v, err := a.stored(ctx, r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// stored reads one run's outcomes and returns the API shape. A run is stale when the bundle
// has moved to another version since it ran: the requirements moved, so the run's statement is
// about a doc that no longer stands. Speccy reads that from the bundle, so a version recorded
// after the run counts at once.
func (a *API) stored(ctx context.Context, row pgdb.VerificationRun) (api.Verification, error) {
	q := a.DB.Queries()
	rows, err := q.ListVerificationOutcomes(ctx, row.ID)
	if err != nil {
		return api.Verification{}, err
	}
	stale := row.Stale
	if b, err := version.Bundle(ctx, q, a.Workspace, row.BundleID); err == nil {
		stale = stale || b.CurrentVersionID.UUID != row.VersionID
	}
	row.Stale = stale
	run := Run{ID: row.ID, Verdict: verify.Verdict(row.Verdict), Repo: row.Repo, SHA: row.Sha,
		BaseSHA: row.BaseSha, Digest: row.Digest, Stale: row.Stale, At: row.CreatedAt}
	_ = json.Unmarshal(row.Counts, &run.Counts)
	_ = json.Unmarshal(row.Notes, &run.Notes)
	for _, o := range rows {
		out := Outcome{Result: verify.Result{ID: o.TraceID, Outcome: verify.Outcome(o.Outcome),
			Level: kernel.Level(o.Level), Blocks: o.Blocks, Waived: o.Waived, Note: o.Note},
			Provenance: verify.Provenance(o.Provenance)}
		_ = json.Unmarshal(o.Targets, &out.Targets)
		_ = json.Unmarshal(o.Judgement, &out.Judgement)
		run.Results = append(run.Results, out)
	}
	var handoff *uuid.UUID
	if row.HandoffID.Valid {
		handoff = &row.HandoffID.UUID
	}
	v := runAPI(row.BundleID, run, handoff, row.Stale)
	v.StartedBy = &row.StartedBy
	return v, nil
}

func runAPI(bundle uuid.UUID, run Run, handoff *uuid.UUID, stale bool) api.Verification {
	out := api.Verification{
		Id: run.ID, BundleId: bundle, HandoffId: handoff, Verdict: api.VerificationVerdict(run.Verdict),
		Repo: run.Repo, Sha: run.SHA, Counts: countsAPI(run.Counts), Notes: nonNil(run.Notes),
		Stale: stale, CreatedAt: run.At, Outcomes: []api.VerificationOutcome{},
	}
	if run.BaseSHA != "" {
		out.BaseSha = &run.BaseSHA
	}
	if run.Digest != "" {
		out.Digest = &run.Digest
	}
	for _, o := range run.Results {
		out.Outcomes = append(out.Outcomes, outcomeAPI(o))
	}
	return out
}

func outcomeAPI(o Outcome) api.VerificationOutcome {
	out := api.VerificationOutcome{
		TraceId: o.ID, Outcome: api.VerificationOutcomeOutcome(o.Outcome), Level: api.VerificationOutcomeLevel(o.Level),
		Blocks: o.Blocks, Waived: o.Waived, Provenance: api.VerificationOutcomeProvenance(o.Provenance),
		Targets: []api.VerificationTarget{},
	}
	if o.Note != "" {
		out.Note = &o.Note
	}
	for _, t := range o.Targets {
		tg := api.VerificationTarget{Kind: api.VerificationTargetKind(t.Kind), Path: t.Path, Quote: t.Quote,
			Holds: &t.Holds}
		if t.Line > 0 {
			line := t.Line
			tg.Line = &line
		}
		if t.Fault != "" {
			f := t.Fault
			tg.Fault = &f
		}
		if t.Provenance != "" {
			p := api.VerificationTargetProvenance(t.Provenance)
			tg.Provenance = &p
		}
		out.Targets = append(out.Targets, tg)
	}
	if q := o.Judgement.RequirementQuote; q != "" {
		out.RequirementQuote = &q
	}
	if q := o.Judgement.CodeQuote; q != "" {
		out.CodeQuote = &q
	}
	if r := o.Judgement.Reason; r != "" {
		out.Reason = &r
	}
	return out
}

func countsAPI(c verify.Counts) api.VerificationCounts {
	return api.VerificationCounts{Implemented: c.Implemented, Untested: c.Untested, Unproven: c.Unproven,
		Missing: c.Missing, Breached: c.Breached, Waived: c.Waived, Blocking: c.Blocking, Skipped: c.Skipped}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
