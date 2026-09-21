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

// runReport sends a build report against a handoff (REQ-137), for a builder with no MCP
// connection.
func runReport(args []string, stdout, stderr io.Writer) int {
	var handoff, sectionPath, traceID, text string
	kind := api.Note
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func(name string) (string, bool) {
			if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
				return v, true
			}
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "speccy report: the flag --%s needs a value.\n", name)
				return "", false
			}
			i++
			return args[i], true
		}
		ok := true
		switch {
		case a == "--handoff" || strings.HasPrefix(a, "--handoff="):
			handoff, ok = value("handoff")
		case a == "--section" || strings.HasPrefix(a, "--section="):
			sectionPath, ok = value("section")
		case a == "--trace-id" || strings.HasPrefix(a, "--trace-id="):
			traceID, ok = value("trace-id")
		case a == "--text" || strings.HasPrefix(a, "--text="):
			text, ok = value("text")
		case a == "--blocked":
			kind = api.Blocked
		case a == "--note":
			kind = api.Note
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "speccy report: unknown flag %s.\n", a)
			return exitUsage
		default:
			paths = append(paths, a)
		}
		if !ok {
			return exitUsage
		}
	}
	if len(paths) != 1 || handoff == "" || strings.TrimSpace(text) == "" {
		fmt.Fprint(stderr, "Usage: speccy report <path> --handoff <id> --text <what you need> [--blocked | --note] [--section \"A › B\"] [--trace-id REQ-012]\n")
		return exitUsage
	}
	id, err := parseUUID(handoff)
	if err != nil {
		fmt.Fprintf(stderr, "speccy report: --handoff must be the ID from the build packet.\n")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, s, code := oneBundle(ctx, "report", paths, stderr)
	if code != exitOK {
		return code
	}
	body := api.ReportBuildJSONRequestBody{Kind: kind, Text: text}
	if sectionPath != "" {
		parts := strings.Split(sectionPath, "›")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		body.Section = &parts
	}
	if traceID != "" {
		body.TraceId = &traceID
	}
	res, err := s.client.ReportBuildWithResponse(ctx, id, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy report: %v.\n", err)
		return exitRun
	}
	if res.JSON200 == nil {
		fmt.Fprintf(stderr, "speccy report: %s\n", problemText(res.ApplicationproblemJSONDefault))
		return exitRun
	}
	t := res.JSON200
	what := "a note"
	if t.Blocking {
		what = "a blocking thread"
	}
	fmt.Fprintf(stdout, "Reported %s: %s\n", what, t.Title)
	return exitOK
}
