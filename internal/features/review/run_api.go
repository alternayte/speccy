package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/divergence"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/sourcepolicy"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

func (a *API) bundle(ctx context.Context, id [16]byte) (pgdb.SpecDoc, error) {
	b, err := a.DB.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: id})
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
	var stages Stages
	if req.Body != nil && req.Body.Stages != nil {
		stages = Stages{}
		for _, st := range *req.Body.Stages {
			stages = append(stages, string(st))
		}
	}
	run, err := a.Service.StartRun(ctx, b, stages)
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
		if f.Path != b.DocPath {
			continue
		}
		doc := section.Parse(f.Content)
		for _, r := range Assumptions(f.Content) {
			out.Items = append(out.Items, anchorAPI(anchor.New(f.Path, f.Content, doc, r[0], r[1])))
		}
	}
	return out, nil
}

// claimSourcesAPI returns the sources of a claim, with what the resolver learned about each.
func claimSourcesAPI(in []sourcepolicy.Source) []api.ClaimSource {
	out := make([]api.ClaimSource, 0, len(in))
	for _, s := range in {
		c := api.ClaimSource{Url: s.URL}
		if s.FinalURL != "" {
			c.FinalUrl = &s.FinalURL
		}
		if len(s.Chain) > 0 {
			chain := s.Chain
			c.Chain = &chain
		}
		if s.Status != 0 {
			st := s.Status
			c.Status = &st
		}
		c.RetrievedAt, c.Modified = s.RetrievedAt, s.Modified
		if s.Tier != "" {
			t := api.ClaimSourceTier(s.Tier)
			c.Tier = &t
		}
		if s.Dropped {
			d := true
			c.Dropped = &d
		}
		if s.Reason != "" {
			r := s.Reason
			c.Reason = &r
		}
		out = append(out, c)
	}
	return out
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
		sources := []sourcepolicy.Source{}
		_ = json.Unmarshal(c.Sources, &sources)
		out.Items = append(out.Items, api.Claim{Id: c.ID, Text: c.Text, Label: api.ClaimLabel(c.Label), Reason: c.Reason,
			Class: c.Class, Sources: claimSourcesAPI(sources), Anchor: anchorAPI(an)})
	}
	return out, nil
}

// ListQuestions lists a run's build questions with each reader's answer and the result
// (REQ-040 to REQ-046). Readers are named by number only (DEC-013).
func (a *API) ListQuestions(ctx context.Context, req api.ListQuestionsRequestObject) (api.ListQuestionsResponseObject, error) {
	run, _, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	q := a.DB.Queries()
	results, err := q.ListQuestionResults(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := api.ListQuestions200JSONResponse{Items: []api.BuildQuestion{}}
	if len(results) == 0 {
		return out, nil
	}
	byQuestion := map[[16]byte]pgdb.QuestionResult{}
	for _, r := range results {
		byQuestion[r.QuestionID] = r
	}
	answers, err := q.ListAnswers(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	questions, err := q.ListQuestions(ctx, run.VersionID)
	if err != nil {
		return nil, err
	}
	for _, qu := range questions {
		r, ok := byQuestion[qu.ID]
		if !ok {
			continue
		}
		item := api.BuildQuestion{Id: qu.ID, Number: int(qu.Number), Text: qu.Text, Level: api.BuildQuestionLevel(qu.Level),
			Result: api.BuildQuestionResult(r.Result), Cites: []api.Cite{}, Answers: []api.ReaderAnswer{}, Groups: [][]int{}}
		var cites []cite
		_ = json.Unmarshal(qu.Cites, &cites)
		for _, c := range cites {
			ac := api.Cite{Kind: api.CiteKind(c.Kind)}
			if c.Kind == "trace" {
				ac.Id = &c.ID
			} else {
				ac.Path = &c.Path
			}
			item.Cites = append(item.Cites, ac)
		}
		var an anchor.Anchor
		_ = json.Unmarshal(qu.Anchor, &an)
		item.Anchor = anchorAPI(an)
		_ = json.Unmarshal(r.Groups, &item.Groups)
		for _, ans := range answers {
			if ans.QuestionID != qu.ID {
				continue
			}
			quotes := []api.QuoteCheck{}
			_ = json.Unmarshal(ans.Quotes, &quotes)
			item.Answers = append(item.Answers, api.ReaderAnswer{Reader: readerNumber(ans.ReaderRole), Answer: ans.Answer, Answered: ans.QuotesFound && !divergence.IsNotSpecified(ans.Answer), Quotes: quotes})
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// readerNumber is 1 for reader_1, and so on.
func readerNumber(role string) int {
	for i, r := range readerRoles(3) {
		if r == role {
			return i + 1
		}
	}
	return 0
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
	var final *Event
	switch e.run.Status {
	case "complete":
		final = &Event{Type: "done", Stage: e.run.Stage}
	case "failed":
		final = &Event{Type: "failed", Stage: e.run.Stage, Message: e.run.Error}
	}
	return WriteEvents(e.ctx, w, e.broker, e.run.ID, final, Event{Type: "stage", Stage: e.run.Stage})
}

// WriteEvents streams one run's events as server-sent events. A run that ended before this
// request, or before a restart, sends final alone. A listener that joins before any event gets
// first, so it has a state to show.
func WriteEvents(ctx context.Context, w http.ResponseWriter, broker *Broker, id uuid.UUID, final *Event, first Event) error {
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
	if final != nil {
		return send(*final)
	}
	history, next, cancel := broker.Subscribe(id)
	defer cancel()
	for _, ev := range history {
		if err := send(ev); err != nil {
			return nil
		}
	}
	if len(history) == 0 {
		if err := send(first); err != nil {
			return nil
		}
	}
	for {
		select {
		case <-ctx.Done():
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
