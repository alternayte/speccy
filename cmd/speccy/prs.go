package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/http/api"
)

const reviewPRsUsage = `Usage: speccy review-prs <pull request URL>... | --repo <owner/name> [--requested]
                        [--parallel N] [--again] [--yes] [--stages …]
       speccy review-prs --batch <ID>    Show a batch until it ends.
       speccy review-prs --cancel <ID>   Start no new pull request of a batch.
`

const askUsage = `Usage: speccy ask <pull request URL> "<concern>" [--section "A › B"] [--force]
`

const pendingUsage = `Usage: speccy pending list <pull request URL>
       speccy pending delete <pull request URL> <ID>...
       speccy pending discard <pull request URL>... | --repo <owner/name> | --batch <ID>
`

// prFlags are the flags of the pull request commands. args are the words that are no flag.
type prFlags struct {
	args      []string
	repo      string
	requested bool
	parallel  int
	again     bool
	yes       bool
	stages    string
	stagesSet bool
	batch     string
	cancel    string
	section   string
	force     bool
}

// parsePRFlags reads the flags that allowed names. A flag in no command's list is an error.
func parsePRFlags(cmd string, args []string, allowed ...string) (prFlags, error) {
	var f prFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			f.args = append(f.args, a)
			continue
		}
		name, v, hasValue := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		ok := false
		for _, n := range allowed {
			ok = ok || n == name
		}
		if !ok {
			return f, fmt.Errorf("speccy %s: unknown flag %s", cmd, a)
		}
		value := func() (string, error) {
			if hasValue {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("speccy %s: the flag --%s needs a value", cmd, name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "requested":
			f.requested = true
		case "again":
			f.again = true
		case "yes":
			f.yes = true
		case "force":
			f.force = true
		case "repo":
			f.repo, err = value()
		case "stages":
			f.stages, err = value()
			f.stagesSet = true
		case "batch":
			f.batch, err = value()
		case "cancel":
			f.cancel, err = value()
		case "section":
			f.section, err = value()
		case "parallel":
			var s string
			if s, err = value(); err == nil {
				if f.parallel, err = strconv.Atoi(s); err != nil {
					err = fmt.Errorf("speccy %s: --parallel takes a number", cmd)
				}
			}
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// prSession opens the local state of the current folder. A batch records each review, so the
// state stays after the command.
func prSession(ctx context.Context) (*session, error) {
	cwd, _ := os.Getwd()
	return openSessionIn(ctx, findRoot(cwd), true)
}

// runReviewPRs is speccy review-prs: a batch of pull request reviews, each posted as a pending
// review that only the reviewer sees (docs/specs/pr-review-batch.md).
func runReviewPRs(args []string, stdin io.Reader, interactive bool, stdout, stderr io.Writer) int {
	f, err := parsePRFlags("review-prs", args, "repo", "requested", "parallel", "again", "yes", "stages", "batch", "cancel")
	if err != nil {
		fmt.Fprintf(stderr, "%v.\n\n%s", err, reviewPRsUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if f.batch != "" || f.cancel != "" {
		ref := f.batch + f.cancel
		id, err := uuid.Parse(ref)
		if err != nil || f.batch != "" && f.cancel != "" || len(f.args) > 0 {
			fmt.Fprint(stderr, reviewPRsUsage)
			return exitUsage
		}
		s, err := prSession(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "speccy review-prs: %s\n", sentence(err))
			return exitRun
		}
		if f.cancel != "" {
			res, err := s.client.CancelPrBatchWithResponse(ctx, id)
			if err != nil {
				fmt.Fprintf(stderr, "speccy review-prs: %s\n", sentence(err))
				return exitRun
			}
			if res.JSON200 == nil {
				return commandProblem(stderr, "review-prs", res.ApplicationproblemJSONDefault)
			}
			fmt.Fprintln(stdout, "No new pull request of the batch starts. The reviews that run now finish and post.")
			return exitOK
		}
		return watchBatch(ctx, s.client, id, stdout, stderr)
	}
	if (len(f.args) == 0) == (f.repo == "") {
		fmt.Fprint(stderr, "speccy review-prs: name the pull requests by their URLs, or name one repo with --repo.\n\n"+reviewPRsUsage)
		return exitUsage
	}
	body := api.CreatePrBatchJSONRequestBody{}
	if len(f.args) > 0 {
		body.Urls = &f.args
	}
	if f.repo != "" {
		body.Repo = &f.repo
	}
	if f.requested {
		body.Requested = &f.requested
	}
	if f.again {
		body.Again = &f.again
	}
	if f.parallel != 0 {
		body.Parallel = &f.parallel
	}
	if f.stagesSet {
		st := []api.PrBatchRequestStages{}
		for _, name := range strings.Split(f.stages, ",") {
			if name = strings.TrimSpace(name); name != "" && name != "lint" && name != "all" {
				st = append(st, api.PrBatchRequestStages(name))
			}
		}
		if strings.TrimSpace(f.stages) != "all" {
			body.Stages = &st
		}
	}
	start := f.yes
	body.Start = &start
	s, err := prSession(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review-prs: %s\n", sentence(err))
		return exitRun
	}
	fmt.Fprintln(stderr, "Speccy reads the pull requests and estimates the cost.")
	res, err := s.client.CreatePrBatchWithResponse(ctx, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review-prs: %s\n", sentence(err))
		return exitRun
	}
	if res.JSON200 == nil {
		return commandProblem(stderr, "review-prs", res.ApplicationproblemJSONDefault)
	}
	b := res.JSON200
	printPlan(stdout, b)
	if !f.yes {
		if b.Estimate.Pulls == 0 {
			_, _ = s.client.CancelPrBatchWithResponse(ctx, b.Id)
			fmt.Fprintln(stdout, "Nothing to review.")
			return exitOK
		}
		if !interactive {
			_, _ = s.client.CancelPrBatchWithResponse(ctx, b.Id)
			fmt.Fprintln(stderr, "speccy review-prs: pass --yes to start the batch with no question.")
			return exitUsage
		}
		fmt.Fprint(stdout, "Start the batch? [y/N] ")
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			_, _ = s.client.CancelPrBatchWithResponse(ctx, b.Id)
			fmt.Fprintln(stdout, "The batch did not start.")
			return exitOK
		}
		started, err := s.client.StartPrBatchWithResponse(ctx, b.Id)
		if err != nil {
			fmt.Fprintf(stderr, "speccy review-prs: %s\n", sentence(err))
			return exitRun
		}
		if started.JSON200 == nil {
			return commandProblem(stderr, "review-prs", started.ApplicationproblemJSONDefault)
		}
	}
	return watchBatch(ctx, s.client, b.Id, stdout, stderr)
}

// printPlan says what a batch will review and what it costs.
func printPlan(w io.Writer, b *api.PrBatch) {
	e := b.Estimate
	fmt.Fprintf(w, "Batch %s: %s.\n", b.Id, b.Source)
	fmt.Fprintf(w, "%d pull request%s with %d spec doc%s to review.", e.Pulls, pluralS(e.Pulls), e.Docs, pluralS(e.Docs))
	skipped := 0
	for _, it := range b.Items {
		if it.State == api.PrBatchItemStateSkipped || it.State == api.PrBatchItemStateFailed {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Fprintf(w, " %d other%s will not run:", skipped, pluralS(skipped))
	}
	fmt.Fprintln(w)
	for _, it := range b.Items {
		if it.State == api.PrBatchItemStateSkipped || it.State == api.PrBatchItemStateFailed {
			fmt.Fprintf(w, "  %s#%d: %s\n", it.Repo, it.Pull, it.Reason)
		}
	}
	switch {
	case e.LintOnly:
		fmt.Fprintln(w, "Lint checks only: no model call. Assign models in the app (Admin → Models) for the full review.")
	case e.Priced:
		fmt.Fprintf(w, "Estimate: %d model calls, %d tokens in, %d tokens out, about $%.2f.\n", e.Calls, e.TokensIn, e.TokensOut, e.CostUsd)
	default:
		fmt.Fprintf(w, "Estimate: %d model calls, %d tokens in, %d tokens out. No prices are set, so no cost shows.\n", e.Calls, e.TokensIn, e.TokensOut)
	}
}

// watchBatch prints each pull request of a batch when it finishes, until the batch ends.
func watchBatch(ctx context.Context, c *api.ClientWithResponses, id uuid.UUID, stdout, stderr io.Writer) int {
	shown := map[int]bool{}
	for {
		res, err := c.GetPrBatchWithResponse(ctx, id)
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "The batch goes on. Show it with speccy review-prs --batch %s, or stop it with speccy review-prs --cancel %s.\n", id, id)
			return exitOK
		}
		if err != nil {
			fmt.Fprintf(stderr, "speccy review-prs: %s\n", sentence(err))
			return exitRun
		}
		if res.JSON200 == nil {
			return commandProblem(stderr, "review-prs", res.ApplicationproblemJSONDefault)
		}
		b := res.JSON200
		for i, it := range b.Items {
			if shown[i] || it.State == api.PrBatchItemStateWaiting || it.State == api.PrBatchItemStateReviewing {
				continue
			}
			shown[i] = true
			fmt.Fprintln(stdout, itemLine(it))
		}
		if b.Status != api.PrBatchStatusRunning && b.Status != api.PrBatchStatusPlanned {
			counts := map[api.PrBatchItemState]int{}
			for _, it := range b.Items {
				counts[it.State]++
			}
			fmt.Fprintf(stdout, "Batch %s: %d posted, %d skipped, %d failed.\n", b.Status,
				counts[api.PrBatchItemStatePosted], counts[api.PrBatchItemStateSkipped], counts[api.PrBatchItemStateFailed])
			if counts[api.PrBatchItemStateFailed] > 0 {
				return exitRun
			}
			return exitOK
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// itemLine is one finished pull request of a batch, for a person.
func itemLine(it api.PrBatchItem) string {
	head := fmt.Sprintf("%s#%d", it.Repo, it.Pull)
	if it.State != api.PrBatchItemStatePosted {
		return fmt.Sprintf("%-8s %s: %s", it.State, head, it.Reason)
	}
	var docs []string
	for _, d := range it.Docs {
		switch {
		case d.Error != nil:
			docs = append(docs, fmt.Sprintf("%s: the review failed: %s", d.Path, *d.Error))
		case d.Verdict == "build_ready":
			docs = append(docs, d.Path+": Build Ready")
		default:
			docs = append(docs, fmt.Sprintf("%s: Not Build Ready, %d MUST, %d SHOULD", d.Path, d.Must, d.Should))
		}
	}
	changes := fmt.Sprintf("%d comment%s added", it.Comments, pluralS(it.Comments))
	if it.Removed > 0 {
		changes += fmt.Sprintf(", %d removed because the finding is gone", it.Removed)
	}
	return fmt.Sprintf("posted   %s: %s. Your pending review: %s. %s", head, strings.Join(docs, "; "), changes, it.ReviewUrl)
}

// runAsk is speccy ask: one concern of the reviewer becomes one question for the author, in
// the reviewer's pending review.
func runAsk(args []string, stdout, stderr io.Writer) int {
	f, err := parsePRFlags("ask", args, "section", "force")
	if err != nil {
		fmt.Fprintf(stderr, "%v.\n\n%s", err, askUsage)
		return exitUsage
	}
	if len(f.args) != 2 || strings.TrimSpace(f.args[1]) == "" {
		fmt.Fprint(stderr, askUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := prSession(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "speccy ask: %s\n", sentence(err))
		return exitRun
	}
	body := api.AskAuthorJSONRequestBody{Url: f.args[0], Concern: f.args[1]}
	if f.force {
		body.Force = &f.force
	}
	if f.section != "" {
		parts := strings.Split(f.section, "›")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		body.Section = &parts
	}
	res, err := s.client.AskAuthorWithResponse(ctx, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy ask: %s\n", sentence(err))
		return exitRun
	}
	if res.JSON200 == nil {
		return commandProblem(stderr, "ask", res.ApplicationproblemJSONDefault)
	}
	r := res.JSON200
	where := ""
	if r.Doc != nil {
		where = *r.Doc
		if r.HeadingPath != nil && len(*r.HeadingPath) > 0 {
			where += " › " + strings.Join(*r.HeadingPath, " › ")
		}
	}
	switch r.Status {
	case api.AskResultStatusUnclear:
		fmt.Fprintln(stdout, "The concern fits more than one section, so Speccy posted nothing. Name one with --section:")
		for _, c := range deref(r.Candidates) {
			fmt.Fprintf(stdout, "  %s: --section %q\n", c.Doc, strings.Join(c.HeadingPath, " › "))
		}
		fmt.Fprintf(stdout, "The question would be: %s\n", r.Question)
	case api.AskResultStatusAnswered:
		fmt.Fprintf(stdout, "%s already answers this, so Speccy posted nothing:\n  \"%s\"\n", where, strings.Join(strings.Fields(deref(r.AnswerQuote)), " "))
		fmt.Fprintf(stdout, "The question would be: %s\nAdd --force to post it anyway.\n", r.Question)
	default:
		at := "in the body of the review, because the line is not in the diff"
		switch {
		case r.Line != nil:
			at = fmt.Sprintf("on line %d", *r.Line)
		case r.Place != nil && *r.Place == api.AskResultPlaceFile:
			at = "on the whole file, because the line is not in the diff"
		}
		fmt.Fprintf(stdout, "Added to your pending review, on %s, %s:\n  %s\n%s\n", where, at, r.Question, deref(r.ReviewUrl))
	}
	return exitOK
}

// runPending is speccy pending: list, delete or discard what is in the reviewer's pending
// reviews.
func runPending(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, pendingUsage)
		return exitUsage
	}
	sub := args[0]
	f, err := parsePRFlags("pending "+sub, args[1:], "repo", "batch")
	if err != nil {
		fmt.Fprintf(stderr, "%v.\n\n%s", err, pendingUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	open := func() (*session, int) {
		s, err := prSession(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "speccy pending: %s\n", sentence(err))
			return nil, exitRun
		}
		return s, exitOK
	}
	switch sub {
	case "list":
		if len(f.args) != 1 || f.repo != "" || f.batch != "" {
			fmt.Fprint(stderr, pendingUsage)
			return exitUsage
		}
		s, code := open()
		if s == nil {
			return code
		}
		res, err := s.client.ListPendingWithResponse(ctx, &api.ListPendingParams{Url: f.args[0]})
		if err != nil {
			fmt.Fprintf(stderr, "speccy pending: %s\n", sentence(err))
			return exitRun
		}
		if res.JSON200 == nil {
			return commandProblem(stderr, "pending", res.ApplicationproblemJSONDefault)
		}
		r := res.JSON200
		if !r.Exists {
			fmt.Fprintf(stdout, "You have no pending review on %s#%d.\n", r.Repo, r.Pull)
			return exitOK
		}
		fmt.Fprintf(stdout, "Your pending review on %s#%d has %d comment%s. %s\n", r.Repo, r.Pull, len(r.Comments), pluralS(len(r.Comments)), deref(r.ReviewUrl))
		for _, c := range r.Comments {
			by := "you   "
			if c.Speccy {
				by = "Speccy"
			}
			at := c.Path
			if c.Line != nil {
				at += ":" + strconv.Itoa(*c.Line)
			}
			fmt.Fprintf(stdout, "  %d  %s  %s  %s\n", c.Id, by, at, c.Excerpt)
		}
		return exitOK
	case "delete":
		if len(f.args) < 2 || f.repo != "" || f.batch != "" {
			fmt.Fprint(stderr, pendingUsage)
			return exitUsage
		}
		var ids []int64
		for _, a := range f.args[1:] {
			n, err := strconv.ParseInt(a, 10, 64)
			if err != nil {
				fmt.Fprintf(stderr, "speccy pending delete: %q is not a comment ID. speccy pending list gives them.\n", a)
				return exitUsage
			}
			ids = append(ids, n)
		}
		s, code := open()
		if s == nil {
			return code
		}
		res, err := s.client.DeletePendingWithResponse(ctx, api.DeletePendingJSONRequestBody{Url: f.args[0], Ids: ids})
		if err != nil {
			fmt.Fprintf(stderr, "speccy pending: %s\n", sentence(err))
			return exitRun
		}
		if res.JSON200 == nil {
			return commandProblem(stderr, "pending", res.ApplicationproblemJSONDefault)
		}
		fmt.Fprintf(stdout, "Deleted %d comment%s.\n", len(res.JSON200.Deleted), pluralS(len(res.JSON200.Deleted)))
		for _, id := range res.JSON200.Missing {
			fmt.Fprintf(stdout, "  %d is not in your pending review.\n", id)
		}
		return exitOK
	case "discard":
		body := api.DiscardPendingJSONRequestBody{}
		named := 0
		if len(f.args) > 0 {
			body.Urls = &f.args
			named++
		}
		if f.repo != "" {
			body.Repo = &f.repo
			named++
		}
		if f.batch != "" {
			id, err := uuid.Parse(f.batch)
			if err != nil {
				fmt.Fprintf(stderr, "speccy pending discard: --batch takes the ID of a batch.\n")
				return exitUsage
			}
			body.Batch = &id
			named++
		}
		if named != 1 {
			fmt.Fprint(stderr, pendingUsage)
			return exitUsage
		}
		s, code := open()
		if s == nil {
			return code
		}
		res, err := s.client.DiscardPendingWithResponse(ctx, body)
		if err != nil {
			fmt.Fprintf(stderr, "speccy pending: %s\n", sentence(err))
			return exitRun
		}
		if res.JSON200 == nil {
			return commandProblem(stderr, "pending", res.ApplicationproblemJSONDefault)
		}
		for _, p := range res.JSON200.Discarded {
			fmt.Fprintf(stdout, "Discarded your pending review on %s#%d.\n", p.Repo, p.Pull)
		}
		fmt.Fprintf(stdout, "%d discarded. %d pull request%s had no pending review of yours.\n", len(res.JSON200.Discarded),
			len(res.JSON200.None), pluralS(len(res.JSON200.None)))
		return exitOK
	default:
		fmt.Fprintf(stderr, "speccy pending: unknown command %q.\n\n%s", sub, pendingUsage)
		return exitUsage
	}
}

// commandProblem prints an API refusal for a command, and returns its exit code.
func commandProblem(stderr io.Writer, cmd string, p *api.Problem) int {
	fmt.Fprintf(stderr, "speccy %s: %s\n", cmd, problemText(p))
	if p != nil && (p.Status == 400 || p.Status == 401 || p.Status == 403 || p.Status == 404 || p.Status == 422) {
		return exitUsage
	}
	return exitRun
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
