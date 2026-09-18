package mcpclient

import (
	"context"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/mcpclient/mcptest"
)

func TestSession_ToolsAndSearch(t *testing.T) {
	ctx := context.Background()
	url := mcptest.SearchServer(t, func(q string) string { return "Result for " + q })
	s, err := Open(ctx, Connection{Transport: "http", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tools, err := s.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "delete_page" || !tools[0].Destructive || tools[1].Name != "search" || !tools[1].ReadOnly {
		t.Fatalf("tools = %+v", tools)
	}
	arg, err := QueryArgument(tools[1].InputSchema)
	if err != nil || arg != "query" {
		t.Fatalf("query argument %q, %v", arg, err)
	}
	out, err := s.Call(ctx, "search", map[string]any{arg: "go release"})
	if err != nil || !strings.Contains(out, "Result for go release") {
		t.Errorf("search: %q, %v", out, err)
	}
}
