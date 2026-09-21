package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
)

// runProfile is speccy profile validate <file> (REQ-014). It prints each schema error with its
// path and exits 2 when the profile is not valid.
func runProfile(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "validate" {
		fmt.Fprint(stderr, "Usage: speccy profile validate <file>\n")
		return exitUsage
	}
	l, err := profile.ParseFile(args[1])
	if err != nil {
		var ve *profile.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(stderr, "%s is not a valid profile:\n", args[1])
			for _, e := range ve.Errors {
				fmt.Fprintf(stderr, "  %s\n", e)
			}
			return exitUsage
		}
		fmt.Fprintf(stderr, "speccy profile validate: %v.\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "%s is a valid profile: %s (%s), %d checks.\n", args[1], l.Profile.Key, l.Profile.Name, len(l.Profile.Checks))
	return exitOK
}

// runExport is speccy export <path> --format zip|html (REQ-008). It writes the file into the
// current folder and prints its name.
func runExport(args []string, stdout, stderr io.Writer) int {
	format := "zip"
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--format" || a == "-format":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "speccy export: the flag --format needs a value.")
				return exitUsage
			}
			i++
			format = args[i]
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "speccy export: unknown flag %s.\n", a)
			return exitUsage
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) != 1 || (format != "zip" && format != "html") {
		fmt.Fprint(stderr, "Usage: speccy export <path> --format zip|html\n")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %v.\n", err)
		return exitUsage
	}
	rootDir := findRoot(cwd)
	cfg, err := source.LoadRepoConfig(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %s is not valid: %v.\n", source.RepoConfigFile, err)
		return exitUsage
	}
	root, err := local.Open(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %v.\n", err)
		return exitUsage
	}
	if c, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = c
	}
	scan, err := root.Scan(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %v.\n", err)
		return exitRun
	}
	selected, err := selectBundles(scan, root.Dir(), cwd, paths)
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %v.\n", err)
		return exitUsage
	}
	if len(selected) != 1 {
		fmt.Fprintf(stderr, "speccy export: %s holds %d bundles. Name one bundle.\n", paths[0], len(selected))
		return exitUsage
	}
	s, err := openSession(ctx, root.Dir())
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %v.\n", problemText(err))
		return exitRun
	}
	bundles, err := listAll(ctx, s.client)
	if err != nil {
		fmt.Fprintf(stderr, "speccy export: %v.\n", problemText(err))
		return exitRun
	}
	for _, b := range bundles {
		if b.Slug != selected[0].Slug {
			continue
		}
		f := api.ExportBundleParamsFormat(format)
		res, err := s.client.ExportBundleWithResponse(ctx, b.Id, &api.ExportBundleParams{Format: &f})
		if err != nil {
			fmt.Fprintf(stderr, "speccy export: %v.\n", err)
			return exitRun
		}
		if res.StatusCode() != 200 {
			fmt.Fprintf(stderr, "speccy export: %s\n", problemText(res.ApplicationproblemJSONDefault))
			return exitRun
		}
		name := attachmentName(res.HTTPResponse.Header.Get("Content-Disposition"), "bundle."+format)
		if err := os.WriteFile(name, res.Body, 0o644); err != nil {
			fmt.Fprintf(stderr, "speccy export: %v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "Wrote %s.\n", name)
		return exitOK
	}
	fmt.Fprintf(stderr, "speccy export: Speccy did not load %s. Run speccy in the folder to see why.\n", selected[0].Slug)
	return exitRun
}

var filenameRe = regexp.MustCompile(`filename="([^"/\\]+)"`)

func attachmentName(header, fallback string) string {
	if m := filenameRe.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return fallback
}

// defaultRepoConfig is the .speccy.yaml that speccy init writes (SDD §10.4).
const defaultRepoConfig = `# Speccy repo configuration. The format is in SDD §10.4.

# Folder bundles: a folder with one markdown file that has a type field in its frontmatter.
# With no entry here, every such folder is a bundle.
# bundles:
#   - path: docs/specs/*

# Single-file bundles: markdown files with no frontmatter, reviewed with a profile.
# map:
#   - glob: docs/**/prd-*.md
#     profile: prd
#   - glob: docs/**/sdd-*.md
#     profile: sdd

# Links by path convention: "<from> <kind> <to>", with {name} matching in both.
# link_rules:
#   - "docs/sdd-{name}.md implements docs/prd-{name}.md"

# Adoption mode: these checks report as INFO until you remove them from the list.
# adoption:
#   relaxed: [links.has-upstream]

mode: standalone        # standalone | connected
enforcement: advisory   # advisory | blocking
`

// runInit is speccy init (SDD §12.2): it writes .speccy.yaml, adds .speccy/state/ to
// .gitignore, and offers to turn loose markdown files into bundles.
func runInit(args []string, stdin io.Reader, stdout, stderr io.Writer, interactive bool) int {
	forGitHub := false
	for _, a := range args {
		if a == "--github" {
			forGitHub = true
			continue
		}
		fmt.Fprint(stderr, "Usage: speccy init [--github]\n")
		return exitUsage
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitUsage
	}
	if forGitHub {
		return initGitHub(dir, stdout, stderr)
	}
	cfgPath := filepath.Join(dir, source.RepoConfigFile)
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Fprintf(stdout, "%s exists. Speccy did not change it.\n", source.RepoConfigFile)
	} else {
		if err := os.WriteFile(cfgPath, []byte(defaultRepoConfig), 0o644); err != nil {
			fmt.Fprintf(stderr, "speccy init: %v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "Wrote %s.\n", source.RepoConfigFile)
	}
	added, err := ensureIgnored(filepath.Join(dir, ".gitignore"), ".speccy/state/")
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitRun
	}
	if added {
		fmt.Fprintln(stdout, "Added .speccy/state/ to .gitignore. It holds the local database and key.")
	}

	loose, err := looseDocs(dir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitRun
	}
	if len(loose) == 0 {
		return exitOK
	}
	profiles, err := profile.LoadLocal(filepath.Join(dir, ".speccy", "profiles"))
	if err != nil {
		fmt.Fprintf(stderr, "speccy init: %v.\n", err)
		return exitUsage
	}
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !interactive {
		fmt.Fprintf(stdout, "These markdown files are not bundles: %s. To review one, add \"type: <%s>\" to its frontmatter, map it in %s, or adopt it in the app.\n",
			strings.Join(loose, ", "), strings.Join(keys, "|"), source.RepoConfigFile)
		return exitOK
	}
	versioned := map[string]profile.Versioned{}
	for k, p := range profiles {
		versioned[k] = profile.Versioned{Loaded: p}
	}
	in := bufio.NewScanner(stdin)
	for _, f := range loose {
		// The guess is the default answer, as it is in the app: Enter accepts it.
		guess := ""
		if content, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			if k, ok := profile.Guess(versioned, content); ok {
				guess = k
			}
		}
		if guess != "" {
			fmt.Fprintf(stdout, "Turn %s into a bundle? Press Enter for %s, type another profile (%s), or type - to skip: ", f, guess, strings.Join(keys, ", "))
		} else {
			fmt.Fprintf(stdout, "Turn %s into a bundle? Type a profile (%s), or press Enter to skip: ", f, strings.Join(keys, ", "))
		}
		if !in.Scan() {
			fmt.Fprintln(stdout)
			return exitOK
		}
		answer := strings.TrimSpace(in.Text())
		if answer == "-" {
			continue
		}
		if answer == "" {
			if guess == "" {
				continue
			}
			answer = guess
		}
		if _, ok := profiles[answer]; !ok {
			fmt.Fprintf(stdout, "There is no profile %q. Speccy skipped %s.\n", answer, f)
			continue
		}
		if err := addType(filepath.Join(dir, f), answer); err != nil {
			fmt.Fprintf(stderr, "speccy init: %v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "%s is now a %s bundle.\n", f, strings.ToUpper(answer))
	}
	return exitOK
}

// ensureIgnored adds line to the .gitignore at p when no line has it. added is false when it
// was there.
func ensureIgnored(p, line string) (added bool, err error) {
	src, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(src), "\n") {
		l = strings.TrimSpace(l)
		if l == line || l == strings.TrimSuffix(line, "/") || l == "/"+line {
			return false, nil
		}
	}
	if len(src) > 0 && !bytes.HasSuffix(src, []byte("\n")) {
		src = append(src, '\n')
	}
	src = append(src, []byte(line+"\n")...)
	return true, os.WriteFile(p, src, 0o644)
}

// looseDocs lists the markdown files directly in dir with no frontmatter type.
func looseDocs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !source.IsMarkdown(name) || strings.HasPrefix(name, ".") {
			continue
		}
		if source.NeverASpec[strings.ToLower(name)] {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		fm, _, err := source.ReadFrontmatter(content)
		if err == nil && fm.Type == "" {
			out = append(out, name)
		}
	}
	return out, nil
}

// addType sets the frontmatter type of the markdown file at p: a new frontmatter block, or a
// type line at the top of the one it has.
func addType(p, key string) error {
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return os.WriteFile(p, source.AddTypeLine(src, key), 0o644)
}
