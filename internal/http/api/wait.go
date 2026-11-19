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
