package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/store"
)

// JobKindRequest is the job kind of a review that POST /reviews or POST /reviews/url asked for.
const JobKindRequest = "review_request"

// The kinds of a review request.
const (
	requestContent = "content"
	requestURL     = "url"
)

// RegisterJobs registers the review request job on the service's queue.
func (a *API) RegisterJobs() {
	a.Service.Queue.Register(JobKindRequest, queue.Interactive, 3, a.HandleRequest, a.EndOrphanRequest)
}

// queueRequest stores a review request and queues its job in one transaction.
func (a *API) queueRequest(ctx context.Context, kind string, body any) (api.ReviewRequest, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return api.ReviewRequest{}, err
	}
	id, now := kernel.NewID(), time.Now().UTC()
	err = a.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if err := q.DeleteReviewRequestsBefore(ctx, pgdb.DeleteReviewRequestsBeforeParams{WorkspaceID: a.Workspace,
			Before: now.AddDate(0, 0, -ContentReviewDays)}); err != nil {
			return err
		}
		if err := q.InsertReviewRequest(ctx, pgdb.InsertReviewRequestParams{ID: id, WorkspaceID: a.Workspace, Kind: kind,
			Request: dbtype.JSON(raw), CreatedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: now}); err != nil {
			return err
		}
		_, err := a.Service.Queue.Insert(ctx, &tx, JobKindRequest, requestJob{ID: id.String()}, nil)
		return err
	})
	if err != nil {
		return api.ReviewRequest{}, err
	}
	a.Service.Progress.Publish(id, Event{Type: "stage", Stage: "queued"})
	return api.ReviewRequest{Id: id, Kind: api.ReviewRequestKind(kind), Status: api.ReviewRequestStatusQueued, CreatedAt: now}, nil
}

// GetReviewRequest is GET /reviews/{reviewId}.
func (a *API) GetReviewRequest(ctx context.Context, req api.GetReviewRequestRequestObject) (api.GetReviewRequestResponseObject, error) {
	row, err := a.DB.Queries().GetReviewRequest(ctx, pgdb.GetReviewRequestParams{WorkspaceID: a.Workspace, ID: req.ReviewId})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("review_not_found", "The review does not exist, or it is older than %d days.", ContentReviewDays)
	}
	if err != nil {
		return nil, err
	}
	out := api.ReviewRequest{Id: row.ID, Kind: api.ReviewRequestKind(row.Kind), Status: api.ReviewRequestStatus(row.Status), CreatedAt: row.CreatedAt}
	if row.Error != "" {
		out.Error = &row.Error
	}
	switch row.Status {
	case "complete":
		if row.Kind == requestContent {
			var c api.ContentReview
			if err := json.Unmarshal(row.Result, &c); err != nil {
				return nil, err
			}
			out.Content = &c
		} else {
			var u api.UrlReview
			if err := json.Unmarshal(row.Result, &u); err != nil {
				return nil, err
			}
			out.Url = &u
		}
	case "running", "queued":
		if a.Service.Progress == nil {
			break
		}
		if history, _, cancel := a.Service.Progress.Subscribe(row.ID); len(history) > 0 {
			cancel()
			if st := history[len(history)-1].Stage; st != "" {
				out.Stage = &st
			}
		} else {
			cancel()
		}
	}
	return api.GetReviewRequest200JSONResponse(out), nil
}

// requestJob is the payload of a review request job.
type requestJob struct {
	ID string `json:"id"`
}

// HandleRequest is the handler of a review request job. A review that fails stores its cause
// on the request, so the job ends either way. A job whose context ends leaves the request for
// River to try again.
func (a *API) HandleRequest(ctx context.Context, payload []byte) error {
	var p requestJob
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil //nolint:nilerr // a payload that does not parse names no request
	}
	id := uuidOf(p.ID)
	q := a.DB.Queries()
	row, err := q.GetReviewRequest(ctx, pgdb.GetReviewRequestParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Status == "complete" || row.Status == "failed" {
		return nil
	}
	if err := q.StartReviewRequest(ctx, id); err != nil {
		return err
	}
	// The job has no request, so the review acts as the person who asked for it.
	ctx = kernel.WithActor(ctx, kernel.Actor{UserID: row.CreatedBy})
	result, runErr := a.runRequest(ctx, row)
	if runErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	finish := pgdb.FinishReviewRequestParams{ID: id, Status: "complete", Result: dbtype.JSON(`null`),
		FinishedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}}
	if runErr != nil {
		finish.Status, finish.Error = "failed", problemDetail(runErr)
	} else if finish.Result, err = json.Marshal(result); err != nil {
		return err
	}
	if err := q.FinishReviewRequest(context.WithoutCancel(ctx), finish); err != nil {
		return err
	}
	if runErr != nil {
		a.Service.Progress.Publish(id, Event{Type: "failed", Message: finish.Error})
	} else {
		a.Service.Progress.Publish(id, Event{Type: "done"})
	}
	return nil
}

// EndOrphanRequest ends a review request whose process stopped.
func (a *API) EndOrphanRequest(ctx context.Context, payload []byte) error {
	var p requestJob
	if json.Unmarshal(payload, &p) != nil || p.ID == "" {
		return nil //nolint:nilerr // a payload that names no request leaves nothing to end
	}
	return a.DB.Queries().FinishReviewRequest(ctx, pgdb.FinishReviewRequestParams{ID: uuidOf(p.ID), Status: "failed",
		Result: dbtype.JSON(`null`), Error: StoppedCause, FinishedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}})
}

func (a *API) runRequest(ctx context.Context, row pgdb.ReviewRequest) (any, error) {
	if row.Kind == requestContent {
		var body api.ContentReviewRequest
		if err := json.Unmarshal(row.Request, &body); err != nil {
			return nil, err
		}
		c, stages, err := contentOf(body)
		if err != nil {
			return nil, err
		}
		c.Progress = row.ID
		res, err := a.Service.ReviewContent(ctx, c, stages)
		if err != nil {
			return nil, err
		}
		return a.contentReview(ctx, res, c.Files)
	}
	var body api.UrlReviewRequest
	if err := json.Unmarshal(row.Request, &body); err != nil {
		return nil, err
	}
	var stages Stages
	if body.Stages != nil {
		stages = Stages{}
		for _, st := range *body.Stages {
			stages = append(stages, string(st))
		}
	}
	a.Service.Progress.Publish(row.ID, Event{Type: "stage", Stage: "reviewing"})
	res, err := a.Service.ReviewURL(ctx, body.Url, stages)
	if err != nil {
		return nil, err
	}
	return a.URLReviewOut(ctx, res)
}

// problemDetail is the text of an error as a person reads it.
func problemDetail(err error) string {
	if ke, ok := kernel.AsError(err); ok {
		return ke.Detail
	}
	return err.Error()
}
