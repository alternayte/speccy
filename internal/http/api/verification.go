package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// NotVerified reports whether a finished run's verdict is Not Verified.
func (v Verification) NotVerified() bool {
	return v.Verdict != nil && *v.Verdict == VerificationVerdictNotVerified
}

// StartVerification queues a verification run and waits until it ends. A run is a job on the
// server, so a terminal, an agent and the Action read it until it is done or failed. A failed
// run returns its cause as the error.
func StartVerification(ctx context.Context, c *ClientWithResponses, bundle openapi_types.UUID, body RunVerificationJSONRequestBody) (Verification, error) {
	res, err := c.RunVerificationWithResponse(ctx, bundle, body)
	if err != nil {
		return Verification{}, err
	}
	if res.JSON202 == nil {
		return Verification{}, errors.New(problemText(res.ApplicationproblemJSONDefault, res.Status()))
	}
	id := res.JSON202.Id
	for {
		got, err := c.GetVerificationWithResponse(ctx, id)
		if err != nil {
			return Verification{}, err
		}
		if got.JSON200 == nil {
			return Verification{}, errors.New(problemText(got.ApplicationproblemJSONDefault, got.Status()))
		}
		v := *got.JSON200
		switch v.Status {
		case VerificationStatusDone:
			return v, nil
		case VerificationStatusFailed:
			msg := "the verification failed"
			if v.Error != nil && *v.Error != "" {
				msg = *v.Error
			}
			return v, errors.New(msg)
		}
		select {
		case <-ctx.Done():
			return Verification{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func problemText(p *Problem, status string) string {
	if p == nil {
		return fmt.Sprintf("the server answered %s", status)
	}
	if p.Detail != nil && *p.Detail != "" {
		return *p.Detail
	}
	return p.Title
}
