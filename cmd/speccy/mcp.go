package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
	fl := flag.NewFlagSet("speccy mcp", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, "Usage: speccy mcp [--root folder]\n") }
	// Some MCP clients set no working folder, so the folder with the docs can come as a flag (#89).
	rootDir := fl.String("root", "", "the folder with the docs (default: the current folder)")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fl.NArg() != 0 {
		fl.Usage()
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
	if *rootDir != "" {
		if cwd, err = filepath.Abs(*rootDir); err != nil {
			fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
			return exitUsage
		}
	}
	root, err := local.Open(findRoot(cwd))
	if err != nil {
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitUsage
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	// An agent edits files on disk: the owner of the folder follows them (REQ-005).
	st, err := openSeat(root, state, filepath.Join(state, "key"), nil)
	if err != nil {
		fmt.Fprintf(stderr, "speccy mcp: %v.\n", err)
		return exitRun
	}
	client, err := st.client()
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
