package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/http/api"
)

const breakdownUsage = `Usage: speccy breakdown propose <path> [--acknowledged]
       speccy breakdown show <path> [--json]
       speccy breakdown edit <path> --based-on <revision> --ops '<JSON list>' | --ops @<file>
       speccy breakdown update <path> --based-on <revision> [--acknowledged]
       speccy breakdown readers <path> --item <item>
       speccy breakdown push <path> --tracker linear|jira|github --target <team, project URL or owner/name>
                             --based-on <revision> [--acknowledged]
       speccy breakdown export <path> --format markdown|csv
`

// runBreakdown is speccy breakdown: the same breakdown steps as the MCP tools, for a builder or
// an author with no MCP connection (#159).
func runBreakdown(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, breakdownUsage)
		return exitUsage
	}
	verb := args[0]
	flags := map[string]string{}
	bools := map[string]bool{}
	var paths []string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "--acknowledged", "--json":
			bools[strings.TrimPrefix(a, "--")] = true
			continue
		}
		if name, ok := strings.CutPrefix(a, "--"); ok {
			if k, v, eq := strings.Cut(name, "="); eq {
				flags[k] = v
				continue
			}
			if i+1 >= len(rest) {
				fmt.Fprintf(stderr, "speccy breakdown: the flag --%s needs a value.\n", name)
				return exitUsage
			}
			i++
			flags[name] = rest[i]
			continue
		}
		paths = append(paths, a)
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, breakdownUsage)
		return exitUsage
	}
	basedOn := int64(0)
	if v, ok := flags["based-on"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			fmt.Fprintln(stderr, "speccy breakdown: --based-on is the revision of the breakdown you read, a number.")
			return exitUsage
		}
		basedOn = n
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b, s, code := oneBundle(ctx, "breakdown", paths, stderr)
	if code != exitOK {
		return code
	}
	c := s.client
	fail := func(p *api.Problem, status int) int {
		detail := fmt.Sprintf("the Speccy API answered with status %d", status)
		if p != nil && p.Detail != nil {
			detail = *p.Detail
		}
		fmt.Fprintf(stderr, "speccy breakdown %s: %s\n", verb, sentenceEnd(detail))
		return exitRun
	}
	ack := bools["acknowledged"]
	var out *api.Breakdown
	switch verb {
	case "propose":
		body := api.ProposeBreakdownJSONRequestBody{}
		if ack {
			body.Acknowledged = &ack
		}
		res, err := c.ProposeBreakdownWithResponse(ctx, b.Id, body)
		if err != nil {
			return fail(nil, 0)
		}
		if res.JSON201 == nil {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		out = res.JSON201
	case "show":
		res, err := c.GetBreakdownWithResponse(ctx, b.Id)
		if err != nil {
			return fail(nil, 0)
		}
		if res.JSON200 == nil {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		out = res.JSON200
	case "edit":
		raw := flags["ops"]
		if file, ok := strings.CutPrefix(raw, "@"); ok {
			data, err := os.ReadFile(file)
			if err != nil {
				fmt.Fprintf(stderr, "speccy breakdown edit: %v.\n", err)
				return exitUsage
			}
			raw = string(data)
		}
		var ops []api.BreakdownOp
		if err := json.Unmarshal([]byte(raw), &ops); err != nil || len(ops) == 0 || basedOn == 0 {
			fmt.Fprint(stderr, "speccy breakdown edit: give --based-on and --ops, a JSON list of changes such as "+
				`[{"op":"split","item":"i2","trace_ids":["REQ-004"]}].`+"\n")
			return exitUsage
		}
		res, err := c.EditBreakdownWithResponse(ctx, b.Id, api.EditBreakdownJSONRequestBody{BasedOn: basedOn, Ops: ops})
		if err != nil {
			return fail(nil, 0)
		}
		if res.JSON200 == nil {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		out = res.JSON200
	case "update":
		if basedOn == 0 {
			fmt.Fprint(stderr, breakdownUsage)
			return exitUsage
		}
		body := api.UpdateBreakdownJSONRequestBody{BasedOn: basedOn}
		if ack {
			body.Acknowledged = &ack
		}
		res, err := c.UpdateBreakdownWithResponse(ctx, b.Id, body)
		if err != nil {
			return fail(nil, 0)
		}
		if res.JSON200 == nil {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		out = res.JSON200
	case "readers":
		if flags["item"] == "" {
			fmt.Fprint(stderr, breakdownUsage)
			return exitUsage
		}
		res, err := c.CheckBreakdownReadersWithResponse(ctx, b.Id, api.CheckBreakdownReadersJSONRequestBody{Item: flags["item"]})
		if err != nil {
			return fail(nil, 0)
		}
		if res.JSON200 == nil {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		out = res.JSON200
	case "push":
		if basedOn == 0 || flags["tracker"] == "" || flags["target"] == "" {
			fmt.Fprint(stderr, breakdownUsage)
			return exitUsage
		}
		body := api.PushBreakdownJSONRequestBody{BasedOn: basedOn, Tracker: api.PushBreakdownRequestTracker(flags["tracker"]),
			Target: flags["target"]}
		if ack {
			body.Acknowledged = &ack
		}
		res, err := c.PushBreakdownWithResponse(ctx, b.Id, body)
		if err != nil {
			return fail(nil, 0)
		}
		if res.JSON200 == nil {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		if bools["json"] {
			return printJSON(stdout, res.JSON200)
		}
		if len(res.JSON200.Created) == 0 {
			fmt.Fprintln(stdout, "Every item is already an issue. The push created nothing.")
			return exitOK
		}
		for _, cr := range res.JSON200.Created {
			fmt.Fprintf(stdout, "%-5s %-12s %s  %s\n", cr.Kind, cr.Key, cr.Title, cr.Url)
		}
		return exitOK
	case "export":
		format := flags["format"]
		if format == "md" {
			format = "markdown"
		}
		res, err := c.ExportBreakdownWithResponse(ctx, b.Id, &api.ExportBreakdownParams{Format: format})
		if err != nil {
			return fail(nil, 0)
		}
		if res.StatusCode() != 200 {
			return fail(res.ApplicationproblemJSONDefault, res.StatusCode())
		}
		_, _ = stdout.Write(res.Body)
		return exitOK
	default:
		fmt.Fprint(stderr, breakdownUsage)
		return exitUsage
	}
	if bools["json"] {
		return printJSON(stdout, out)
	}
	fmt.Fprint(stdout, breakdownText(*out))
	if out.Must > 0 {
		return exitNotReady
	}
	return exitOK
}

// breakdownText prints a breakdown for a terminal: the epics with their items, then the
// findings.
func breakdownText(b api.Breakdown) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Breakdown of %s, version %d, revision %d", b.Bundle, b.VersionNumber, b.Revision)
	if b.Stale {
		fmt.Fprintf(&sb, " (stale: the doc is at version %d)", b.CurrentVersionNumber)
	}
	sb.WriteString("\n\n")
	for _, e := range b.Epics {
		fmt.Fprintf(&sb, "%s  %s", e.Id, e.Title)
		if e.Issue != nil {
			fmt.Fprintf(&sb, "  [%s]", e.Issue.Key)
		}
		sb.WriteString("\n")
		for _, it := range b.Items {
			if it.Epic != e.Id {
				continue
			}
			fmt.Fprintf(&sb, "  %-4s %s", it.Id, it.Title)
			if len(it.TraceIds) > 0 {
				fmt.Fprintf(&sb, "  (%s)", strings.Join(it.TraceIds, ", "))
			}
			if it.EffectiveAssignee != nil {
				fmt.Fprintf(&sb, "  → %s", it.EffectiveAssignee.Name)
			}
			if it.Issue != nil {
				fmt.Fprintf(&sb, "  [%s]", it.Issue.Key)
			}
			if len(it.DependsOn) > 0 {
				fmt.Fprintf(&sb, "  after %s", strings.Join(it.DependsOn, ", "))
			}
			sb.WriteString("\n")
			for _, ch := range it.Changed {
				fmt.Fprintf(&sb, "       changed: %s\n", ch)
			}
		}
	}
	if len(b.Findings) > 0 {
		sb.WriteString("\nFindings:\n")
		for _, f := range b.Findings {
			fmt.Fprintf(&sb, "  %-6s %-22s %s\n", f.Level, f.Slug, f.Message)
		}
	}
	return sb.String()
}

func printJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return exitRun
	}
	return exitOK
}

// runWorkItem is speccy work-item: the slice of the spec one work item builds, for a builder
// with no MCP connection (#159). --server reads it from a Speccy server.
func runWorkItem(args []string, stdout, stderr io.Writer) int {
	server, ref := "", ""
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			asJSON = true
		case a == "--server" && i+1 < len(args):
			i++
			server = args[i]
		case strings.HasPrefix(a, "--server="):
			server = strings.TrimPrefix(a, "--server=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "speccy work-item: unknown flag %s.\n", a)
			return exitUsage
		default:
			ref = a
		}
	}
	if ref == "" {
		fmt.Fprint(stderr, "Usage: speccy work-item <issue URL, issue key, Speccy URL or bundle/item> [--server URL] [--json]\n")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var c *api.ClientWithResponses
	if server != "" {
		rc, err := remoteClient(strings.TrimSuffix(server, "/"))
		if err != nil {
			fmt.Fprintf(stderr, "speccy work-item: %v.\n", err)
			return exitUsage
		}
		c = rc
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "speccy work-item: %v.\n", err)
			return exitUsage
		}
		s, err := openSession(ctx, findRoot(cwd))
		if err != nil {
			fmt.Fprintf(stderr, "speccy work-item: %v.\n", problemText(err))
			return exitRun
		}
		c = s.client
	}
	res, err := c.GetWorkItemWithResponse(ctx, &api.GetWorkItemParams{Ref: ref})
	if err != nil {
		fmt.Fprintf(stderr, "speccy work-item: %v.\n", err)
		return exitRun
	}
	if res.JSON200 == nil {
		detail := fmt.Sprintf("the Speccy API answered with status %d", res.StatusCode())
		if p := res.ApplicationproblemJSONDefault; p != nil && p.Detail != nil {
			detail = *p.Detail
		}
		fmt.Fprintf(stderr, "speccy work-item: %s\n", sentenceEnd(detail))
		return exitRun
	}
	if asJSON {
		return printJSON(stdout, res.JSON200)
	}
	fmt.Fprint(stdout, workItemText(*res.JSON200))
	return exitOK
}

