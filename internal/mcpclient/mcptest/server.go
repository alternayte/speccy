// Package mcptest runs an MCP server with a search tool, for tests.
package mcptest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchIn struct {
	Query string `json:"query" jsonschema:"the search query"`
}

type deleteIn struct {
	ID string `json:"id"`
}

// SearchServer starts an MCP server over streamable HTTP with a read-only "search" tool that
// answers with results(query), and a destructive "delete_page" tool. It returns the URL.
func SearchServer(t *testing.T, results func(query string) string) string {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-search", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "search", Description: "Search the web.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: results(in.Query)}}}, nil, nil
		})
	destructive := true
	mcp.AddTool(s, &mcp.Tool{Name: "delete_page", Description: "Delete a page.", Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ deleteIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deleted"}}}, nil, nil
		})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	t.Cleanup(srv.Close)
	return srv.URL
}
