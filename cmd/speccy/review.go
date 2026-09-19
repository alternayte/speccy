package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
)

// exitNotReady is SDD §12.2: Not Build Ready in blocking mode.
const exitNotReady = 1

// reviewed is the result of one bundle. The JSON output uses these field names.
type reviewed struct {
	Path     string        `json:"path"`
	Title    string        `json:"title"`
	Profile  string        `json:"profile"`
	MainDoc  string        `json:"main_doc"`
	Kind     string        `json:"kind"` // lint or full
	Verdict  string        `json:"verdict,omitempty"`
	Score    int           `json:"score"`
	Must     int           `json:"must"`
	Should   int           `json:"should"`
	Info     int           `json:"info"`
	Waivers  int           `json:"waivers"`
	Relaxed  int           `json:"relaxed"`
	Findings []api.Finding `json:"findings"`
	Notes    []string      `json:"notes"`
	Error    string        `json:"error,omitempty"`
	// lines maps a file of the bundle to its text, to print line numbers.
	lines map[string][]byte
}

type reviewFlags struct {
	format      string
	summary     bool
	server      string
	stages      string
	stagesSet   bool
	enforcement string
	paths       []string
}

// parseReviewFlags reads the flags of speccy review. Flags may come before or after the paths.
func parseReviewFlags(args []string) (reviewFlags, error) {
	f := reviewFlags{format: "text"}
	value := func(i *int, name string) (string, error) {
		a := args[*i]
		if k, v, ok := strings.Cut(a, "="); ok && strings.TrimLeft(k, "-") == name {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("the flag %s needs a value", a)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			f.paths = append(f.paths, a)
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		var err error
		switch name {
		case "summary":
			f.summary = true
		case "format":
			f.format, err = value(&i, name)
		case "server":
			f.server, err = value(&i, name)
		case "stages":
			f.stages, err = value(&i, name)
			f.stagesSet = true
		case "enforcement":
			f.enforcement, err = value(&i, name)
		default:
			return f, fmt.Errorf("unknown flag %s", a)
		}
		if err != nil {
			return f, err
		}
	}
	switch f.format {
	case "text", "json", "md":
	default:
		return f, fmt.Errorf("--format must be text, json, or md, not %q", f.format)
	}
	switch f.enforcement {
	case "", "advisory", "blocking":
	default:
		return f, fmt.Errorf("--enforcement must be advisory or blocking, not %q", f.enforcement)
	}
	if len(f.paths) == 0 {
		return f, errors.New("name at least one path: a bundle folder, a markdown file, or a folder of bundles")
	}
	return f, nil
}

// runReview is speccy review (SDD §12.2, REQ-121, REQ-134). Exit codes: 0 Build Ready or any
// verdict in advisory mode, 1 Not Build Ready in blocking mode, 2 usage or configuration, 3 a
// run error.
func runReview(args []string, stdout, stderr io.Writer) int {
	fl, err := parseReviewFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n\nUsage: speccy review <path…> [--format text|json|md] [--summary] [--server URL] [--stages lint,rubric,grounding,divergence,coherence] [--enforcement advisory|blocking]\n", err)
		return exitUsage
	}
	stages, err := review.ParseStages(fl.stages)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v\n", problemText(err))
		return exitUsage
	}
	lintOnly := fl.stagesSet && strings.TrimSpace(fl.stages) == review.StageLint
	if lintOnly {
		stages = review.Stages{}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", err)
		return exitUsage
	}
	rootDir := findRoot(cwd)
	cfg, err := source.LoadRepoConfig(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %s is not valid: %v.\n", source.RepoConfigFile, err)
		return exitUsage
	}
	enforcement := fl.enforcement
	if enforcement == "" {
		enforcement = cfg.Enforcement
	}
	if enforcement == "" {
		enforcement = "advisory"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root, err := local.Open(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", err)
		return exitUsage
	}
	// Compare paths with symlinks resolved, as the root is.
	rootDir = root.Dir()
	if c, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = c
	}
	scan, err := root.Scan(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", err)
		return exitRun
	}
	selected, err := selectBundles(scan, rootDir, cwd, fl.paths)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", err)
		return exitUsage
	}
	for _, p := range scan.Problems {
		if len(fl.paths) == 0 || underAny(p.Path, rel(rootDir, cwd, fl.paths)) {
			fmt.Fprintf(stderr, "Not a bundle: %s: %s\n", p.Path, p.Message)
		}
	}

	var results []reviewed
	var code int
	if fl.server != "" {
		results, code = reviewRemote(ctx, fl, stages, selected, stderr)
	} else {
		results, code = reviewLocal(ctx, rootDir, fl, stages, lintOnly, selected, stderr)
	}
	if code != exitOK {
		return code
	}
	for i := range results {
		for _, b := range selected {
			if b.Slug == results[i].Path {
				results[i].lines = map[string][]byte{}
				for _, f := range b.Files {
					results[i].lines[f.Path] = f.Content
				}
			}
		}
	}

	if fl.summary {
		writeSummary(stdout, fl.format, results)
	} else {
		writeResults(stdout, fl.format, results)
	}
	for _, r := range results {
		if r.Error != "" {
			return exitRun
		}
	}
	if enforcement == "blocking" {
		for _, r := range results {
			if r.Verdict != string(api.BuildReady) {
				return exitNotReady
			}
		}
	}
	return exitOK
}

