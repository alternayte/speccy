package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/mcpserver"
	"github.com/alternayte/speccy/internal/source/local"
)

// runMCP is speccy mcp: the MCP server over stdio for the folder with .speccy.yaml, or the
// current folder (REQ-110). It uses .speccy/state, so the app, the CLI, and agents share
// models, reviews, and threads. Stdout carries the protocol; messages go to stderr.
func runMCP(args []string, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(stderr, "Usage: speccy mcp\n")
		return exitUsage
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitUsage
	}
	root, err := local.Open(findRoot(cwd))
	if err != nil {
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitUsage
	}
	a, db, err := openApp(ctx, root, filepath.Join(root.Dir(), ".speccy", "state"))
	if err != nil {
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitRun
	}
	// An agent edits files on disk: follow them, as local mode does (REQ-005).
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
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitRun
	}
	s := mcpserver.New(func(context.Context, nethttp.Header) (*api.ClientWithResponses, error) { return client, nil })
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitRun
	}
	return exitOK
}

// withMCP serves /mcp over streamable HTTP in hosted mode (REQ-110). Each tool call sends the
// caller's Authorization header to the API in h, so the token's owner and the role table decide.
func withMCP(h nethttp.Handler) nethttp.Handler {
	clientFor := func(_ context.Context, header nethttp.Header) (*api.ClientWithResponses, error) {
		auth := header.Get("Authorization")
		return api.NewClientWithResponses("http://speccy.local/api/v1", api.WithHTTPClient(speccyhttp.InProcess{Handler: h}),
			api.WithRequestEditorFn(func(_ context.Context, req *nethttp.Request) error {
				req.Header.Set("Authorization", auth)
				return nil
			}))
	}
	mux := nethttp.NewServeMux()
	mux.Handle("/mcp", mcpserver.HTTP(clientFor))
	mux.Handle("/", h)
	return mux
}
