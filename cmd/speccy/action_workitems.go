package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source/github"
)

// workItemsEvent is the part of a pull_request or a push event that names work items.
type workItemsEvent struct {
	PullRequest *struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Head   struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	} `json:"pull_request"`
	// After is the head commit of a push.
	After      string `json:"after"`
	Ref        string `json:"ref"`
	Repository struct {
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
}

// runActionWorkItems is `speccy action --work-items`, the verify Action of a code repo (#158).
// It finds the work items a pull request names in its title, its body and its branch, and runs a
// verification scoped to each. On a pull request the run reads the head commit and only
// advises: one summary comment and a check. On a push to the default branch it reads the merge
// commit, and that run counts toward the handoff. It needs a Speccy server: the breakdown lives
// there.
func runActionWorkItems(server string, blocking bool, stdout, stderr io.Writer) int {
	getenv := os.Getenv
	token, repo := getenv("GITHUB_TOKEN"), getenv("GITHUB_REPOSITORY")
	if token == "" || repo == "" || getenv("GITHUB_EVENT_PATH") == "" {
		fmt.Fprintln(stderr, "speccy action --work-items runs in GitHub Actions. It needs GITHUB_TOKEN, GITHUB_REPOSITORY, and GITHUB_EVENT_PATH.")
		return exitUsage
	}
	if server == "" {
		fmt.Fprintln(stderr, "speccy action --work-items needs --server: the work items live on a Speccy server.")
		return exitUsage
	}
	var event workItemsEvent
	raw, err := os.ReadFile(getenv("GITHUB_EVENT_PATH"))
	if err == nil {
		err = json.Unmarshal(raw, &event)
	}
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: the event file does not read: %v.\n", err)
		return exitUsage
	}
	c, err := remoteClient(strings.TrimSuffix(server, "/"))
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n", err)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gh := &github.Client{API: getenv("GITHUB_API_URL"), Token: token}

	var text, sha string
	pr := 0
	switch {
	case event.PullRequest != nil:
		p := event.PullRequest
		pr, sha = p.Number, p.Head.SHA
		text = strings.Join([]string{p.Title, p.Body, p.Head.Ref}, "\n")
	case event.After != "" && event.Ref == "refs/heads/"+event.Repository.DefaultBranch:
		sha = event.After
		pulls, err := gh.CommitPulls(ctx, repo, sha)
		if err != nil {
			fmt.Fprintf(stderr, "speccy action: the pull requests of %s do not read: %v.\n", sha, err)
			return exitRun
		}
		for _, p := range pulls {
			if p.Merged {
				text += strings.Join([]string{p.Title, p.Body, p.HeadRef}, "\n") + "\n"
			}
		}
	default:
		fmt.Fprintln(stdout, "Speccy verifies work items on a pull request and on a push to the default branch. This event is neither.")
		return exitOK
	}

	var runs []action.VerifyBundle
	seen := map[string]bool{}
	for _, ref := range issueRefs(text, repo) {
		res, err := c.GetWorkItemWithResponse(ctx, &api.GetWorkItemParams{Ref: ref})
		if err != nil {
			fmt.Fprintf(stderr, "speccy action: %v.\n", err)
			return exitRun
		}
		if res.JSON200 == nil {
			continue // not a pushed work item
		}
		w := res.JSON200
		name := w.Bundle + "/" + w.Item.Id
		if seen[name] {
			continue
		}
		seen[name] = true
		body := api.RunVerificationJSONRequestBody{WorkItem: &ref}
		if pr > 0 {
			// A pull request target reads its head commit, and the run only advises.
			target := fmt.Sprintf("%s/%s/pull/%d", webBase(getenv("GITHUB_SERVER_URL")), repo, pr)
			body.Target = &target
		} else {
			body.Repo, body.Sha = &repo, &sha
		}
		v := action.VerifyBundle{Slug: name}
		run, err := api.StartVerification(ctx, c, w.DocId, body)
		if err != nil {
			v.Error = "The verification failed: " + err.Error()
		} else {
			v.Run = run
		}
		runs = append(runs, v)
	}
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "This change names no work item that Speccy pushed, so there is nothing to verify.")
		return exitOK
	}
	o := action.Options{GitHub: gh, Repo: repo, PR: pr, HeadSHA: sha, Blocking: blocking}
	var res action.Result
	if pr > 0 {
		var files []github.PRFile
		if files, err = gh.PRFiles(ctx, repo, pr); err != nil {
			fmt.Fprintf(stderr, "speccy action: the files of the pull request do not read: %v.\n", err)
			return exitRun
		}
		res = action.RunVerify(ctx, o, runs, files)
	} else {
		res = action.RunVerifyChecks(ctx, o, runs)
	}
	fmt.Fprint(stdout, res.Summary)
	for _, w := range res.Warnings {
		fmt.Fprintln(stderr, w)
	}
	if res.Failed {
		return exitNotReady
	}
	return exitOK
}

var (
	issueURLRe  = regexp.MustCompile(`https?://[^\s)\]>"']+`)
	issueKeyRe  = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-\d+\b`)
	repoIssueRe = regexp.MustCompile(`\b([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(\d+)\b`)
	hashIssueRe = regexp.MustCompile(`(?:^|[\s(])#(\d+)\b`)
)

// issueRefs are the issue URLs and keys a text names, in the order they appear, each once: a
// URL, a Linear or Jira key such as ENG-123, owner/repo#12, and #12 in repo. A branch name
// such as eng-123-add-login names ENG-123 in lower case, so the keys are also read upper-cased.
func issueRefs(text, repo string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, u := range issueURLRe.FindAllString(text, -1) {
		add(strings.TrimRight(u, ".,;:"))
	}
	for _, k := range issueKeyRe.FindAllString(text, -1) {
		add(k)
	}
	for _, k := range issueKeyRe.FindAllString(strings.ToUpper(text), -1) {
		add(k)
	}
	for _, m := range repoIssueRe.FindAllStringSubmatch(text, -1) {
		add(m[1] + "#" + m[2])
	}
	for _, m := range hashIssueRe.FindAllStringSubmatch(text, -1) {
		add(repo + "#" + m[1])
	}
	// A text names a few issues. A cap keeps a long body from costing many API calls.
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func webBase(server string) string {
	if server == "" {
		return "https://github.com"
	}
	return strings.TrimSuffix(server, "/")
}
