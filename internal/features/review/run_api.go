package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

func (a *API) bundle(ctx context.Context, id [16]byte) (pgdb.Bundle, error) {
	b, err := a.DB.Queries().GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return b, kernel.NotFound("bundle_not_found", "No bundle has the ID %s.", fmt.Sprintf("%x", id))
	}
	return b, err
}

// StartRun queues a full review.
func (a *API) StartRun(ctx context.Context, req api.StartRunRequestObject) (api.StartRunResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	run, err := a.Service.StartRun(ctx, b)
	if err != nil {
		return nil, err
	}
	out, err := a.toAPI(ctx, b, run)
	if err != nil {
		return nil, err
	}
	return api.StartRun202JSONResponse(out), nil
}

// EstimateRun estimates a full review's tokens and cost (REQ-104).
func (a *API) EstimateRun(ctx context.Context, req api.EstimateRunRequestObject) (api.EstimateRunResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	est, err := a.Service.EstimateRun(ctx, b)
	if err != nil {
		return nil, err
	}
	out := api.RunEstimate{Calls: est.Calls, CachedSteps: est.CachedHits, TokensIn: est.TokensIn, TokensOut: est.TokensOut, Priced: est.Priced}
	if est.Priced {
		c := float32(est.CostUSD)
		out.CostUsd = &c
	}
	return api.EstimateRun200JSONResponse(out), nil
}

// ListAssumptions lists the current main doc's assumption sentences (REQ-033).
func (a *API) ListAssumptions(ctx context.Context, req api.ListAssumptionsRequestObject) (api.ListAssumptionsResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	out := api.ListAssumptions200JSONResponse{Items: []api.Anchor{}}
	if !b.CurrentVersionID.Valid {
		return out, nil
	}
	files, err := version.Files(ctx, a.DB.Queries(), b.CurrentVersionID.UUID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Path != b.MainDoc {
			continue
		}
		doc := section.Parse(f.Content)
		for _, r := range Assumptions(f.Content) {
			out.Items = append(out.Items, anchorAPI(anchor.New(f.Path, f.Content, doc, r[0], r[1])))
		}
	}
	return out, nil
}

func anchorAPI(an anchor.Anchor) api.Anchor {
	path := an.HeadingPath
	if path == nil {
		path = []string{}
	}
	return api.Anchor{File: an.File, HeadingPath: path, Quote: an.Quote, Prefix: an.Prefix, Suffix: an.Suffix, Start: an.Start, End: an.End}
}

// ListClaims lists a run's claims (REQ-031).
func (a *API) ListClaims(ctx context.Context, req api.ListClaimsRequestObject) (api.ListClaimsResponseObject, error) {
	run, _, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Queries().ListClaims(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := api.ListClaims200JSONResponse{Items: []api.Claim{}}
	for _, c := range rows {
		var an anchor.Anchor
		_ = json.Unmarshal(c.Anchor, &an)
		sources := []string{}
		_ = json.Unmarshal(c.Sources, &sources)
		out.Items = append(out.Items, api.Claim{Id: c.ID, Text: c.Text, Label: api.ClaimLabel(c.Label), Reason: c.Reason, Sources: sources, Anchor: anchorAPI(an)})
	}
	return out, nil
}

// RunEvents streams a run's progress as server-sent events (REQ-026).
func (a *API) RunEvents(ctx context.Context, req api.RunEventsRequestObject) (api.RunEventsResponseObject, error) {
	run, _, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	return eventStream{ctx: ctx, run: run, broker: a.Service.Progress}, nil
}

type eventStream struct {
	ctx    context.Context
	run    pgdb.ReviewRun
	broker *Broker
}

func (e eventStream) VisitRunEventsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(ev Event) error {
		b, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	// A run that ended before this request (or before a restart) sends its final state.
	switch e.run.Status {
	case "complete":
		return send(Event{Type: "done", Stage: e.run.Stage})
	case "failed":
		return send(Event{Type: "failed", Stage: e.run.Stage, Message: e.run.Error})
	}
	history, next, cancel := e.broker.Subscribe(e.run.ID)
	defer cancel()
	for _, ev := range history {
		if err := send(ev); err != nil {
			return nil
		}
	}
	if len(history) == 0 {
		if err := send(Event{Type: "stage", Stage: e.run.Stage}); err != nil {
			return nil
		}
	}
	for {
		select {
		case <-e.ctx.Done():
			return nil
		case ev, ok := <-next:
			if !ok {
				return nil
			}
			if err := send(ev); err != nil {
				return nil
			}
		}
	}
}
