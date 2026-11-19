// Package prcheck is the pull request review of a hosted Speccy's GitHub App
// (docs/specs/hosted-pr-review.md). A webhook queues a review of the head commit in the pr
// queue. The job reviews the spec docs that the pull request changes, with the workspace
// profiles and models, and posts the summary comment, the inline comments and one check per
// bundle as the App, with the same code as the Action.
package prcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// JobKind is the job kind of an App review of a pull request.
const JobKind = "pr_review"

// Service queues and runs the App's pull request reviews.
type Service struct {
	Reviews *review.Service
	API     *review.API
	Queue   *queue.Queue
	// GitHub returns the App's installation client for a host.
	GitHub func(ctx context.Context, apiURL string) (*github.Client, error)
	// Profiles are the workspace profiles.
	Profiles func() map[string]profile.Versioned
	// BaseURL is SPECCY_BASE_URL, for the report links.
	BaseURL string
}

// Event is a pull request event the App reviews: a push to the pull request, or a comment
// that may hold a reply command.
type Event struct {
	API  string `json:"api"`
	Repo string `json:"repo"`
	Pull int    `json:"pull"`
	// SHA is the head commit the event names. Empty for a comment: the job reads the head.
	SHA string `json:"sha,omitempty"`
}

// Register registers the job on the queue.
func (s *Service) Register() {
	s.Queue.Register(JobKind, queue.PR, 2, s.Handle, nil)
}

// Queue queues a review of the pull request. A push cancels the review of an older head of
// the same pull request: its result can no longer count.
func (s *Service) Enqueue(ctx context.Context, e Event) error {
	if e.SHA != "" {
		jobs, err := s.Queue.Active(ctx)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Kind != JobKind {
				continue
			}
			var old Event
			if json.Unmarshal(j.Payload, &old) == nil && old.Repo == e.Repo && old.Pull == e.Pull && old.SHA != e.SHA {
				if err := s.Queue.Cancel(ctx, j.ID); err != nil {
					return err
				}
			}
		}
	}
	_, err := s.Queue.Insert(ctx, nil, JobKind, e, &queue.Opts{Unique: true})
	return err
}

// Handle runs one review job.
func (s *Service) Handle(ctx context.Context, payload []byte) error {
	var e Event
	if err := json.Unmarshal(payload, &e); err != nil {
		return nil //nolint:nilerr // a payload that does not parse names no pull request
	}
	err := s.review(ctx, e)
	if err != nil && ctx.Err() == nil {
		// A review that cannot run is logged; River does not try it again with no change.
		slog.ErrorContext(ctx, "the App's pull request review failed", "repo", e.Repo, "pull", e.Pull, "err", err)
		return nil
	}
	return err
}

