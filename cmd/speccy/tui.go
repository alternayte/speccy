package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing/fstest"
	"time"

	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/tui"
)

// runTUI is speccy tui (REQ-122) for the folder with .speccy.yaml, or the current folder. It
// uses .speccy/state, as local mode does, and follows changes on disk.
func runTUI(args []string, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(stderr, "Usage: speccy tui\n")
		return exitUsage
	}
	// The screen belongs to the TUI: log only problems, to a file in the state folder.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy tui: %v.\n", err)
		return exitUsage
	}
	root, err := local.Open(findRoot(cwd))
	if err != nil {
		fmt.Fprintf(stderr, "speccy tui: %v.\n", err)
		return exitUsage
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		fmt.Fprintf(stderr, "speccy tui: %v.\n", err)
		return exitRun
	}
	logFile, err := os.OpenFile(filepath.Join(state, "tui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		defer func() { _ = logFile.Close() }()
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelWarn})))
	}
	a, db, err := openApp(ctx, root, state, filepath.Join(state, "key"))
	if err != nil {
		fmt.Fprintf(stderr, "speccy tui: %v.\n", err)
		return exitRun
	}
	go func() {
		_ = root.Watch(ctx, 300*time.Millisecond, func() {
			if err := a.Profiles.Reload(ctx); err != nil && ctx.Err() == nil {
				slog.Error("reload of the profiles failed", "err", err)
			}
			if err := a.Bundles.Sync(ctx); err != nil && ctx.Err() == nil {
				slog.Error("sync after a change on disk failed", "err", err)
			}
		})
	}()
	h := localHandler(fs.FS(fstest.MapFS{}), a, db)
	client, err := api.NewClientWithResponses("http://speccy.local/api/v1", api.WithHTTPClient(speccyhttp.InProcess{Handler: h}))
	if err != nil {
		fmt.Fprintf(stderr, "speccy tui: %v.\n", err)
		return exitRun
	}
	// The folder of each bundle on disk, from a scan; a bundle that is new since then rescans.
	var mu sync.Mutex
	dirs := map[string]string{}
	scan := func() {
		cfg, _ := source.LoadRepoConfig(root.Dir())
		if s, err := root.Scan(cfg); err == nil {
			for _, b := range s.Bundles {
				dirs[b.Slug] = b.Dir
			}
		}
	}
	bundleDir := func(slug string) string {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := dirs[slug]; !ok {
			scan()
		}
		return dirs[slug]
	}
	if err := tui.Run(ctx, tui.Options{Client: client, Root: root.Dir(), BundleDir: bundleDir}); err != nil {
		fmt.Fprintf(stderr, "speccy tui: %v.\n", err)
		return exitRun
	}
	return exitOK
}
