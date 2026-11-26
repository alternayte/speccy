package breakdown

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/store"
)

// JobKindPropose is the job kind of a breakdown proposal (docs/specs/breakdown-proposal-job.md).
const JobKindPropose = "breakdown_propose"

// proposalDays is how long a proposal row stays: the review_request rule.
const proposalDays = 90

// stoppedProposal is the reason of a proposal whose job ended with no result.
const stoppedProposal = "The server stopped during the proposal. Propose again."

type proposeJob struct {
	ID string `json:"id"`
}

// RegisterJobs registers the proposal job on q.
func (a *API) RegisterJobs(q *queue.Queue) {
	a.Queue = q
	q.Register(JobKindPropose, queue.Interactive, 3, a.handlePropose, a.endOrphanPropose)
}

// QueueProposal runs the checks that refuse a proposal, then stores it and queues its job in one
// transaction. A doc with a queued or running proposal gets no second one.
func (a *API) QueueProposal(ctx context.Context, docID uuid.UUID, ack bool) (api.BreakdownProposal, error) {
	b, _, err := a.mayPropose(ctx, docID, ack)
	if err != nil {
		return api.BreakdownProposal{}, err
	}
	id, now := kernel.NewID(), a.now()
	err = a.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		last, err := q.LatestBreakdownProposal(ctx, pgdb.LatestBreakdownProposalParams{WorkspaceID: a.Workspace, SpecDocID: b.ID})
		if err == nil && (last.Status == "queued" || last.Status == "running") {
			return kernel.Conflict("proposal_running", "A breakdown proposal of this doc is still running. Wait for it to end.")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := q.DeleteBreakdownProposalsBefore(ctx, pgdb.DeleteBreakdownProposalsBeforeParams{WorkspaceID: a.Workspace,
			Before: now.AddDate(0, 0, -proposalDays)}); err != nil {
			return err
		}
		if err := q.InsertBreakdownProposal(ctx, pgdb.InsertBreakdownProposalParams{ID: id, WorkspaceID: a.Workspace, SpecDocID: b.ID,
			Acknowledged: ack, CreatedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: now}); err != nil {
			return err
		}
		_, err = a.Queue.Insert(ctx, &tx, JobKindPropose, proposeJob{ID: id.String()}, nil)
		return err
	})
	if err != nil {
		return api.BreakdownProposal{}, err
	}
	return api.BreakdownProposal{Id: id, Status: api.BreakdownProposalStatusQueued, CreatedAt: now}, nil
}

// handlePropose runs one proposal as the person who asked for it.
func (a *API) handlePropose(ctx context.Context, payload []byte) error {
	var p proposeJob
	if json.Unmarshal(payload, &p) != nil {
		return nil //nolint:nilerr // a payload that does not parse names no proposal
	}
	id, err := uuid.Parse(p.ID)
	if err != nil {
		return nil //nolint:nilerr // a payload that names no proposal leaves nothing to run
	}
	q := a.DB.Queries()
	row, err := q.GetBreakdownProposal(ctx, pgdb.GetBreakdownProposalParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Status == "complete" || row.Status == "failed" {
		return nil
	}
	if err := q.StartBreakdownProposal(ctx, id); err != nil {
		return err
	}
	ctx = kernel.WithActor(ctx, kernel.Actor{UserID: row.CreatedBy})
	_, runErr := a.Propose(ctx, row.SpecDocID, row.Acknowledged)
	if runErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	finish := pgdb.FinishBreakdownProposalParams{ID: id, Status: "complete", FinishedAt: sql.NullTime{Time: a.now(), Valid: true}}
	if runErr != nil {
		finish.Status, finish.Error = "failed", detail(runErr)
	}
	return q.FinishBreakdownProposal(ctx, finish)
}

// endOrphanPropose fails a proposal whose job ended with no result.
func (a *API) endOrphanPropose(ctx context.Context, payload []byte) error {
	var p proposeJob
	if json.Unmarshal(payload, &p) != nil {
		return nil //nolint:nilerr // a payload that does not parse names no proposal
	}
	id, err := uuid.Parse(p.ID)
	if err != nil {
		return nil //nolint:nilerr // a payload that names no proposal leaves nothing to end
	}
	return a.DB.Queries().FinishBreakdownProposal(ctx, pgdb.FinishBreakdownProposalParams{ID: id, Status: "failed",
		Error: stoppedProposal, FinishedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}})
}

// detail is the sentence of an error for a person.
func detail(err error) string {
	if ke, ok := kernel.AsError(err); ok {
		return ke.Detail
	}
	return err.Error()
}

// GetBreakdownProposal returns the newest proposal of a doc.
func (a *API) GetBreakdownProposal(ctx context.Context, req api.GetBreakdownProposalRequestObject) (api.GetBreakdownProposalResponseObject, error) {
	row, err := a.DB.Queries().LatestBreakdownProposal(ctx, pgdb.LatestBreakdownProposalParams{WorkspaceID: a.Workspace, SpecDocID: req.DocId})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("no_proposal", "This doc has no breakdown proposal.")
	}
	if err != nil {
		return nil, err
	}
	out := api.BreakdownProposal{Id: row.ID, Status: api.BreakdownProposalStatus(row.Status), CreatedAt: row.CreatedAt}
	if row.Error != "" {
		out.Error = &row.Error
	}
	if row.FinishedAt.Valid {
		out.FinishedAt = &row.FinishedAt.Time
	}
	return api.GetBreakdownProposal200JSONResponse(out), nil
}
