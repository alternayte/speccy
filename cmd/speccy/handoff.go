package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/features/handoff"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
)

// runHandoff writes the build packet of one bundle as a folder, so a coding agent opens
// files (REQ-136). The same packet comes back inline from the MCP tool.
func runHandoff(args []string, stdout, stderr io.Writer) int {
	out, label := "", ""
	ack := false
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func(name string) (string, bool) {
			if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
				return v, true
			}
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "speccy handoff: the flag --%s needs a value.\n", name)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case a == "--out" || strings.HasPrefix(a, "--out="):
			v, ok := value("out")
			if !ok {
				return exitUsage
			}
			out = v
		case a == "--label" || strings.HasPrefix(a, "--label="):
			v, ok := value("label")
			if !ok {
				return exitUsage
			}
			label = v
		case a == "--acknowledged":
			ack = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "speccy handoff: unknown flag %s.\n", a)
			return exitUsage
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, "Usage: speccy handoff <path> --out <folder> [--label <name>] [--acknowledged]\n")
		return exitUsage
	}
	if out == "" {
		out = "handoff"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, s, code := oneBundle(ctx, "handoff", paths, stderr)
	if code != exitOK {
		return code
	}
	body := api.TakeHandoffJSONRequestBody{}
	if label != "" {
		body.Label = &label
	}
	if ack {
		body.Acknowledged = &ack
	}
	res, err := s.client.TakeHandoffWithResponse(ctx, b.Id, body)
	if err != nil {
		fmt.Fprintf(stderr, "speccy handoff: %v.\n", err)
		return exitRun
	}
	if res.JSON200 == nil {
		fmt.Fprintf(stderr, "speccy handoff: %s\n", problemText(res.ApplicationproblemJSONDefault))
		return exitRun
	}
	if err := writePacket(out, *res.JSON200); err != nil {
		fmt.Fprintf(stderr, "speccy handoff: %v.\n", err)
		return exitRun
	}
	p := *res.JSON200
	fmt.Fprintf(stdout, "Wrote the build packet of %s version %d to %s.\n", p.Bundle, p.VersionNumber, out)
	fmt.Fprintf(stdout, "Start the build from %s.\n", filepath.Join(out, handoff.File))
	return exitOK
}

// writePacket writes the packet as the folder dir: the files handoff.Files names, which are
// also the files of the .zip the app downloads.
func writePacket(dir string, p api.BuildPacket) error {
	files, err := handoff.Files(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, f.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// oneBundle resolves the paths to exactly one saved bundle, in an open local session.
func oneBundle(ctx context.Context, cmd string, paths []string, stderr io.Writer) (api.SpecDoc, *session, int) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %v.\n", cmd, err)
		return api.SpecDoc{}, nil, exitUsage
	}
	rootDir := findRoot(cwd)
	cfg, err := source.LoadRepoConfig(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %s is not valid: %v.\n", cmd, source.RepoConfigFile, err)
		return api.SpecDoc{}, nil, exitUsage
	}
	root, err := local.Open(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %v.\n", cmd, err)
		return api.SpecDoc{}, nil, exitUsage
	}
	if c, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = c
	}
	scan, err := root.Scan(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %v.\n", cmd, err)
		return api.SpecDoc{}, nil, exitRun
	}
	selected, err := selectBundles(scan, root.Dir(), cwd, paths)
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %v.\n", cmd, err)
		return api.SpecDoc{}, nil, exitUsage
	}
	if len(selected) != 1 {
		fmt.Fprintf(stderr, "speccy %s: %s holds %d bundles. Name one bundle.\n", cmd, paths[0], len(selected))
		return api.SpecDoc{}, nil, exitUsage
	}
	if selected[0].Unnamed {
		fmt.Fprintf(stderr, "speccy %s: %s is in no bundle. Review it first, so Speccy saves it.\n", cmd, paths[0])
		return api.SpecDoc{}, nil, exitUsage
	}
	s, err := openSession(ctx, root.Dir())
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %v.\n", cmd, problemText(err))
		return api.SpecDoc{}, nil, exitRun
	}
	bundles, err := listAll(ctx, s.client)
	if err != nil {
		fmt.Fprintf(stderr, "speccy %s: %v.\n", cmd, problemText(err))
		return api.SpecDoc{}, nil, exitRun
	}
	for _, b := range bundles {
		if b.Slug == selected[0].Slug {
			return b, s, exitOK
		}
	}
	fmt.Fprintf(stderr, "speccy %s: Speccy did not load %s. Run speccy in the folder to see why.\n", cmd, selected[0].Slug)
	return api.SpecDoc{}, nil, exitRun
}
