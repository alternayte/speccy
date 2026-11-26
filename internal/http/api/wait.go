package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// WaitReview polls a queued review from POST /reviews or POST /reviews/url until it is complete
// or failed (docs/specs/hosted-pr-review.md). stage, when set, hears each new stage.
func WaitReview(ctx context.Context, c *ClientWithResponses, id openapi_types.UUID, every time.Duration, stage func(string)) (*ReviewRequest, error) {
	last := ""
	for {
		res, err := c.GetReviewRequestWithResponse(ctx, id)
		if err != nil {
			return nil, err
		}
		r := res.JSON200
		if r == nil {
			if p := res.ApplicationproblemJSONDefault; p != nil && p.Detail != nil {
				return nil, errors.New(*p.Detail)
			}
			return nil, fmt.Errorf("the Speccy API answered with status %d", res.StatusCode())
		}
		switch r.Status {
		case ReviewRequestStatusComplete:
			return r, nil
		case ReviewRequestStatusFailed:
			msg := "The review failed."
			if r.Error != nil {
				msg = *r.Error
			}
			return r, errors.New(msg)
		}
		now := string(r.Status)
		if r.Stage != nil {
			now = *r.Stage
		}
		if stage != nil && now != last {
			last = now
			stage(now)
		}
		t := time.NewTimer(every)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// WaitProposal polls the newest breakdown proposal of a doc until it is complete or failed
// (docs/specs/breakdown-proposal-job.md). A failed proposal is an error with its reason.
func WaitProposal(ctx context.Context, c *ClientWithResponses, docID openapi_types.UUID, every time.Duration) (*BreakdownProposal, error) {
	for {
		res, err := c.GetBreakdownProposalWithResponse(ctx, docID)
		if err != nil {
			return nil, err
		}
		p := res.JSON200
		if p == nil {
			if pr := res.ApplicationproblemJSONDefault; pr != nil && pr.Detail != nil {
				return nil, errors.New(*pr.Detail)
			}
			return nil, fmt.Errorf("the Speccy API answered with status %d", res.StatusCode())
		}
		switch p.Status {
		case BreakdownProposalStatusComplete:
			return p, nil
		case BreakdownProposalStatusFailed:
			msg := "The breakdown proposal failed."
			if p.Error != nil {
				msg = *p.Error
			}
			return p, errors.New(msg)
		}
		t := time.NewTimer(every)
		select {
		case <-ctx.Done():
			t.Stop()
			return p, ctx.Err()
		case <-t.C:
		}
	}
}