func (s *Service) review(ctx context.Context, e Event) error {
	gh, err := s.GitHub(ctx, e.API)
	if err != nil {
		return err
	}
	pr, err := gh.PullRequest(ctx, e.Repo, e.Pull)
	if err != nil {
		return err
	}
	if e.SHA != "" && pr.HeadSHA != e.SHA {
		return nil // a newer push has its own job
	}
	// The gate keys come from the base branch, so a pull request cannot change its own gate.
	// A pull request that adds .speccy.yaml uses its own.
	baseRaw, onBase, err := gh.FileAt(ctx, e.Repo, pr.BaseRef, source.RepoConfigFile)
	if err != nil {
		return err
	}
	headRaw, onHead, err := gh.FileAt(ctx, e.Repo, pr.HeadSHA, source.RepoConfigFile)
	if err != nil {
		return err
	}
	if !onBase && !onHead {
		return nil // the repo did not opt in
	}
	gate := headRaw
	if onBase {
		gate = baseRaw
	}
	gateCfg, err := source.ParseRepoConfig(gate)
	if err != nil {
		return s.configProblem(ctx, gh, e, pr, err)
	}
	if !gateCfg.ReviewedByApp() {
		return nil
	}

	host := strings.TrimSuffix(strings.TrimSuffix(gh.API, "/api/v3"), "/")
	if host == "" || strings.Contains(host, "api.github.com") {
		host = "https://github.com"
	}
	prURL := fmt.Sprintf("%s/%s/pull/%d", host, e.Repo, e.Pull)
	plan, err := s.Reviews.PlanURL(ctx, prURL)
	if err != nil {
		return err
	}
	if plan.Review.Commit != pr.HeadSHA {
		return nil
	}
	for i := range plan.Items {
		if from := plan.Items[i].Content.From; from != nil {
			from.Config.Enforcement, from.Config.Mode = gateCfg.Enforcement, gateCfg.Mode
			from.Config.Adoption.Relaxed = gateCfg.Adoption.Relaxed
		}
	}
	// With no reviewer model, the review is lint only, as speccy review is.
	var stages review.Stages
	lint := false
	if _, err := s.Reviews.Gateway.Assigned(ctx, model.RoleReviewer); err != nil {
		stages, lint = review.Stages{}, true
	}
	rev := s.Reviews.ReviewPlan(ctx, plan, stages, make(chan struct{}, review.URLParallel))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	out, err := s.API.URLReviewOut(ctx, rev)
	if err != nil {
		return err
	}
	checks := func(key string) (profile.Profile, bool) {
		p, ok := s.Profiles()[key]
		return p.Profile, ok
	}
	bundles, err := action.BundlesOf(ctx, gh, &out, checks, lint)
	if err != nil {
		return err
	}
	for i, d := range out.Docs {
		if d.Review != nil && d.Review.ReportPath != "" && s.BaseURL != "" {
			bundles[i].Report = s.BaseURL + d.Review.ReportPath
		}
	}
	files, err := gh.PRFiles(ctx, e.Repo, e.Pull)
	if err != nil {
		return err
	}
	o := action.Options{GitHub: gh, Repo: e.Repo, PR: e.Pull, HeadSHA: pr.HeadSHA, HeadRef: pr.HeadRef, BaseRef: pr.BaseRef,
		Fork: pr.Fork(e.Repo), Blocking: gateCfg.Enforcement == "blocking", Relaxed: gateCfg.Adoption.Relaxed, Config: headRaw}
	action.PROptions(&o, out.Config, nil, "")
	decisions := map[string]source.Decisions{}
	for _, it := range plan.Items {
		if from := it.Content.From; from != nil {
			decisions[path.Join(from.Dir, it.Content.MainDoc)] = from.Decisions
		}
	}
	o.Sidecar = func(doc string) (source.Decisions, error) {
		if d, ok := decisions[doc]; ok {
			return d, nil
		}
		raw, ok, err := gh.FileAt(ctx, e.Repo, pr.HeadSHA, source.SidecarPath(doc))
		if err != nil || !ok {
			return source.Decisions{}, err
		}
		return source.ParseDecisions(raw)
	}
	if ok, err := gh.Exists(ctx, e.Repo, pr.HeadSHA, ".speccy/profiles"); err == nil && ok {
		o.Notes = append(o.Notes, "This repo has a .speccy/profiles folder. A hosted Speccy reviews with the workspace profiles; change them in Admin → Profiles.")
	}
	res := action.Run(ctx, o, bundles, files)
	for _, w := range res.Warnings {
		slog.WarnContext(ctx, "the App's pull request review", "repo", e.Repo, "pull", e.Pull, "warning", w)
	}
	return nil
}

// configProblem sets one failed check that says why: a .speccy.yaml that does not parse is
// the author's to fix, as the Action's failed job says.
func (s *Service) configProblem(ctx context.Context, gh *github.Client, e Event, pr github.PRInfo, err error) error {
	return gh.CreateCheckRun(ctx, e.Repo, github.CheckRun{Name: "speccy", HeadSHA: pr.HeadSHA, Conclusion: "failure",
		Title: source.RepoConfigFile + " is not valid", Summary: err.Error()})
}
