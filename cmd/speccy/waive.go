package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/http/api"
)

const waiveUsage = "Usage: speccy waive <doc path> <finding ID> --reason \"<why the check does not apply>\" [--approve] [--upstream-change | --send-back]\n" +
	"The finding ID is the id of a finding in speccy review --format json.\n" +
	"--upstream-change answers a coherence.contradiction finding: the linked doc must change.\n" +
	"--send-back answers a coherence.downstream-request finding: the downstream doc must change.\n"

const waiversUsage = "Usage: speccy waivers list <doc path>\n"

// runWaive asks for a waiver of one finding (REQ-072), and with --approve approves it under the
// profile's waiver policy, as the app does (#135). The owner of the state folder decides both.
// --upstream-change and --send-back ask for the two other answers to a conflict, which use the
// waiver mechanism.
func runWaive(args []string, stdout, stderr io.Writer) int {
	reason, approve, upstream, sendBack := "", false, false, false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--reason":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "speccy waive: the flag --reason needs a value.")
				return exitUsage
			}
			i++
			reason = args[i]
		case strings.HasPrefix(a, "--reason="):
			reason = strings.TrimPrefix(a, "--reason=")
		case a == "--approve":
			approve = true
		case a == "--upstream-change":
			upstream = true
		case a == "--send-back":
			sendBack = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "speccy waive: unknown flag %s.\n\n%s", a, waiveUsage)
			return exitUsage
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) != 2 {
		fmt.Fprint(stderr, waiveUsage)
		return exitUsage
	}
	if strings.TrimSpace(reason) == "" {
		fmt.Fprintf(stderr, "speccy waive: give the reason with --reason. An approver reads it to judge the waiver.\n\n%s", waiveUsage)
		return exitUsage
	}
	if upstream && sendBack {
		fmt.Fprintf(stderr, "speccy waive: give one of --upstream-change and --send-back.\n\n%s", waiveUsage)
		return exitUsage
	}
	findingID, err := parseUUID(rest[1])
	if err != nil {
		fmt.Fprintf(stderr, "speccy waive: %q is not a finding ID. Find the id of the finding in speccy review --format json.\n", rest[1])
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	doc, s, code := oneBundle(ctx, "waive", rest[:1], stderr)
	if code != exitOK {
		return code
	}
	body := api.RequestWaiverJSONRequestBody{FindingId: findingID, Reason: reason}
	if upstream {
		body.UpstreamChange = &upstream
	}
	if sendBack {
		body.SendBack = &sendBack
	}
	res, err := s.client.RequestWaiverWithResponse(ctx, doc.Id, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy waive: %s\n", sentence(err))
		return exitRun
	}
	if res.JSON200 == nil {
		return commandProblem(stderr, "waive", res.ApplicationproblemJSONDefault)
	}
	w := *res.JSON200
	what := "a waiver"
	switch {
	case upstream:
		what = "a change of " + w.Conflict.With
	case sendBack:
		what = "a send-back to " + downstreamName(w)
	}
	fmt.Fprintf(stdout, "Asked for %s for %s in %s. Waiver ID %s.\n", what, w.CheckSlug, waiverWhere(w), w.Id)
	if !approve {
		fmt.Fprintf(stdout, "It needs %s under the policy %s. Approve it in the app, or run this command with --approve.\n", approvals(w.Needed), w.Policy)
		return exitOK
	}
	ar, err := s.client.ApproveWaiverWithResponse(ctx, w.Id)
	if err != nil {
		fmt.Fprintf(stderr, "speccy waive: the request exists, but the approval failed: %s\n", sentence(err))
		return exitRun
	}
	if ar.JSON200 == nil {
		fmt.Fprintf(stderr, "speccy waive: the request exists, but the approval failed: %s\n", problemText(ar.ApplicationproblemJSONDefault))
		return commandExit(ar.ApplicationproblemJSONDefault)
	}
	w = *ar.JSON200
	if w.Status == api.WaiverStatusApproved {
		switch {
		case upstream:
			fmt.Fprintf(stdout, "Approved. Speccy wrote the upstream request to the doc's sidecar. The finding waits on %s.\n", w.Conflict.With)
		case sendBack:
			fmt.Fprintf(stdout, "Approved. Speccy wrote the send-back to the sidecar of %s. The conflict blocks that doc again.\n", downstreamName(w))
		default:
			fmt.Fprintf(stdout, "Approved. Speccy wrote the waiver to the doc's sidecar, and the finding no longer counts in the verdict.\n")
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "Approved: %d of %s. The waiver applies after the last approval.\n", len(w.Approvals), approvals(w.Needed))
	return exitOK
}

// runWaivers is speccy waivers list: the waivers of one spec doc, newest first.
func runWaivers(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "list" {
		fmt.Fprint(stderr, waiversUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	doc, s, code := oneBundle(ctx, "waivers", args[1:], stderr)
	if code != exitOK {
		return code
	}
	res, err := s.client.ListWaiversWithResponse(ctx, doc.Id)
	if err != nil {
		fmt.Fprintf(stderr, "speccy waivers: %s\n", sentence(err))
		return exitRun
	}
	if res.JSON200 == nil {
		return commandProblem(stderr, "waivers", res.ApplicationproblemJSONDefault)
	}
	items := res.JSON200.Items
	if len(items) == 0 {
		fmt.Fprintf(stdout, "%s has no waivers.\n", doc.Slug)
		return exitOK
	}
	for i, w := range items {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		status := string(w.Status)
		kind := ""
		switch {
		case w.UpstreamChange != nil && *w.UpstreamChange:
			kind = "  upstream request"
		case w.SendBack != nil && *w.SendBack:
			kind = "  send-back to " + downstreamName(w)
		}
		switch w.Status {
		case api.WaiverStatusRequested:
			status = fmt.Sprintf("requested, %d of %s", len(w.Approvals), approvals(w.Needed))
		case api.WaiverStatusInvalidated:
			status = "ended"
			if w.EndedBecause != nil {
				status += ": " + strings.ReplaceAll(string(*w.EndedBecause), "_", " ")
			}
		}
		fmt.Fprintf(stdout, "%s  %s  %s%s\n", w.Id, w.CheckSlug, status, kind)
		fmt.Fprintf(stdout, "  In %s. Asked by %s.\n", waiverWhere(w), w.RequestedBy)
		if c := w.Conflict; c != nil {
			fmt.Fprintf(stdout, "  Conflict with %s: %q against %q.\n", c.With, c.Quote, c.WithQuote)
		}
		fmt.Fprintf(stdout, "  Reason: %s\n", w.Reason)
		if w.DecisionReason != nil {
			fmt.Fprintf(stdout, "  Decision reason: %s\n", *w.DecisionReason)
		}
	}
	return exitOK
}

// downstreamName names the downstream doc of a send-back.
func downstreamName(w api.Waiver) string {
	if w.Downstream == nil {
		return "the downstream doc"
	}
	return w.Downstream.Slug
}

// waiverWhere names what a waiver binds to: a trace ID, a section, or the whole doc.
func waiverWhere(w api.Waiver) string {
	switch {
	case w.Trace != nil:
		return "trace ID " + w.Trace.Id
	case len(w.Section) == 0:
		return "the whole doc"
	}
	return "the section " + strings.Join(w.Section, " › ")
}

func approvals(n int) string {
	if n == 1 {
		return "1 approval"
	}
	return fmt.Sprintf("%d approvals", n)
}

// commandExit is the exit code of an API problem: a problem with the request is 2, the rest 3.
func commandExit(p *api.Problem) int {
	if p != nil && (p.Status == 400 || p.Status == 401 || p.Status == 403 || p.Status == 404 || p.Status == 422) {
		return exitUsage
	}
	return exitRun
}
