package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

const actionPRUsage = "speccy action --pr <pull request URL> --pending | --dry-run [--stages …] [--levels must,should] [--no-attribution]\n"

// prFlagsAction are the flags that shape the pending review of speccy action --pr.
type prFlagsAction struct {
	levels        string
	levelsSet     bool
	noAttribution bool
}

// runActionPR is speccy action --pr <url> --pending | --dry-run: a reviewer on a laptop reviews
// the spec docs that a pull request changes, with the local state and models, and gets one
// pending review that only that person sees (#91). It makes no check run and no summary
// comment, and it reads no reply command: those belong to the Action in CI.
func runActionPR(prURL string, pending, dryRun bool, fl reviewFlags, pf prFlagsAction, stdout, stderr io.Writer) int {
	const usage = "Usage: " + actionPRUsage
	if pending == dryRun {
		fmt.Fprint(stderr, "speccy action --pr needs one of --pending and --dry-run. --pending posts one review that only you see. --dry-run prints it and posts nothing.\n\n"+usage)
		return exitUsage
	}
	build, err := github.ParseBuildURL(prURL)
	if err != nil || build.Pull == 0 {
		fmt.Fprintf(stderr, "speccy action: %q is not the URL of a pull request.\n\n%s", prURL, usage)
		return exitUsage
	}
	stages, err := review.ParseStages(fl.stages)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %s\n", problemText(err))
		return exitUsage
	}
	var levels []string
	if pf.levelsSet {
		if levels, err = source.ParseLevels(pf.levels); err != nil {
			fmt.Fprintf(stderr, "speccy action: --levels %v.\n\n%s", err, usage)
			return exitUsage
		}
	}
	attribution := ""
	if pf.noAttribution {
		attribution = source.AttributionNone
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		if token, err = github.GHToken(ctx, build.Host); err != nil {
			fmt.Fprintf(stderr, "speccy action: no GitHub token. Set GITHUB_TOKEN, or log in with gh auth login: %v.\n", err)
			return exitUsage
		}
	}
	gh := &github.Client{API: build.APIURL(), Token: token}
	cwd, _ := os.Getwd()
	rootDir := findRoot(cwd)
	// A pending review outlives the command: the local state keeps the marks of a review with
	// no attribution, so a later batch merges into it (#140).
	s, err := openSessionIn(ctx, rootDir, pending)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n", problemText(err))
		return exitRun
	}
	// With no reviewer model, the review is lint only, as speccy review is.
	body := api.ReviewUrlJSONRequestBody{Url: prURL}
	lint := fl.stagesSet && fl.stages == review.StageLint
	if !fl.stagesSet && !roleAssigned(ctx, s.client, api.Reviewer) {
		fmt.Fprintln(stderr, "No model is assigned, so only lint ran. Assign models in the app (Admin → Models), or pass --stages lint to say so.")
		lint = true
	}
	if lint {
		body.Stages = &[]api.UrlReviewRequestStages{}
	} else if stages != nil {
		st := make([]api.UrlReviewRequestStages, len(stages))
		for i, x := range stages {
			st[i] = api.UrlReviewRequestStages(x)
		}
		body.Stages = &st
	}
	res, err := s.client.ReviewUrlWithResponse(ctx, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n", err)
		return exitRun
	}
	if res.JSON200 == nil {
		return problemExit(stderr, prURL, res.ApplicationproblemJSONDefault)
	}
	rev := res.JSON200
	files, err := gh.PRFiles(ctx, rev.Repo, build.Pull)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: the files of the pull request do not read: %v.\n", err)
		return exitRun
	}
	// The inline limit, the levels and the attribution come from the .speccy.yaml that the
	// review used: the repo's at the commit, or the local one (#131). A flag wins.
	o := action.Options{GitHub: gh, Repo: rev.Repo, PR: build.Pull, HeadSHA: rev.Commit}
	action.PROptions(&o, rev.Config, levels, attribution)
	profiles, _ := profile.LoadLocal(filepath.Join(rootDir, ".speccy", "profiles"))
	checks := func(key string) (profile.Profile, bool) {
		p, ok := profiles[key]
		return p.Profile, ok
	}
	bundles, err := action.BundlesOf(ctx, gh, rev, checks, lint)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n", err)
		return exitRun
	}
	pr := action.Pending(o, bundles, files)
	pr.Config = &rev.Config
	if dryRun {
		fmt.Fprintln(stderr, action.ConfigLine(rev.Config, rev.Commit))
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(pr); err != nil {
			fmt.Fprintf(stderr, "speccy action: %v.\n", err)
			return exitRun
		}
		return exitOK
	}
	url, err := gh.CreatePendingReview(ctx, pr.Repo, pr.Pull, pr.CommitID, pr.Body, pr.Comments)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: GitHub did not take the pending review: %v. A person has one pending review on a pull request at a time: submit or discard yours, then run this again.\n", err)
		return exitRun
	}
	fmt.Fprintf(stdout, "Posted one pending review with %d comment%s on %s#%d. Only you see it until you submit it.\n%s\n%s\n",
		len(pr.Comments), pluralS(len(pr.Comments)), pr.Repo, pr.Pull, url, action.ConfigLine(rev.Config, rev.Commit))
	if pr.Plain() {
		if err := keepMarks(ctx, s.client, gh, pr, prURL); err != nil {
			fmt.Fprintf(stderr, "Warning: Speccy did not keep which comments it wrote, so a later speccy review-prs may post them again: %v.\n", err)
		}
	}
	return exitOK
}

// keepMarks keeps the marks of a pending review with no attribution in the local state: the
// review has no hidden marker, so a later batch finds Speccy's comments by their IDs.
func keepMarks(ctx context.Context, c *api.ClientWithResponses, gh *github.Client, pr action.PendingReview, prURL string) error {
	r, err := gh.Pending(ctx, pr.Repo, pr.Pull)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("GitHub shows no pending review on %s#%d", pr.Repo, pr.Pull)
	}
	res, err := c.RecordPendingWithResponse(ctx, api.RecordPendingJSONRequestBody{Url: prURL, Marks: action.Posted(r, pr)})
	if err != nil {
		return err
	}
	if res.StatusCode() != 204 {
		if p := res.ApplicationproblemJSONDefault; p != nil && p.Detail != nil {
			return fmt.Errorf("%s", *p.Detail)
		}
		return fmt.Errorf("the Speccy API answered with status %d", res.StatusCode())
	}
	return nil
}
