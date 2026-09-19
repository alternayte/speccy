package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/source/local"
)

// runAction is speccy action, the GitHub Action (SDD §12.4, REQ-124). It reviews the bundles
// that a pull request changes, posts or updates one summary comment, posts inline comments on
// changed lines, and sets one check run per bundle. It reads the Actions environment:
// GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_EVENT_PATH, and GITHUB_API_URL.
func runAction(args []string, stdout, stderr io.Writer) int {
	fl, err := parseReviewFlags(append(args, "."))
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n\nUsage: speccy action [--server URL] [--stages …] [--enforcement advisory|blocking]\n", err)
		return exitUsage
	}
	fl.paths = nil
	getenv := os.Getenv
	token, repo := getenv("GITHUB_TOKEN"), getenv("GITHUB_REPOSITORY")
	if token == "" || repo == "" || getenv("GITHUB_EVENT_PATH") == "" {
		fmt.Fprintln(stderr, "speccy action runs in GitHub Actions. It needs GITHUB_TOKEN, GITHUB_REPOSITORY, and GITHUB_EVENT_PATH.")
		return exitUsage
	}
	var event struct {
		PullRequest *struct {
			Number int `json:"number"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	raw, err := os.ReadFile(getenv("GITHUB_EVENT_PATH"))
	if err == nil {
		err = json.Unmarshal(raw, &event)
	}
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: the event file does not read: %v.\n", err)
		return exitUsage
	}
	stages, err := review.ParseStages(fl.stages)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %s\n", problemText(err))
		return exitUsage
	}
	lintOnly := fl.stagesSet && strings.TrimSpace(fl.stages) == review.StageLint
	if lintOnly {
		stages = review.Stages{}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cwd, _ := os.Getwd()
	rootDir := findRoot(cwd)
	cfg, err := source.LoadRepoConfig(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %s is not valid: %v.\n", source.RepoConfigFile, err)
		return exitUsage
	}
	enforcement := fl.enforcement
	if enforcement == "" {
		enforcement = cfg.Enforcement
	}
	if fl.server == "" && cfg.Mode == "connected" {
		fl.server = cfg.Server
	}
	root, err := local.Open(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n", err)
		return exitUsage
	}
	scan, err := root.Scan(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n", err)
		return exitRun
	}
	gh := &github.Client{API: getenv("GITHUB_API_URL"), Token: token}

	// The bundles the pull request changes. With no pull request, every bundle.
	var files []github.PRFile
	selected := scan.Bundles
	if event.PullRequest != nil {
		if files, err = gh.PRFiles(ctx, repo, event.PullRequest.Number); err != nil {
			fmt.Fprintf(stderr, "speccy action: the files of the pull request do not read: %v.\n", err)
			return exitRun
		}
		selected = nil
		seen := map[string]bool{}
		for _, f := range files {
			for _, b := range scan.Bundles {
				if !seen[b.Slug] && bundleMatches(b, f.Filename, false) {
					seen[b.Slug] = true
					selected = append(selected, b)
				}
			}
		}
	}

	var results []reviewed
	var code int
	reports := map[string]string{}
	var s *session
	if fl.server != "" {
		results, code = reviewRemote(ctx, fl, stages, selected, stderr)
	} else {
		if s, err = openSession(ctx, root.Dir()); err != nil {
			fmt.Fprintf(stderr, "speccy action: %v.\n", problemText(err))
			return exitRun
		}
		results, code = reviewWith(ctx, s, fl, stages, lintOnly, selected, stderr)
	}
	if code != exitOK {
		return code
	}

	// Standalone mode writes the HTML reports for the workflow to upload (SDD §12.4).
	runURL := ""
	if s != nil {
		dir := getenv("SPECCY_REPORTS_DIR")
		if dir == "" {
			dir = "speccy-reports"
		}
		if err := os.MkdirAll(dir, 0o755); err == nil {
			bundles, _ := listAll(ctx, s.client)
			for _, b := range bundles {
				f := api.Html
				res, err := s.client.ExportBundleWithResponse(ctx, b.Id, &api.ExportBundleParams{Format: &f})
				if err != nil || res.StatusCode() != 200 || !containsSlug(results, b.Slug) {
					continue
				}
				name := strings.ReplaceAll(b.Slug, "/", "_") + ".html"
				if os.WriteFile(filepath.Join(dir, name), res.Body, 0o644) == nil {
					reports[b.Slug] = name
				}
			}
		}
		if id := getenv("GITHUB_RUN_ID"); id != "" {
			server := getenv("GITHUB_SERVER_URL")
			if server == "" {
				server = "https://github.com"
			}
			runURL = fmt.Sprintf("%s/%s/actions/runs/%s", server, repo, id)
		}
	}

	profiles, _ := profile.LoadLocal(filepath.Join(root.Dir(), ".speccy", "profiles"))
	var bundles []action.Bundle
	for _, r := range results {
		ab := action.Bundle{Slug: r.Path, Profile: r.Profile, Kind: r.Kind, Verdict: r.Verdict, Score: r.Score, Must: r.Must,
			Should: r.Should, Waivers: r.Waivers, Relaxed: r.Relaxed, Error: r.Error, Findings: r.Findings, Files: map[string][]byte{}}
		for _, b := range selected {
			if b.Slug == r.Path {
				ab.Dir, ab.MainDoc = b.Dir, b.Main.Path
				for _, f := range b.Files {
					ab.Files[f.Path] = f.Content
				}
			}
		}
		if p, ok := profiles[r.Profile]; ok {
			ab.Prefixes = p.Profile.Trace.Prefixes
		}
		if _, ok := reports[r.Path]; ok && runURL != "" {
			ab.Report = runURL // the reports are artifacts of the run
		}
		bundles = append(bundles, ab)
	}

	o := action.Options{GitHub: gh, Repo: repo, InlineLimit: cfg.PR.InlineLimit, Blocking: enforcement == "blocking", RunURL: runURL}
	var res action.Result
	if event.PullRequest != nil {
		o.PR, o.HeadSHA = event.PullRequest.Number, event.PullRequest.Head.SHA
		res = action.Run(ctx, o, bundles, files)
	} else {
		fmt.Fprintln(stderr, "This event has no pull request, so Speccy posts no comments.")
		writeResults(stdout, "text", results)
		for _, b := range bundles {
			res.Failed = res.Failed || (o.Blocking && (b.Error != "" || b.Verdict != "build_ready"))
		}
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(stderr, "Warning:", w)
	}
	if res.Summary != "" {
		fmt.Fprintln(stdout, res.Summary)
		if p := getenv("GITHUB_STEP_SUMMARY"); p != "" {
			if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				_, _ = f.WriteString(res.Summary + "\n")
				_ = f.Close()
			}
		}
	}
	fmt.Fprintf(stdout, "Speccy reviewed %d bundle%s, posted %d inline comment%s, and resolved %d.\n",
		len(bundles), pluralS(len(bundles)), res.Posted, pluralS(res.Posted), res.Resolved)
	for _, r := range results {
		if r.Error != "" {
			return exitRun
		}
	}
	if res.Failed {
		return exitNotReady // REQ-125: only in blocking mode
	}
	return exitOK
}

func containsSlug(rs []reviewed, slug string) bool {
	for _, r := range rs {
		if r.Path == slug {
			return true
		}
	}
	return false
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
