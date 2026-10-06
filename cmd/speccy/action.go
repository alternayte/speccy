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

// actionEvent is the part of the GitHub event file the Action reads.
type actionEvent struct {
	PullRequest *struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
}

// actionRepo is the repo of the checkout that speccy action reviews in CI. The store that this
// process opens reads a link rule target that is not in the tree from the open pull requests
// of that repo (#142). Every other command leaves it nil: a folder on a laptop names no repo
// that Speccy can trust.
var actionRepo *review.FolderRepo

// runAction is speccy action, the GitHub Action (SDD §12.4, REQ-124). It reviews the bundles
// that a pull request changes, posts or updates one summary comment, posts inline comments on
// changed lines, and sets one check run per bundle. It reads the Actions environment:
// GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_EVENT_PATH, and GITHUB_API_URL.
// prNumber and headSHA read the pull request of the event, or zero values with none.
func prNumber(e actionEvent) int {
	if e.PullRequest == nil {
		return 0
	}
	return e.PullRequest.Number
}

func headSHA(e actionEvent) string {
	if e.PullRequest == nil {
		return ""
	}
	return e.PullRequest.Head.SHA
}

func runAction(args []string, stdout, stderr io.Writer) int {
	// --verify runs the post-build verification gate instead of the review (the spec contract
	// gate). It reads the same Actions environment.
	verifyMode := false
	// --pr reviews a pull request from a laptop, and --pending or --dry-run says what to do with
	// the findings (#91).
	prURL, pending, dryRun, prMode := "", false, false, false
	// --levels and --no-attribution shape the pending review of --pr (#131, #140).
	var pf prFlagsAction
	var kept []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--verify":
			verifyMode = true
		case a == "--pending":
			pending = true
		case a == "--dry-run":
			dryRun = true
		case a == "--no-attribution":
			pf.noAttribution = true
		case a == "--levels" && i+1 < len(args):
			i++
			pf.levels, pf.levelsSet = args[i], true
		case a == "--levels":
			pf.levelsSet = true
		case strings.HasPrefix(a, "--levels="):
			pf.levels, pf.levelsSet = strings.TrimPrefix(a, "--levels="), true
		case a == "--pr" && i+1 < len(args):
			i++
			prURL, prMode = args[i], true
		case a == "--pr":
			prMode = true
		case strings.HasPrefix(a, "--pr="):
			prURL, prMode = strings.TrimPrefix(a, "--pr="), true
		default:
			kept = append(kept, a)
		}
	}
	args = kept
	fl, err := parseReviewFlags(append(args, "."))
	if err != nil {
		fmt.Fprintf(stderr, "speccy action: %v.\n\nUsage: speccy action [--server URL] [--stages …] [--enforcement advisory|blocking]\n       %s", err, actionPRUsage)
		return exitUsage
	}
	fl.paths = nil
	if prMode || pending || dryRun {
		return runActionPR(prURL, pending, dryRun, fl, pf, stdout, stderr)
	}
	if pf.noAttribution || pf.levelsSet {
		fmt.Fprintf(stderr, "speccy action: --levels and --no-attribution work with --pr only. In CI, set pr.levels in %s.\n", source.RepoConfigFile)
		return exitUsage
	}
	getenv := os.Getenv
	token, repo := getenv("GITHUB_TOKEN"), getenv("GITHUB_REPOSITORY")
	if token == "" || repo == "" || getenv("GITHUB_EVENT_PATH") == "" {
		fmt.Fprintln(stderr, "speccy action runs in GitHub Actions. It needs GITHUB_TOKEN, GITHUB_REPOSITORY, and GITHUB_EVENT_PATH.")
		return exitUsage
	}
	var event actionEvent
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
				if !seen[b.Slug] && b.Names(f.Filename, false) {
					seen[b.Slug] = true
					selected = append(selected, b)
				}
			}
		}
	}

	if verifyMode {
		return runActionVerify(ctx, fl, gh, repo, event.PullRequest != nil, prNumber(event), headSHA(event),
			selected, files, cfg, enforcement, root.Dir(), stdout, stderr)
	}

	var results []reviewed
	var code int
	reports := map[string]string{}
	var s *session
	if fl.server != "" {
		results, code = reviewRemote(ctx, fl, stages, selected, stderr)
	} else {
		actionRepo = &review.FolderRepo{Client: gh, Repo: repo, Dir: root.Dir(), Pull: prNumber(event)}
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
			ab.Prefixes, ab.CoverPrefixes = p.Profile.Trace.Prefixes, p.Profile.Trace.Cover
			ab.Checks = p.Profile
		}
		if _, ok := reports[r.Path]; ok && runURL != "" {
			ab.Report = runURL // the reports are artifacts of the run
		}
		if r.Report != "" {
			ab.Report = r.Report // connected mode: the report on the server
		}
		// The links that the review read from open pull requests (#142), as the run stored them.
		if s != nil && r.DocID != "" && r.Error == "" {
			if id, err := parseUUID(r.DocID); err == nil {
				tr, err := s.client.GetTraceWithResponse(ctx, id)
				switch {
				case err != nil:
					fmt.Fprintf(stderr, "Warning: the links of %s do not read: %v.\n", r.Path, err)
				case tr.JSON200 == nil:
					fmt.Fprintf(stderr, "Warning: the links of %s do not read: %s.\n", r.Path, problemText(tr.ApplicationproblemJSONDefault))
				default:
					ab.Upstream = action.UpstreamOf(tr.JSON200.Links)
				}
			}
		}
		bundles = append(bundles, ab)
	}

	o := action.Options{GitHub: gh, Repo: repo, InlineLimit: cfg.PR.InlineLimit, Levels: cfg.PR.Levels, Blocking: enforcement == "blocking", RunURL: runURL}
	// #140: attribution none is for a reviewer's pending review. The Action posts as its own
	// bot, and its hidden markers find its threads on the next push, with no state between runs.
	if cfg.PR.Attribution == source.AttributionNone {
		fmt.Fprintf(stderr, "Note: pr.attribution: none in %s applies to pending reviews only. The Action posts as its own bot and keeps its markers.\n", source.RepoConfigFile)
	}
	// Adoption mode: which relaxed checks now pass on every mapped doc (REQ-133). The sweep
	// lints the docs this pull request did not change, and makes no model call.
	o.Relaxed = cfg.Adoption.Relaxed
	if len(o.Relaxed) > 0 {
		o.Config, _ = os.ReadFile(filepath.Join(root.Dir(), source.RepoConfigFile))
		o.Ready = readyToEnforce(ctx, s, fl, root.Dir(), o.Relaxed, scan.Bundles, results, stderr)
	}
	// The sidecars sit at the root of the checkout, where git sees them (DEC-009).
	o.Sidecar = func(doc string) (source.Decisions, error) {
		raw, err := os.ReadFile(filepath.Join(root.Dir(), filepath.FromSlash(source.SidecarPath(doc))))
		if os.IsNotExist(err) {
			return source.Decisions{}, nil
		}
		if err != nil {
			return source.Decisions{}, err
		}
		return source.ParseDecisions(raw)
	}
	var res action.Result
	if event.PullRequest != nil {
		o.PR, o.HeadSHA = event.PullRequest.Number, event.PullRequest.Head.SHA
		// The branch to commit a decision to, and the branch that says which waivers are merged.
		if pr, err := gh.PullRequest(ctx, repo, o.PR); err != nil {
			fmt.Fprintf(stderr, "Warning: the pull request does not read, so Speccy records no decision: %v\n", err)
			o.Sidecar = nil
		} else {
			o.HeadRef, o.BaseRef, o.Fork = pr.HeadRef, pr.BaseRef, pr.Fork(repo)
		}
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
	fmt.Fprintf(stdout, "Speccy reviewed %d bundle%s, posted %d inline comment%s, resolved %d, and recorded %d decision%s.\n",
		len(bundles), pluralS(len(bundles)), res.Posted, pluralS(res.Posted), res.Resolved, res.Decided, pluralS(res.Decided))
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

// readyToEnforce returns the relaxed checks with no finding on any mapped doc. It reuses the
// results of this run and lints the rest of the repo, which lint does in well under a second
// for each doc (T-081). With no local session (connected mode) it offers nothing.
func readyToEnforce(ctx context.Context, s *session, fl reviewFlags, rootDir string, relaxed []string,
	all []local.Bundle, done []reviewed, stderr io.Writer) []string {
	if s == nil {
		return nil
	}
	failing := map[string]bool{}
	seen := map[string]bool{}
	for _, r := range done {
		seen[r.Path] = true
		for _, f := range r.Findings {
			failing[f.CheckSlug] = true
		}
	}
	var rest []local.Bundle
	for _, b := range all {
		if !seen[b.Slug] {
			rest = append(rest, b)
		}
	}
	if len(rest) > 0 {
		sweep := reviewFlags{format: "text", stages: review.StageLint, stagesSet: true, root: rootDir}
		results, code := reviewWith(ctx, s, sweep, review.Stages{}, true, rest, io.Discard)
		if code != exitOK {
			fmt.Fprintln(stderr, "Warning: the adoption mode sweep did not finish, so Speccy offers no check to enforce.")
			return nil
		}
		for _, r := range results {
			for _, f := range r.Findings {
				failing[f.CheckSlug] = true
			}
		}
	}
	var out []string
	for _, slug := range relaxed {
		if !failing[slug] {
			out = append(out, slug)
		}
	}
	return out
}
