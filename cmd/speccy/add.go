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
	const usage = "Usage: speccy add <url> [--profile <key>] [--doc <path>=<profile>]... [--server <URL>]\n"
	url, profile, server := "", "", ""
	var picks []api.PullDocChoice
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--profile" && i+1 < len(args):
			i++
			profile = args[i]
		case strings.HasPrefix(args[i], "--profile="):
			profile = strings.TrimPrefix(args[i], "--profile=")
		case args[i] == "--server" && i+1 < len(args):
			i++
			server = args[i]
		case strings.HasPrefix(args[i], "--server="):
			server = strings.TrimPrefix(args[i], "--server=")
		case (args[i] == "--doc" && i+1 < len(args)) || strings.HasPrefix(args[i], "--doc="):
			v := strings.TrimPrefix(args[i], "--doc=")
			if args[i] == "--doc" {
				i++
				v = args[i]
			}
			p, key, ok := strings.Cut(v, "=")
			if !ok || p == "" {
				fmt.Fprintf(stderr, "speccy add: --doc takes <path>=<profile>, or <path>= for a file that is not a spec.\n\n%s", usage)
				return exitUsage
			}
			picks = append(picks, api.PullDocChoice{Path: p, Profile: key})
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(stderr, "speccy add: %s is not a flag of this command.\n\n%s", args[i], usage)
			return exitUsage
		case url == "":
			url = args[i]
		default:
			fmt.Fprint(stderr, "speccy add takes one URL.\n\n"+usage)
			return exitUsage
		}
	}
	if url == "" {
		fmt.Fprint(stderr, "speccy add needs a GitHub address.\n\n"+usage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var client *api.ClientWithResponses
	if server != "" {
		c, err := remoteClient(server)
		if err != nil {
			fmt.Fprintf(stderr, "speccy add: %v.\n", err)
			return exitUsage
		}
		client = c
	} else {
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
		client = s.client
	}
	body := api.AddGithubSourceJSONRequestBody{Url: url}
	if profile != "" {
		body.Profile = &profile
	}
	if picks != nil {
		body.Docs = &picks
	}
	res, err := client.AddGithubSourceWithResponse(ctx, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy add: %v.\n", problemText(err))
		return exitRun
	}
	if res.JSON200 == nil {
		fmt.Fprintf(stderr, "speccy add: %s\n", problemText(res.ApplicationproblemJSONDefault))
		return exitRun
	}
	if pr := res.JSON200.Source.Pull; pr != nil {
		return printPullSource(res.JSON200, stdout, stderr)
	}
	if server != "" {
		src := res.JSON200.Source
		fmt.Fprintf(stdout, "Reading %s on %s, %s: %d bundle%s.\n", src.Repo, src.Branch, src.Path, src.Bundles, pluralS(src.Bundles))
		if src.Error != "" {
			fmt.Fprintf(stderr, "The first sync failed: %s\n", src.Error)
			return exitRun
		}
		return exitOK
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
	bundles, err := listAll(ctx, client)
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

// printPullSource prints a pull request source: the pull request, and each spec doc with its
// profile.
func printPullSource(res *api.AddedSource, stdout, stderr io.Writer) int {
	src, pr := res.Source, res.Source.Pull
	if res.AlreadyAdded {
		fmt.Fprintf(stdout, "Already added: Speccy follows pull request #%d of %s, %q by %s.\n", pr.Number, src.Repo, pr.Title, pr.Author)
	} else {
		fmt.Fprintf(stdout, "Following pull request #%d of %s, %q by %s.\n", pr.Number, src.Repo, pr.Title, pr.Author)
	}
	if src.Error != "" {
		fmt.Fprintf(stderr, "The first sync failed: %s\n", src.Error)
		return exitRun
	}
	if src.Docs != nil {
		for _, d := range *src.Docs {
			fmt.Fprintf(stdout, "  %s  %s\n", d.Path, d.Profile)
		}
	}
	fmt.Fprintln(stdout, "Review the docs, then prepare a GitHub review from the bundle page or with prepare_github_review.")
	return exitOK
}
