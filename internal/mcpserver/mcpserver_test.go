package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/speccy/internal/http/api"
)

// Over HTTP, /mcp needs a token, and each tool call sends the caller's Authorization header
// to the API, so the API's role table decides what the token may do (REQ-110, T-041).
func TestHTTP_TokenGoesToTheAPI(t *testing.T) {
	var seen []string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"guest_not_allowed","detail":"A guest can read this bundle only.","status":403,"title":"Forbidden","type":"about:blank"}`))
	}))
	defer apiServer.Close()
	h := HTTP(func(_ context.Context, header http.Header) (*api.ClientWithResponses, error) {
		auth := header.Get("Authorization")
		return api.NewClientWithResponses(apiServer.URL, api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", auth)
			return nil
		}))
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Post(srv.URL, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", res.StatusCode)
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer{"spy_test"}}}
	s, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	out, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "list_bundles", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsError || out.Content[0].(*mcp.TextContent).Text != "A guest can read this bundle only." {
		t.Errorf("the tool did not report the API's refusal: %+v", out.Content[0])
	}
	if len(seen) == 0 || seen[0] != "Bearer spy_test" {
		t.Errorf("the API saw Authorization %q", seen)
	}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// save_file writes only to a bundle that Speccy stores. For a local bundle it says where the
// file is, and sends no write to the API.
func TestSaveFile_LocalBundleWritesNothing(t *testing.T) {
	const id = "0190a0a0-0000-7000-8000-000000000001"
	var writes int
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + id + `","bundle_id":"` + id + `","slug":"pay","title":"Pay","profile_key":"sdd","source_kind":"local","path":"SPEC.md","local_dir":"/srv/specs/pay"}`))
	}))
	defer apiServer.Close()
	server := New(func(context.Context, http.Header) (*api.ClientWithResponses, error) {
		return api.NewClientWithResponses(apiServer.URL)
	})
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	s, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	out, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "save_file", Arguments: map[string]any{
		"bundle": id, "path": "SPEC.md", "content": "# Pay\n", "base_version": id}})
	if err != nil {
		t.Fatal(err)
	}
	text := out.Content[0].(*mcp.TextContent).Text
	if !out.IsError || !strings.Contains(text, filepath.Join("/srv/specs/pay", "SPEC.md")) || writes != 0 {
		t.Errorf("save_file on a local bundle: error %v, %d writes, text %q; want the path of the file and no write", out.IsError, writes, text)
	}
}
