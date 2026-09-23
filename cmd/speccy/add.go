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

// runAdd is speccy add <url>: make a GitHub source from a source URL, sync it once, and print
// the bundles it made (REQ-128). It uses the same endpoints as the bundles screen, so the two
// ways in give one result.
func runAdd(args []string, stdout, stderr io.Writer) int {
	url, profile := "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--profile" && i+1 < len(args):
			i++
			profile = args[i]
		case strings.HasPrefix(args[i], "--profile="):
			profile = strings.TrimPrefix(args[i], "--profile=")
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "speccy add: %s is not a flag of this command.\n\nUsage: speccy add <url> [--profile <key>]\n", args[i])
			return exitUsage
		case url == "":
			url = args[i]
		default:
			fmt.Fprint(stderr, "speccy add takes one URL.\n\nUsage: speccy add <url> [--profile <key>]\n")
			return exitUsage
		}
	}
	if url == "" {
		fmt.Fprint(stderr, "speccy add needs a GitHub address.\n\nUsage: speccy add <url> [--profile <key>]\n")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy add: %v.\n", err)
		return exitUsage
	}
	// The source must outlive the command, so this session keeps its store in .speccy/state.
	s, err := openSessionIn(ctx, findRoot(cwd), true)
	if err != nil {
		fmt.Fprintf(stderr, "speccy add: %v.\n", problemText(err))
		return exitRun
	}
	body := api.AddGithubSourceJSONRequestBody{Url: url}
	if profile != "" {
		body.Profile = &profile
	}
	res, err := s.client.AddGithubSourceWithResponse(ctx, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy add: %v.\n", problemText(err))
		return exitRun
	}
	if res.JSON200 == nil {
		fmt.Fprintf(stderr, "speccy add: %s\n", problemText(res.ApplicationproblemJSONDefault))
		return exitRun
	}
	src := res.JSON200.Source
	what := src.Path
	if src.Path == "." {
		what = "the whole repo"
	}
	if res.JSON200.AlreadyAdded {
		fmt.Fprintf(stdout, "Already added: Speccy reads %s on %s, %s.\n", src.Repo, src.Branch, what)
	} else {
		fmt.Fprintf(stdout, "Reading %s on %s, %s.\n", src.Repo, src.Branch, what)
	}
	if src.Error != "" {
		fmt.Fprintf(stderr, "The first sync failed: %s\n", src.Error)
		return exitRun
	}
	bundles, err := listAll(ctx, s.client)
	if err != nil {
		fmt.Fprintf(stderr, "speccy add: %v.\n", problemText(err))
		return exitRun
	}
	n := 0
	for _, b := range bundles {
		if b.SourceKind == api.SpecDocSourceKindGithub {
			fmt.Fprintf(stdout, "  %s\n", b.Slug)
			n++
		}
	}
	fmt.Fprintf(stdout, "%d bundle%s. Run speccy to open them.\n", n, pluralS(n))
	// The store holds the local database and key, which no repo wants.
	if added, err := ensureIgnored(".gitignore", ".speccy/state/"); err == nil && added {
		fmt.Fprintln(stdout, "Added .speccy/state/ to .gitignore.")
	}
	return exitOK
}
