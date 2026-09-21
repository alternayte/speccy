package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
)

// workflowPath is the workflow that speccy init --github writes.
const workflowPath = ".github/workflows/speccy.yml"

// workflowFile needs no secret: lint checks only, advisory, on the docs a mapping names.
const workflowFile = `# Speccy reviews the specs that a pull request changes (SDD §12.4).
name: Speccy
on:
  pull_request:
    paths: ["**/*.md", ".speccy.yaml"]
permissions:
  contents: write        # commit a decision that a reply asked for
  pull-requests: write   # the summary and inline comments
  checks: write          # one check per bundle
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: alternayte/speccy@v0.1.0
        # Lint checks only. For the model stages, set a key and pass it here:
        # with:
        #   models: all=anthropic:<model>
        #   anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
`

// initGitHub is speccy init --github: the one command that adopts a repo Speccy did not
// write. It guesses a profile for each loose markdown doc, writes the path mappings, runs a
// lint pass to find the checks that fail today, puts them in adoption mode, and writes the
// workflow. The first comment on a pull request then names the checks the team opted into,
// not every finding in the repo (REQ-130, REQ-133).
func initGitHub(dir string, stdout, stderr io.Writer) int {
	profiles, err := profile.LoadLocal(filepath.Join(dir, ".speccy", "profiles"))
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitUsage
	}
	versioned := map[string]profile.Versioned{}
	for k, p := range profiles {
		versioned[k] = profile.Versioned{Loaded: p}
	}
	docs, err := guessDocs(dir, versioned)
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitRun
	}
	if len(docs) == 0 {
		fmt.Fprintln(stdout, "Speccy found no markdown file that reads like a spec. Add \"type: <profile>\" to one, or write a mapping by hand.")
		return exitOK
	}
	mappings := mapDocs(dir, docs)
	cfgPath := filepath.Join(dir, source.RepoConfigFile)
	if err := os.WriteFile(cfgPath, []byte(repoConfigFor(mappings, nil)), 0o644); err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitRun
	}
	fmt.Fprintf(stdout, "Wrote %s: %d mapping%s for %d doc%s.\n", source.RepoConfigFile,
		len(mappings), pluralS(len(mappings)), len(docs), pluralS(len(docs)))

	relaxed, err := failingChecks(dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: the first review did not run, so no check is relaxed: %v.\n", err)
	} else if len(relaxed) > 0 {
		if err := os.WriteFile(cfgPath, []byte(repoConfigFor(mappings, relaxed)), 0o644); err != nil {
			fmt.Fprintf(stderr, "speccy init: %v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "Adoption mode: %d check%s fail today, so they report as INFO: %s.\n",
			len(relaxed), pluralS(len(relaxed)), strings.Join(relaxed, ", "))
		fmt.Fprintln(stdout, "A reply of /speccy enforce <slug> in a pull request turns one back on.")
	}

	if _, err := os.Stat(filepath.Join(dir, workflowPath)); err == nil {
		fmt.Fprintf(stdout, "%s exists. Speccy did not change it.\n", workflowPath)
	} else {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(path.Dir(workflowPath))), 0o755); err != nil {
			fmt.Fprintf(stderr, "speccy init: %v.\n", err)
			return exitRun
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(workflowPath)), []byte(workflowFile), 0o644); err != nil {
			fmt.Fprintf(stderr, "speccy init: %v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "Wrote %s. It needs no secret and fails no job. The comment says what the model stages add.\n", workflowPath)
	}
	if added, err := ensureIgnored(filepath.Join(dir, ".gitignore"), ".speccy/state/"); err == nil && added {
		fmt.Fprintln(stdout, "Added .speccy/state/ to .gitignore. It holds the local database and key.")
	}
	fmt.Fprintln(stdout, "Open a pull request with these files. Speccy reviews the specs it changes.")
	return exitOK
}

// guessed is one doc and the profile its headings point at.
type guessed struct {
	path    string // relative to the repo, with /
	profile string
}

