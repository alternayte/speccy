package main

import (
	"context"
	"fmt"
	"io"

	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/source/local"
)

// runActionVerify is `speccy action --verify`: it verifies the build at the pull request's
// head commit against each bundle the pull request changes, posts one summary comment with
// the trace ID table, and comments inline on each breached target the diff holds.
func runActionVerify(ctx context.Context, fl reviewFlags, gh *github.Client, repo string, isPR bool,
	pr int, headSHA string, selected []local.Bundle, files []github.PRFile, cfg source.RepoConfig,
	enforcement, dir string, stdout, stderr io.Writer) int {

	if !isPR {
		fmt.Fprintln(stderr, "speccy action --verify runs on a pull request, because it comments on the code it verified.")
		return exitUsage
	}
	s, err := openSession(ctx, dir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action --verify: %v.\n", problemText(err))
		return exitRun
	}
	bundles, err := listAll(ctx, s.client)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action --verify: %v.\n", problemText(err))
		return exitRun
	}
	bySlug := map[string]api.Bundle{}
	for _, b := range bundles {
		bySlug[b.Slug] = b
	}

	var runs []action.VerifyBundle
	for _, b := range selected {
		sum, ok := bySlug[b.Slug]
		if !ok {
			continue
		}
		v := action.VerifyBundle{Slug: b.Slug}
		run, err := api.StartVerification(ctx, s.client, sum.Id, api.RunVerificationJSONRequestBody{
			Repo: &repo, Sha: &headSHA})
		if err != nil {
			v.Error = "The verification failed: " + err.Error()
		} else {
			v.Run = run
		}
		runs = append(runs, v)
	}

	o := action.Options{GitHub: gh, Repo: repo, PR: pr, HeadSHA: headSHA,
		InlineLimit: cfg.PR.InlineLimit, Blocking: enforcement == "blocking"}
	res := action.RunVerify(ctx, o, runs, files)
	fmt.Fprint(stdout, res.Summary)
	for _, w := range res.Warnings {
		fmt.Fprintln(stderr, w)
	}
	if res.Failed {
		return exitNotReady
	}
	return exitOK
}