// workItemText prints a work item for a builder: what to build, the criteria, the answers, and
// the version it is pinned to.
func workItemText(w api.WorkItemPacket) string {
	var sb strings.Builder
	it := w.Item
	fmt.Fprintf(&sb, "# %s\n\n%s/%s, from %s version %d\n", it.Title, w.Bundle, it.Id, w.MainDoc, w.VersionNumber)
	if w.NewerVersion {
		fmt.Fprintf(&sb, "The doc is now at version %d. Ask the author before you build from version %d.\n", w.CurrentVersionNumber, w.VersionNumber)
	}
	if len(it.TraceIds) > 0 {
		fmt.Fprintf(&sb, "Trace IDs: %s\n", strings.Join(it.TraceIds, ", "))
	}
	if len(it.Repos) > 0 {
		fmt.Fprintf(&sb, "Repo: %s\n", strings.Join(it.Repos, ", "))
	}
	sb.WriteString("\n## Acceptance criteria\n\n")
	for _, c := range it.Criteria {
		fmt.Fprintf(&sb, "> %s\n— %s\n\n", strings.Join(strings.Fields(c.Quote), " "), strings.Join(c.HeadingPath, " > "))
	}
	if len(it.Answers) > 0 {
		sb.WriteString("## Build answers\n\n")
		for _, a := range it.Answers {
			fmt.Fprintf(&sb, "- %s %s\n", a.Question, a.Answer)
		}
		sb.WriteString("\n")
	}
	if len(it.Questions) > 0 {
		sb.WriteString("## Open questions\n\n")
		for _, q := range it.Questions {
			fmt.Fprintf(&sb, "- %s\n", q)
		}
		sb.WriteString("\n")
	}
	if len(w.Links) > 0 {
		sb.WriteString("## Linked docs\n\n")
		for _, l := range w.Links {
			fmt.Fprintf(&sb, "- %s %s (%s)\n", l.Kind, l.Title, l.Bundle)
		}
		sb.WriteString("\n")
	}
	if len(w.ExternalLinks) > 0 {
		sb.WriteString("## External links\n\n")
		for _, l := range w.ExternalLinks {
			fmt.Fprintf(&sb, "- %s %s\n", l.Kind, l.Url)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("When the build is done, verify it with speccy verify <doc path> --work-item " + w.Bundle + "/" + it.Id + ".\n")
	return sb.String()
}