// guessDocs finds the markdown files of the repo that read like a spec, with the guess the
// import dialog uses. A file that already names a type needs no mapping.
func guessDocs(dir string, profiles map[string]profile.Versioned) ([]guessed, error) {
	var out []guessed
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !source.IsMarkdown(name) || source.NeverASpec[strings.ToLower(name)] {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if fm, _, err := source.ReadFrontmatter(content); err == nil && fm.Type != "" {
			return nil
		}
		key, ok := profile.Guess(profiles, content)
		if !ok {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, guessed{path: filepath.ToSlash(rel), profile: key})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, err
}

// mapDocs turns the guesses into path mappings. A folder whose markdown files all guess the
// same profile gets one glob for the folder; anything else gets one entry per file, so a
// mapping never takes in a doc that Speccy did not look at.
func mapDocs(dir string, docs []guessed) []source.Mapping {
	byDir := map[string][]guessed{}
	for _, g := range docs {
		d := path.Dir(g.path)
		byDir[d] = append(byDir[d], g)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var out []source.Mapping
	for _, d := range dirs {
		gs := byDir[d]
		same := true
		for _, g := range gs {
			same = same && g.profile == gs[0].profile
		}
		if same && len(gs) == markdownCount(dir, d) {
			glob := "*.md"
			if d != "." {
				glob = d + "/*.md"
			}
			out = append(out, source.Mapping{Glob: glob, Profile: gs[0].profile})
			continue
		}
		for _, g := range gs {
			out = append(out, source.Mapping{Glob: g.path, Profile: g.profile})
		}
	}
	return out
}

// markdownCount is the number of markdown files directly in the folder that could be specs.
func markdownCount(root, dir string) int {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return -1
	}
	n := 0
	for _, e := range entries {
		if e.Type().IsRegular() && source.IsMarkdown(e.Name()) && !source.NeverASpec[strings.ToLower(e.Name())] {
			n++
		}
	}
	return n
}

// repoConfigFor is the .speccy.yaml of an adopted repo.
func repoConfigFor(mappings []source.Mapping, relaxed []string) string {
	var b strings.Builder
	b.WriteString("# Speccy repo configuration, written by speccy init --github. The format is in SDD §10.4.\n\n" +
		"# Each markdown file that matches a glob is a bundle, reviewed with that profile.\n" +
		"# A frontmatter type in the file wins over the mapping.\nmap:\n")
	for _, m := range mappings {
		fmt.Fprintf(&b, "  - glob: %q\n    profile: %s\n", m.Glob, m.Profile)
	}
	b.WriteString("\n# Links by path convention: \"<from> <kind> <to>\", with {name} matching in both.\n" +
		"# link_rules:\n#   - \"docs/sdd-{name}.md implements docs/prd-{name}.md\"\n")
	b.WriteString("\n# Adoption mode: these checks report as INFO until a maintainer removes them.\n" +
		"# A reply of /speccy enforce <slug> in a pull request removes one.\n")
	if len(relaxed) == 0 {
		b.WriteString("# adoption:\n#   relaxed: [links.has-upstream]\n")
		return b.String()
	}
	b.WriteString("adoption:\n  relaxed:\n")
	for _, s := range relaxed {
		fmt.Fprintf(&b, "    - %s\n", s)
	}
	return b.String()
}

// failingChecks lints every mapped doc and returns the check slugs that fail on at least one
// of them, so the first verdict of an adopted repo is not a wall of findings (REQ-133).
func failingChecks(dir string, stderr io.Writer) ([]string, error) {
	// The store closes when ctx ends, so the temporary one of this pass leaves nothing behind.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg, err := source.LoadRepoConfig(dir)
	if err != nil {
		return nil, err
	}
	root, err := local.Open(dir)
	if err != nil {
		return nil, err
	}
	scan, err := root.Scan(cfg)
	if err != nil {
		return nil, err
	}
	s, err := openSession(ctx, root.Dir())
	if err != nil {
		return nil, err
	}
	fl := reviewFlags{format: "text", stages: review.StageLint, stagesSet: true, root: root.Dir()}
	results, code := reviewWith(ctx, s, fl, review.Stages{}, true, scan.Bundles, io.Discard)
	if code != exitOK {
		return nil, fmt.Errorf("the lint pass ended with code %d", code)
	}
	failing := map[string]bool{}
	for _, r := range results {
		for _, f := range r.Findings {
			if f.Level != api.FindingLevelINFO {
				failing[f.CheckSlug] = true
			}
		}
	}
	out := make([]string, 0, len(failing))
	for s := range failing {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}
