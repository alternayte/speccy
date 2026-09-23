package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/http/api"
)

// runVerify verifies one build of a bundle: a pasted GitHub URL, a folder on disk, a repo at one
// commit, or with none of these the repo the doc's implemented-by link names. Speccy reads the
// code; it never runs it, and it never runs the tests.
func runVerify(args []string, stdout, stderr io.Writer) int {
	var repo, sha, path, handoff string
	summary := false
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func(name string) (string, bool) {
			if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
				return v, true
			}
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "speccy verify: the flag --%s needs a value.\n", name)
				return "", false
			}
			i++
			return args[i], true
		}
		ok := true
		switch {
		case a == "--repo" || strings.HasPrefix(a, "--repo="):
			repo, ok = value("repo")
		case a == "--sha" || strings.HasPrefix(a, "--sha="):
			sha, ok = value("sha")
		case a == "--path" || strings.HasPrefix(a, "--path="):
			path, ok = value("path")
		case a == "--handoff" || strings.HasPrefix(a, "--handoff="):
			handoff, ok = value("handoff")
		case a == "--summary":
			summary = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "speccy verify: unknown flag %s.\n", a)
			return exitUsage
		default:
			paths = append(paths, a)
		}
		if !ok {
			return exitUsage
		}
	}
	if len(paths) < 1 || len(paths) > 2 || (sha != "" && repo == "") {
		fmt.Fprint(stderr, "Usage: speccy verify <path> [<GitHub URL or folder>] [--handoff <id>] [--summary]\n"+
			"       speccy verify <path> --repo <owner/name> [--sha <sha>] [--summary]\n"+
			"       speccy verify <path> --path <folder> [--summary]\n"+
			"With no URL, Speccy verifies the repo that the doc's implemented-by link names.\n")
		return exitUsage
	}
	body := api.RunVerificationJSONRequestBody{}
	if len(paths) == 2 {
		target := paths[1]
		paths = paths[:1]
		// A folder on this machine goes to the server as an absolute path.
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			if abs, err := filepath.Abs(target); err == nil {
				target = abs
			}
		}
		body.Target = &target
	}
	if repo != "" {
		body.Repo = &repo
	}
	if sha != "" {
		body.Sha = &sha
	}
	if path != "" {
		body.Path = &path
	}
	if handoff != "" {
		id, err := parseUUID(handoff)
		if err != nil {
			fmt.Fprint(stderr, "speccy verify: --handoff must be the ID from the build packet.\n")
			return exitUsage
		}
		body.HandoffId = &id
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b, s, code := oneBundle(ctx, "verify", paths, stderr)
	if code != exitOK {
		return code
	}
	v, err := api.StartVerification(ctx, s.client, b.Id, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy verify: %s\n", sentenceEnd(err.Error()))
		return exitRun
	}
	if summary {
		fmt.Fprint(stdout, verifySummary(v))
	} else {
		fmt.Fprint(stdout, verifyTable(v))
	}
	if v.NotVerified() {
		return exitNotReady
	}
	return exitOK
}

// verifyTable prints one row per trace ID, with the file and the anchor quote it matched.
func verifyTable(v api.Verification) string {
	var b strings.Builder
	rows := append([]api.VerificationOutcome(nil), v.Outcomes...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TraceId < rows[j].TraceId })
	for _, o := range rows {
		fmt.Fprintf(&b, "%-10s %-12s", o.TraceId, o.Outcome)
		where := ""
		for _, t := range o.Targets {
			if t.Holds != nil && *t.Holds {
				line := 0
				if t.Line != nil {
					line = *t.Line
				}
				where = fmt.Sprintf("%s:%d", t.Path, line)
				if t.Kind == api.Test {
					where += " (test cited)"
				}
				break
			}
		}
		if where == "" {
			for _, t := range o.Targets {
				if t.Fault != nil && *t.Fault != "" {
					where = *t.Fault
					break
				}
			}
		}
		b.WriteString(where)
		b.WriteString("\n")
		if o.Note != nil && *o.Note != "" {
			fmt.Fprintf(&b, "%-10s %s\n", "", *o.Note)
		}
	}
	b.WriteString("\n" + verifySummary(v))
	return b.String()
}

// verifySummary is the one-block result, for a terminal and for a comment.
func verifySummary(v api.Verification) string {
	var b strings.Builder
	verdict := "Verified"
	if v.NotVerified() {
		verdict = "Not Verified"
	}
	at := v.Sha
	if at == "" && v.Digest != nil {
		at = "a folder"
	}
	fmt.Fprintf(&b, "%s: %s at %s\n", verdict, v.Repo, at)
	c := v.Counts
	fmt.Fprintf(&b, "%d implemented, %d untested, %d unproven, %d missing, %d breached",
		c.Implemented, c.Untested, c.Unproven, c.Missing, c.Breached)
	if c.Waived > 0 {
		fmt.Fprintf(&b, ", %d waived", c.Waived)
	}
	b.WriteString(".\n")
	if c.Blocking > 0 {
		fmt.Fprintf(&b, "%d blocking thread(s) opened on the bundle.\n", c.Blocking)
	}
	if c.Skipped > 0 {
		fmt.Fprintf(&b, "The gate skipped %d trace ID(s) outside the profile's verify prefixes.\n", c.Skipped)
	}
	for _, n := range v.Notes {
		b.WriteString(n + "\n")
	}
	return b.String()
}

// sentenceEnd ends a message with a full stop.
func sentenceEnd(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}
