package prreview

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// Batch statuses and item states, as the store and the API name them.
const (
	statusPlanned   = "planned"
	statusRunning   = "running"
	statusDone      = "done"
	statusCancelled = "cancelled"
	statusStopped   = "stopped"

	stateWaiting   = "waiting"
	stateReviewing = "reviewing"
	statePosted    = "posted"
	stateSkipped   = "skipped"
	stateFailed    = "failed"
)

// StoppedReason is the reason of each pull request that a batch had not finished when the
// process that ran it stopped.
const StoppedReason = "Speccy stopped before this pull request finished. Start the batch again."

// batchConfig is what a batch keeps of its request, in the stages column.
type batchConfig struct {
	// Stages is nil for every stage, and empty for lint only.
	Stages review.Stages `json:"stages"`
	Lint   bool          `json:"lint"`
	// Levels and Attribution are the request's, and win over the .speccy.yaml of each review.
	// Empty means the .speccy.yaml decides.
	Levels      []string `json:"levels,omitempty"`
	Attribution string   `json:"attribution,omitempty"`
}

// CreatePrBatch is POST /pr-batches.
func (a *API) CreatePrBatch(ctx context.Context, req api.CreatePrBatchRequestObject) (api.CreatePrBatchResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	in := req.Body
	urls := deref(in.Urls)
	repo := deref(in.Repo)
	if (len(urls) == 0) == (repo == "") {
		return nil, kernel.Invalid("bad_batch", "Name the pull requests by their URLs, or name one repo.")
	}
	if deref(in.Requested) && repo == "" {
		return nil, kernel.Invalid("bad_batch", "requested works with a repo only.")
	}
	parallel := DefaultParallel
	if in.Parallel != nil {
		parallel = *in.Parallel
	}
	if parallel < 1 || parallel > MaxParallel {
		return nil, kernel.Invalid("bad_parallel", "parallel is from 1 to %d.", MaxParallel)
	}
	cfg := batchConfig{}
	if in.Levels != nil {
		var names []string
		for _, l := range *in.Levels {
			names = append(names, string(l))
		}
		levels, err := source.ParseLevels(strings.Join(names, ","))
		if err != nil {
			return nil, kernel.Invalid("bad_levels", "levels %s.", err.Error())
		}
		cfg.Levels = levels
	}
	if in.Attribution != nil {
		switch a := string(*in.Attribution); a {
		case source.AttributionSpeccy, source.AttributionNone:
			cfg.Attribution = a
		default:
			return nil, kernel.Invalid("bad_attribution", "attribution %q is not speccy or none.", a)
		}
	}
	if in.Stages != nil {
		cfg.Stages = review.Stages{}
		for _, st := range *in.Stages {
			cfg.Stages = append(cfg.Stages, string(st))
		}
	}
	// With no reviewer model, the review is lint only, as speccy review is.
	if _, err := a.Gateway.Assigned(ctx, model.RoleReviewer); err != nil || (cfg.Stages != nil && len(cfg.Stages) == 0) {
		cfg.Stages, cfg.Lint = review.Stages{}, true
	}

	prs, source, err := a.pick(ctx, urls, repo, deref(in.Requested))
	if err != nil {
		return nil, err
	}
	id := kernel.NewID()
	est := api.PrBatchEstimate{LintOnly: cfg.Lint}
	plans := map[int]review.URLPlan{}
	var items []pgdb.InsertPrBatchItemParams
	for _, p := range prs {
		item := pgdb.InsertPrBatchItemParams{BatchID: id, Position: int64(len(items)), Repo: p.build.Repo, Pull: int64(p.build.Pull),
			Url: p.url, State: stateWaiting, Result: dbtype.JSON("[]"), UpdatedAt: now()}
		gh, err := a.client(ctx, p.build)
		if err != nil {
			return nil, err
		}
		info, err := gh.PullRequest(ctx, p.build.Repo, p.build.Pull)
		if err != nil {
			item.State, item.Reason = stateFailed, detail(github.UnreadableRepo(p.build.Repo, err))
			items = append(items, item)
			continue
		}
		item.HeadSha = info.HeadSHA
		if !deref(in.Again) {
			n, err := a.DB.Queries().HasPrReview(ctx, pgdb.HasPrReviewParams{WorkspaceID: a.Workspace, Repo: p.build.Repo,
				Pull: int64(p.build.Pull), HeadSha: info.HeadSHA})
			if err != nil {
				return nil, err
			}
			if n > 0 {
				item.State, item.Reason = stateSkipped, fmt.Sprintf("Already reviewed at %s.", short(info.HeadSHA))
				items = append(items, item)
				continue
			}
		}
		plan, err := a.Reviews.PlanURL(ctx, p.url)
		switch {
		case noSpecDoc(err) && repo != "":
			continue // a repo batch takes only the pull requests that change a spec doc
		case noSpecDoc(err):
			item.State, item.Reason = stateSkipped, "It changes no spec doc."
		case err != nil:
			item.State, item.Reason = stateFailed, detail(err)
		default:
			plans[len(items)] = plan
			est.Pulls++
			est.Docs += len(plan.Items)
			if !cfg.Lint {
				e, err := a.Reviews.EstimatePlan(ctx, plan, cfg.Stages)
				if err != nil {
					return nil, err
				}
				est.Calls += e.Calls
				est.TokensIn += e.TokensIn
				est.TokensOut += e.TokensOut
				est.CostUsd += float32(e.CostUSD)
				est.Priced = est.Priced || e.Priced
			}
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, kernel.Invalid("no_pull_request", "%s has no open pull request that changes a spec doc.", source)
	}
	stages, _ := json.Marshal(cfg)
	estimate, _ := json.Marshal(est)
	start := in.Start == nil || *in.Start
	status := statusPlanned
	if start {
		status = statusRunning
	}
	q := a.DB.Queries()
	if err := q.InsertPrBatch(ctx, pgdb.InsertPrBatchParams{ID: id, WorkspaceID: a.Workspace, Status: status, Parallel: int64(parallel),
		Stages: dbtype.JSON(stages), Again: deref(in.Again), Source: source, Estimate: dbtype.JSON(estimate), CreatedAt: now()}); err != nil {
		return nil, err
	}
	for _, it := range items {
		if err := q.InsertPrBatchItem(ctx, it); err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	if a.plans == nil {
		a.plans = map[uuid.UUID]map[int]review.URLPlan{}
	}
	a.plans[id] = plans
	a.mu.Unlock()
	if start {
		a.launch(id)
	}
	out, err := a.batch(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.CreatePrBatch200JSONResponse(out), nil
}

// pick finds the pull requests that the URLs or the repo name, and says in words what the
// batch took.
func (a *API) pick(ctx context.Context, urls []string, repo string, requested bool) ([]pr, string, error) {
	if repo == "" {
		var out []pr
		seen := map[string]bool{}
		for _, u := range urls {
			p, err := parsePR(u)
			if err != nil {
				return nil, "", err
			}
			if !seen[p.url] {
				seen[p.url] = true
				out = append(out, p)
			}
		}
		return out, fmt.Sprintf("%d pull request%s", len(out), plural(len(out))), nil
	}
	b, err := parseRepo(repo)
	if err != nil {
		return nil, "", err
	}
	gh, err := a.client(ctx, b)
	if err != nil {
		return nil, "", err
	}
	var numbers []int
	if requested {
		if numbers, err = gh.RequestedPulls(ctx, b.Repo); err != nil {
			return nil, "", github.UnreadableRepo(b.Repo, err)
		}
	} else {
		pulls, err := gh.OpenPulls(ctx, b.Repo)
		if err != nil {
			return nil, "", github.UnreadableRepo(b.Repo, err)
		}
		for _, p := range pulls {
			if !p.Draft {
				numbers = append(numbers, p.Number)
			}
		}
	}
	out := make([]pr, 0, len(numbers))
	for _, n := range numbers {
		pb := b
		pb.Pull = n
		out = append(out, pr{build: pb, url: prURL(pb)})
	}
	source := b.Repo
	if requested {
		source += ", where your review is requested"
	}
	return out, source, nil
}

// GetPrBatch is GET /pr-batches/{batchId}.
func (a *API) GetPrBatch(ctx context.Context, req api.GetPrBatchRequestObject) (api.GetPrBatchResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	out, err := a.batch(ctx, req.BatchId)
	if err != nil {
		return nil, err
	}
	return api.GetPrBatch200JSONResponse(out), nil
}

// StartPrBatch is POST /pr-batches/{batchId}/start.
func (a *API) StartPrBatch(ctx context.Context, req api.StartPrBatchRequestObject) (api.StartPrBatchResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	b, err := a.DB.Queries().GetPrBatch(ctx, pgdb.GetPrBatchParams{WorkspaceID: a.Workspace, ID: req.BatchId})
	if err != nil {
		return nil, notFound(err)
	}
	if b.Status != statusPlanned {
		return nil, kernel.Conflict("not_planned", "The batch is %s, so it cannot start.", b.Status)
	}
	if err := a.DB.Queries().SetPrBatchStatus(ctx, pgdb.SetPrBatchStatusParams{WorkspaceID: a.Workspace, ID: b.ID, Status: statusRunning}); err != nil {
		return nil, err
	}
	a.launch(b.ID)
	out, err := a.batch(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	return api.StartPrBatch200JSONResponse(out), nil
}

// CancelPrBatch is POST /pr-batches/{batchId}/cancel. No new pull request starts; the reviews
// that run now finish and post.
func (a *API) CancelPrBatch(ctx context.Context, req api.CancelPrBatchRequestObject) (api.CancelPrBatchResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	b, err := a.DB.Queries().GetPrBatch(ctx, pgdb.GetPrBatchParams{WorkspaceID: a.Workspace, ID: req.BatchId})
	if err != nil {
		return nil, notFound(err)
	}
	a.mu.Lock()
	r := a.running[b.ID]
	a.mu.Unlock()
	switch {
	case r != nil:
		r.mu.Lock()
		r.cancelled = true
		r.mu.Unlock()
	case b.Status == statusPlanned:
		a.mu.Lock()
		delete(a.plans, b.ID)
		a.mu.Unlock()
		if err := a.finish(ctx, b.ID, statusCancelled, "The batch was cancelled before it started."); err != nil {
			return nil, err
		}
	}
	out, err := a.batch(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	return api.CancelPrBatch200JSONResponse(out), nil
}

// launch runs a batch on the life of the process.
func (a *API) launch(id uuid.UUID) {
	r := &run{}
	a.mu.Lock()
	if a.running == nil {
		a.running = map[uuid.UUID]*run{}
	}
	a.running[id] = r
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.running, id)
			delete(a.plans, id)
			a.mu.Unlock()
		}()
		if err := a.execute(a.Life, id, r); err != nil && a.Life.Err() == nil {
			slog.Error("pull request batch failed", "batch", id, "err", err)
		}
	}()
}

// execute reviews the waiting pull requests of a batch, parallel at a time. The spec docs of
// all its pull requests share parallel slots too.
func (a *API) execute(ctx context.Context, id uuid.UUID, r *run) error {
	q := a.DB.Queries()
	b, err := q.GetPrBatch(ctx, pgdb.GetPrBatchParams{WorkspaceID: a.Workspace, ID: id})
	if err != nil {
		return err
	}
	var cfg batchConfig
	if err := json.Unmarshal(b.Stages, &cfg); err != nil {
		return err
	}
	items, err := q.ListPrBatchItems(ctx, id)
	if err != nil {
		return err
	}
	a.mu.Lock()
	plans := a.plans[id]
	a.mu.Unlock()
	prs := make(chan struct{}, b.Parallel)
	slots := make(chan struct{}, b.Parallel)
	var wg sync.WaitGroup
	for i, it := range items {
		if it.State != stateWaiting {
			continue
		}
		select {
		case prs <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		r.mu.Lock()
		cancelled := r.cancelled
		r.mu.Unlock()
		if cancelled {
			<-prs
			break
		}
		if !cfg.Lint {
			if spent, err := a.budgetSpent(ctx); err != nil || spent {
				<-prs
				reason := "The monthly token budget is spent."
				if err != nil {
					reason = detail(err)
				}
				a.skipRest(ctx, items[i:], reason)
				break
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-prs }()
			a.reviewItem(ctx, b, cfg, it, plans, slots)
		}()
	}
	wg.Wait()
	status, reason := statusDone, ""
	r.mu.Lock()
	if r.cancelled {
		status, reason = statusCancelled, "The batch was cancelled before this pull request started."
	}
	r.mu.Unlock()
	return a.finish(ctx, id, status, reason)
}

// finish sets the end status of a batch, and gives each pull request that did not start the
// reason.
func (a *API) finish(ctx context.Context, id uuid.UUID, status, reason string) error {
	q := a.DB.Queries()
	if reason != "" {
		if err := q.StopPrBatchItems(ctx, pgdb.StopPrBatchItemsParams{BatchID: id, State: stateSkipped, Reason: reason, UpdatedAt: now()}); err != nil {
			return err
		}
	}
	return q.SetPrBatchStatus(ctx, pgdb.SetPrBatchStatusParams{WorkspaceID: a.Workspace, ID: id, Status: status,
		FinishedAt: nullTime(now())})
}

func (a *API) skipRest(ctx context.Context, items []pgdb.PrBatchItem, reason string) {
	for _, it := range items {
		if it.State != stateWaiting {
			continue
		}
		it.State, it.Reason = stateSkipped, reason
		a.update(ctx, it)
	}
}

func (a *API) budgetSpent(ctx context.Context) (bool, error) {
	b, err := a.Gateway.Budget(ctx)
	if err != nil {
		return false, err
	}
	return b.TokenLimit.Valid && b.TokensUsed >= b.TokenLimit.Int64, nil
}

func (a *API) update(ctx context.Context, it pgdb.PrBatchItem) {
	err := a.DB.Queries().UpdatePrBatchItem(ctx, pgdb.UpdatePrBatchItemParams{BatchID: it.BatchID, Position: it.Position, State: it.State,
		Reason: it.Reason, HeadSha: it.HeadSha, Result: it.Result, Comments: it.Comments, Removed: it.Removed, ReviewUrl: it.ReviewUrl,
		Config: it.Config, UpdatedAt: now()})
	if err != nil {
		slog.Error("update of a batch pull request failed", "batch", it.BatchID, "pull", it.Pull, "err", err)
	}
}

// reviewItem reviews one pull request and posts the findings into the reviewer's pending
// review on it.
func (a *API) reviewItem(ctx context.Context, b pgdb.PrBatch, cfg batchConfig, it pgdb.PrBatchItem, plans map[int]review.URLPlan, slots chan struct{}) {
	it.State = stateReviewing
	a.update(ctx, it)
	fail := func(err error) {
		it.State, it.Reason = stateFailed, detail(err)
		a.update(ctx, it)
	}
	p, err := parsePR(it.Url)
	if err != nil {
		fail(err)
		return
	}
	plan, ok := plans[int(it.Position)]
	if !ok {
		if plan, err = a.Reviews.PlanURL(ctx, it.Url); err != nil {
			if noSpecDoc(err) {
				it.State, it.Reason = stateSkipped, "It changes no spec doc."
				a.update(ctx, it)
				return
			}
			fail(err)
			return
		}
	}
	rev, err := a.ReviewAPI.URLReviewOut(ctx, a.Reviews.ReviewPlan(ctx, plan, cfg.Stages, slots))
	if err != nil {
		fail(err)
		return
	}
	it.HeadSha = rev.Commit
	config, _ := json.Marshal(rev.Config)
	it.Config = dbtype.JSON(config)
	var docs []api.PrBatchDoc
	for _, d := range rev.Docs {
		doc := api.PrBatchDoc{Path: d.Path}
		if d.Review != nil {
			doc.Verdict, doc.Must, doc.Should = string(d.Review.Verdict.Result), d.Review.Verdict.Must, d.Review.Verdict.Should
		} else if d.Error != nil {
			doc.Error = d.Error
		}
		docs = append(docs, doc)
	}
	result, _ := json.Marshal(docs)
	it.Result = dbtype.JSON(result)

	gh, err := a.client(ctx, p.build)
	if err != nil {
		fail(err)
		return
	}
	files, err := gh.PRFiles(ctx, rev.Repo, p.build.Pull)
	if err != nil {
		fail(github.UnreadableRepo(rev.Repo, err))
		return
	}
	bundles, err := action.BundlesOf(ctx, gh, &rev, a.checks, cfg.Lint)
	if err != nil {
		fail(err)
		return
	}
	o := action.Options{GitHub: gh, Repo: rev.Repo, PR: p.build.Pull, HeadSHA: rev.Commit, Prune: cfg.Stages == nil}
	action.PROptions(&o, rev.Config, cfg.Levels, cfg.Attribution)
	pending := action.Pending(o, bundles, files)
	url, n, removed, err := a.post(ctx, gh, pending)
	if err != nil {
		fail(fmt.Errorf("GitHub did not take the pending review: %w", err))
		return
	}
	if err := a.DB.Queries().RecordPrReview(ctx, pgdb.RecordPrReviewParams{WorkspaceID: a.Workspace, Repo: rev.Repo, Pull: int64(p.build.Pull),
		HeadSha: rev.Commit, BatchID: uuid.NullUUID{UUID: b.ID, Valid: true}, ReviewedAt: now()}); err != nil {
		fail(err)
		return
	}
	it.State, it.Reason, it.Comments, it.Removed, it.ReviewUrl = statePosted, "", int64(n), int64(removed), url
	a.update(ctx, it)
}

// post puts a pending review on GitHub: a new one, or what changed for the reviewer's pending
// review. It returns the review's address, the count of comments it added, and the count of
// Speccy's comments it removed because their finding is gone. A review with no attribution
// keeps its marks in the local state, so the next batch finds Speccy's comments in it.
func (a *API) post(ctx context.Context, gh *github.Client, pr action.PendingReview) (url string, added, removed int, err error) {
	existing, err := gh.Pending(ctx, pr.Repo, pr.Pull)
	if err != nil {
		return "", 0, 0, err
	}
	if existing == nil {
		// The marks of an earlier pending review belong to a review that is submitted or gone.
		if err := a.forget(ctx, pr.Repo, pr.Pull); err != nil {
			return "", 0, 0, err
		}
		url, err := gh.CreatePendingReview(ctx, pr.Repo, pr.Pull, pr.CommitID, pr.Body, pr.Comments)
		if err == nil && pr.Plain() {
			err = a.keepNew(ctx, gh, pr.Repo, pr.Pull, func(r *github.PendingReview) action.Marks { return action.Posted(r, pr) })
		}
		return url, len(pr.Comments), 0, err
	}
	marks, err := a.marks(ctx, pr.Repo, pr.Pull)
	if err != nil {
		return existing.URL, 0, 0, err
	}
	var kept action.Marks
	defer func() {
		if kerr := a.keep(ctx, pr.Repo, pr.Pull, kept); err == nil {
			err = kerr
		}
	}()
	add, keys, body, stale := action.Merge(existing, pr, marks)
	for _, c := range stale {
		if err := gh.DeletePendingComment(ctx, c.ID); err != nil {
			return existing.URL, 0, removed, err
		}
		if err := a.unmark(ctx, pr.Repo, pr.Pull, c.ID); err != nil {
			return existing.URL, 0, removed, err
		}
		removed++
	}
	for i, c := range add {
		id, err := gh.AddPendingComment(ctx, existing.ID, c.Path, c.Line, c.Body)
		if err != nil {
			return existing.URL, added, removed, err
		}
		if pr.Plain() {
			kept = append(kept, api.PendingMark{CommentId: id, Kind: api.PendingMarkKindFinding, Key: keys[i]})
		}
		added++
	}
	if body == existing.Body {
		return existing.URL, added, removed, nil
	}
	// GitHub does not let anyone edit a review body that started empty, such as the body of a
	// review the reviewer started from a line in the GitHub page. Speccy's part then goes in
	// one comment on a spec doc of the pull request.
	switch c := action.BodyComment(existing, marks); {
	case strings.TrimSpace(existing.Body) != "":
		err = gh.SetPendingBody(ctx, existing.ID, body)
		if err == nil && pr.Plain() {
			kept = append(kept, api.PendingMark{CommentId: existing.ID, Kind: api.PendingMarkKindReview, Body: pr.Body})
		}
	case c != nil && c.Body != pr.Body:
		err = gh.UpdatePendingComment(ctx, c.ID, pr.Body)
	case c == nil && pr.File != "":
		var id string
		id, err = gh.AddPendingFileComment(ctx, existing.ID, pr.File, pr.Body)
		if pr.Plain() {
			kept = append(kept, api.PendingMark{CommentId: id, Kind: api.PendingMarkKindBody})
		}
	}
	return existing.URL, added, removed, err
}

// EndOrphans ends each batch that a stopped process left planned or running. Only one process
// owns a local state, so such a batch runs nowhere.
func (a *API) EndOrphans(ctx context.Context) error {
	batches, err := a.DB.Queries().ListActivePrBatches(ctx, a.Workspace)
	if err != nil {
		return err
	}
	for _, b := range batches {
		q := a.DB.Queries()
		if err := q.StopPrBatchItems(ctx, pgdb.StopPrBatchItemsParams{BatchID: b.ID, State: stateFailed, Reason: StoppedReason, UpdatedAt: now()}); err != nil {
			return err
		}
		if err := q.SetPrBatchStatus(ctx, pgdb.SetPrBatchStatusParams{WorkspaceID: a.Workspace, ID: b.ID, Status: statusStopped,
			FinishedAt: nullTime(now())}); err != nil {
			return err
		}
	}
	return nil
}

// batch is a batch as the API gives it.
func (a *API) batch(ctx context.Context, id uuid.UUID) (api.PrBatch, error) {
	q := a.DB.Queries()
	b, err := q.GetPrBatch(ctx, pgdb.GetPrBatchParams{WorkspaceID: a.Workspace, ID: id})
	if err != nil {
		return api.PrBatch{}, notFound(err)
	}
	items, err := q.ListPrBatchItems(ctx, id)
	if err != nil {
		return api.PrBatch{}, err
	}
	out := api.PrBatch{Id: b.ID, Status: api.PrBatchStatus(b.Status), Parallel: int(b.Parallel), Source: b.Source, CreatedAt: b.CreatedAt,
		Items: []api.PrBatchItem{}}
	if b.FinishedAt.Valid {
		out.FinishedAt = &b.FinishedAt.Time
	}
	if err := json.Unmarshal(b.Estimate, &out.Estimate); err != nil {
		return out, err
	}
	for _, it := range items {
		item := api.PrBatchItem{Repo: it.Repo, Pull: int(it.Pull), Url: it.Url, State: api.PrBatchItemState(it.State), Reason: it.Reason,
			HeadSha: it.HeadSha, Comments: int(it.Comments), Removed: int(it.Removed), ReviewUrl: it.ReviewUrl, Docs: []api.PrBatchDoc{}}
		if err := json.Unmarshal(it.Result, &item.Docs); err != nil {
			return out, err
		}
		var cfg api.UrlReviewConfig
		if err := json.Unmarshal(it.Config, &cfg); err != nil {
			return out, err
		}
		if cfg.Source != "" {
			item.Config = &cfg
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