// findRoot is the folder with .speccy.yaml, from dir up; else dir.
func findRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, source.RepoConfigFile)); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// rel returns the paths as slash paths relative to the root.
func rel(rootDir, cwd string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(p) {
			abs = filepath.Join(cwd, p)
		}
		r, err := filepath.Rel(rootDir, abs)
		if err != nil {
			r = p
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if d == "." || p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// selectBundles returns the bundles that the paths name: a folder names every bundle in it,
// a file names the bundle that holds it.
func selectBundles(scan *local.Scan, rootDir, cwd string, paths []string) ([]local.Bundle, error) {
	var out []local.Bundle
	seen := map[string]bool{}
	for i, p := range rel(rootDir, cwd, paths) {
		if strings.HasPrefix(p, "../") {
			return nil, fmt.Errorf("%s is outside the folder with %s (%s)", paths[i], source.RepoConfigFile, rootDir)
		}
		info, err := os.Stat(filepath.Join(rootDir, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("%s does not exist", paths[i])
		}
		found := false
		for _, b := range scan.Bundles {
			if !bundleMatches(b, p, info.IsDir()) {
				continue
			}
			found = true
			if !seen[b.Slug] {
				seen[b.Slug] = true
				out = append(out, b)
			}
		}
		if !found {
			if info.IsDir() {
				return nil, fmt.Errorf("%s has no bundle. A bundle is a folder with one markdown file that has a type in its frontmatter, or a file that a map entry in %s selects", paths[i], source.RepoConfigFile)
			}
			return nil, fmt.Errorf("%s is not in a bundle. Add a type field to its frontmatter, or map it to a profile in %s", paths[i], source.RepoConfigFile)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// bundleMatches reports whether the path p (relative to the root) names bundle b: a folder
// names the bundles in it and the bundle it is inside; a file names the bundle it belongs to.
func bundleMatches(b local.Bundle, p string, dir bool) bool {
	inside := func(prefix string) bool { return prefix != "." && strings.HasPrefix(p, prefix+"/") }
	if dir {
		if p == "." || b.Slug == p || strings.HasPrefix(b.Slug, p+"/") {
			return true
		}
		return b.File == "" && inside(b.Dir)
	}
	if path.Join(b.Dir, b.Main.Path) == p {
		return true
	}
	if b.File == "" {
		return inside(b.Dir)
	}
	assets := path.Join(b.Dir, source.AssetsDir(b.File))
	return p == assets || inside(assets)
}

// reviewLocal reviews the bundles in local mode, in this process.
func reviewLocal(ctx context.Context, rootDir string, fl reviewFlags, stages review.Stages, lintOnly bool, selected []local.Bundle, stderr io.Writer) ([]reviewed, int) {
	s, err := openSession(ctx, rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", problemText(err))
		return nil, exitRun
	}
	// With no reviewer model, the default is lint only (decisions.md). An explicit model stage
	// with no model is a configuration error.
	if !fl.stagesSet {
		if _, err := s.app.Reviews.Gateway.Assigned(ctx, model.RoleReviewer); err != nil {
			fmt.Fprintln(stderr, "No model is assigned, so only lint ran. Assign models in the app (Admin → Models), or pass --stages lint to say so.")
			lintOnly = true
		}
	}
	bundles, err := listAll(ctx, s.client)
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", problemText(err))
		return nil, exitRun
	}
	bySlug := map[string]api.Bundle{}
	for _, b := range bundles {
		bySlug[b.Slug] = b
	}
	var out []reviewed
	runs := map[string]string{} // slug → run to wait for
	for _, lb := range selected {
		b, ok := bySlug[lb.Slug]
		if !ok {
			out = append(out, reviewed{Path: lb.Slug, Error: "Speccy did not load this bundle. Run speccy in the folder to see why."})
			continue
		}
		r := reviewed{Path: b.Slug, Title: b.Title, Profile: b.ProfileKey, MainDoc: path.Join(lb.Dir, b.MainDoc)}
		if b.RunError != nil && b.Verdict == nil {
			r.Error = *b.RunError
			out = append(out, r)
			continue
		}
		if !lintOnly {
			body := api.StartRunJSONRequestBody{}
			if stages != nil {
				st := make([]api.StartRunRequestStages, len(stages))
				for i, x := range stages {
					st[i] = api.StartRunRequestStages(x)
				}
				body.Stages = &st
			}
			res, err := s.client.StartRunWithResponse(ctx, b.Id, body)
			if err != nil {
				fmt.Fprintf(stderr, "speccy review: %v.\n", err)
				return nil, exitRun
			}
			if res.JSON202 == nil {
				return nil, problemExit(stderr, b.Slug, res.ApplicationproblemJSONDefault)
			}
			runs[b.Slug] = res.JSON202.Id.String()
		}
		out = append(out, r)
	}
	for i := range out {
		if out[i].Error != "" {
			continue
		}
		b := bySlug[out[i].Path]
		runID := ""
		if id, ok := runs[b.Slug]; ok {
			runID = id
		} else if b.Verdict != nil {
			runID = b.Verdict.RunId.String()
		}
		if runID == "" {
			out[i].Error = "The bundle has no review."
			continue
		}
		if err := fillRun(ctx, s.client, runID, &out[i], stderr); err != nil {
			fmt.Fprintf(stderr, "speccy review: %v.\n", err)
			return nil, exitRun
		}
	}
	return out, exitOK
}

// fillRun waits for a run to end and copies its verdict and findings.
func fillRun(ctx context.Context, c *api.ClientWithResponses, runID string, r *reviewed, stderr io.Writer) error {
	id, err := parseUUID(runID)
	if err != nil {
		return err
	}
	var run *api.Run
	for {
		res, err := c.GetRunWithResponse(ctx, id)
		if err != nil {
			return err
		}
		if res.JSON200 == nil {
			return errors.New(problemText(res.ApplicationproblemJSONDefault))
		}
		run = res.JSON200
		if run.Status == api.Complete || run.Status == api.Failed {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	r.Kind = string(run.Kind)
	if run.Notes != nil {
		r.Notes = *run.Notes
	}
	if run.Status == api.Failed {
		r.Error = run.Error
		return nil
	}
	if v := run.Verdict; v != nil {
		r.Verdict, r.Score, r.Must, r.Should, r.Info = string(v.Result), v.Score, v.Must, v.Should, v.Info
		r.Waivers, r.Relaxed = v.WaiverCount, v.RelaxedCount
	}
	fs, err := c.ListFindingsWithResponse(ctx, id)
	if err != nil {
		return err
	}
	if fs.JSON200 == nil {
		return errors.New(problemText(fs.ApplicationproblemJSONDefault))
	}
	r.Findings = fs.JSON200.Items
	return nil
}

// reviewRemote sends each bundle's files to a server, which reviews them without saving them.
func reviewRemote(ctx context.Context, fl reviewFlags, stages review.Stages, selected []local.Bundle, stderr io.Writer) ([]reviewed, int) {
	c, err := remoteClient(strings.TrimRight(fl.server, "/"))
	if err != nil {
		fmt.Fprintf(stderr, "speccy review: %v.\n", err)
		return nil, exitUsage
	}
	var out []reviewed
	for _, b := range selected {
		body := api.ReviewContentJSONRequestBody{Slug: &b.Slug}
		if b.File != "" {
			body.MainDoc = &b.File
			if b.Main.Frontmatter.Type != "" {
				body.Profile = &b.Main.Frontmatter.Type
			}
		}
		if stages != nil {
			st := make([]api.ContentReviewRequestStages, len(stages))
			for i, x := range stages {
				st[i] = api.ContentReviewRequestStages(x)
			}
			body.Stages = &st
		}
		for _, f := range b.Files {
			cf := api.ContentFile{Path: f.Path, Content: string(f.Content)}
			if !isText(f.Content) {
				enc := api.ContentFileEncodingBase64
				cf.Content, cf.Encoding = encodeBase64(f.Content), &enc
			}
			body.Files = append(body.Files, cf)
		}
		res, err := c.ReviewContentWithResponse(ctx, body)
		if err != nil {
			fmt.Fprintf(stderr, "speccy review: %s: %v.\n", fl.server, err)
			return nil, exitRun
		}
		if res.JSON200 == nil {
			return nil, problemExit(stderr, b.Slug, res.ApplicationproblemJSONDefault)
		}
		v := res.JSON200
		kind := "full"
		if stages != nil && len(stages) == 0 {
			kind = "lint"
		}
		out = append(out, reviewed{
			Path: b.Slug, Title: b.Main.Title, Profile: v.ProfileKey, MainDoc: path.Join(b.Dir, v.MainDoc), Kind: kind,
			Verdict: string(v.Verdict.Result), Score: v.Verdict.Score, Must: v.Verdict.Must, Should: v.Verdict.Should, Info: v.Verdict.Info,
			Waivers: v.Verdict.WaiverCount, Relaxed: v.Verdict.RelaxedCount, Findings: v.Findings, Notes: v.Notes,
		})
	}
	return out, exitOK
}

// listAll pages through the bundles.
func listAll(ctx context.Context, c *api.ClientWithResponses) ([]api.Bundle, error) {
	var out []api.Bundle
	var cursor *api.Cursor
	limit := api.Limit(100)
	for {
		res, err := c.ListBundlesWithResponse(ctx, &api.ListBundlesParams{Cursor: cursor, Limit: &limit})
		if err != nil {
			return nil, err
		}
		if res.JSON200 == nil {
			return nil, errors.New(problemText(res.ApplicationproblemJSONDefault))
		}
		out = append(out, res.JSON200.Items...)
		if res.JSON200.NextCursor == nil || *res.JSON200.NextCursor == "" {
			return out, nil
		}
		next := api.Cursor(*res.JSON200.NextCursor)
		cursor = &next
	}
}

// problemExit prints an API error and returns its exit code: a problem with the setup is 2;
// the rest (budget, backend, timeout) is 3.
func problemExit(stderr io.Writer, slug string, p *api.Problem) int {
	fmt.Fprintf(stderr, "speccy review: %s: %s\n", slug, problemText(p))
	if p != nil && (p.Status == 400 || p.Status == 401 || p.Status == 403 || p.Status == 404 || p.Status == 422) {
		return exitUsage
	}
	return exitRun
}

func problemText(err any) string {
	switch e := err.(type) {
	case *api.Problem:
		if e == nil || e.Detail == nil {
			return "The server gave an error with no detail."
		}
		return *e.Detail
	case interface{ Error() string }:
		msg := e.Error()
		if d := detailOf(err); d != "" {
			msg = d
		}
		return msg
	}
	return fmt.Sprint(err)
}

// detailOf is the user-facing text of a Speccy error.
func detailOf(err any) string {
	if e, ok := err.(error); ok {
		if ke, ok := kernel.AsError(e); ok {
			return ke.Detail
		}
	}
	return ""
}

func parseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }

// isText reports whether content is UTF-8 text with no NUL byte.
func isText(content []byte) bool {
	return utf8.Valid(content) && !bytes.Contains(content, []byte{0})
}

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func sortedCounts(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}
		return strings.Compare(a, b)
	})
	return keys
}
