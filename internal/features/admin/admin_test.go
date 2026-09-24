package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/mcpclient/mcptest"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const secret = "sk-test-VERY-SECRET-KEY-7f3a9c"

// T-043: secrets are encrypted at rest, and the API shows only their last 4 characters.
// Tokens (share, invite, API) join this test when they arrive at M8.
func TestSecrets_EncryptedAndHashedAtRest(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			db := e.Open(t)
			a := newAPI(t, db)
			kind, name := api.Anthropic, "Claude"
			res, err := a.CreateBackend(ctx, api.CreateBackendRequestObject{Body: &api.BackendInput{Kind: kind, Name: name, Secret: ptr(secret)}})
			if err != nil {
				t.Fatal(err)
			}
			created := res.(api.CreateBackend201JSONResponse)
			if created.SecretLast4 != secret[len(secret)-4:] || !created.HasSecret {
				t.Errorf("created backend shows %q, want only the last 4 characters", created.SecretLast4)
			}

			// No column holds the secret in plain text.
			var cfg, last4 string
			var sealed []byte
			row := db.SQL.QueryRowContext(ctx, `SELECT CAST(config AS TEXT), secret_encrypted, secret_last4 FROM model_backend`)
			if err := row.Scan(&cfg, &sealed, &last4); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains([]byte(cfg), []byte(secret)) || bytes.Contains(sealed, []byte(secret)) || len(last4) != 4 {
				t.Errorf("a column holds the secret: config %q, last4 %q", cfg, last4)
			}
			// For SQLite, the database files on disk hold no copy of it either.
			if db.Engine == store.SQLite {
				var path string
				if err := db.SQL.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
					t.Fatal(err)
				}
				for _, f := range []string{path, path + "-wal"} {
					raw, err := os.ReadFile(f)
					if err == nil && bytes.Contains(raw, []byte(secret)) {
						t.Errorf("%s holds the secret in plain text", filepath.Base(f))
					}
				}
			}
			// The sealed secret decrypts with the key, so the gateway can use it.
			plain, err := a.Sealer.Open(sealed)
			if err != nil || string(plain) != secret {
				t.Errorf("the sealed secret does not open: %v", err)
			}
			// An update without a secret keeps it.
			if _, err := a.UpdateBackend(ctx, api.UpdateBackendRequestObject{BackendId: created.Id, Body: &api.BackendInput{Kind: kind, Name: "Claude 2"}}); err != nil {
				t.Fatal(err)
			}
			list, err := a.ListBackends(ctx, api.ListBackendsRequestObject{})
			if err != nil {
				t.Fatal(err)
			}
			items := list.(api.ListBackends200JSONResponse).Items
			if len(items) != 1 || !items[0].HasSecret || items[0].Name != "Claude 2" {
				t.Errorf("after an update with no secret: %+v", items)
			}
		})
	}
}

func TestRoles_AssignAndBlockDelete(t *testing.T) {
	ctx := context.Background()
	a := newAPI(t, storetest.Engines()[0].Open(t))
	res, err := a.CreateBackend(ctx, api.CreateBackendRequestObject{Body: &api.BackendInput{Kind: api.AgentCli, Name: "Claude Code", Preset: ptr("claude")}})
	if err != nil {
		t.Fatal(err)
	}
	b := res.(api.CreateBackend201JSONResponse)
	if _, err := a.AssignRole(ctx, api.AssignRoleRequestObject{Role: api.RoleName(model.RoleReader1), Body: &api.RoleInput{BackendId: b.Id, Model: "haiku"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteBackend(ctx, api.DeleteBackendRequestObject{BackendId: b.Id}); err == nil {
		t.Error("a backend in use was deleted")
	}
	roles, _ := a.ListRoles(ctx, api.ListRolesRequestObject{})
	items := roles.(api.ListRoles200JSONResponse).Items
	if len(items) != 6 || items[1].Model == nil || *items[1].Model != "haiku" {
		t.Errorf("roles = %+v", items)
	}
	if _, err := a.CreateBackend(ctx, api.CreateBackendRequestObject{Body: &api.BackendInput{Kind: api.Openai, Name: "No key"}}); err == nil {
		t.Error("an API backend with no key was created")
	}
}

func newAPI(t *testing.T, db *store.DB) *API {
	t.Helper()
	ws, err := db.Workspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sealer, err := kernel.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return &API{DB: db, Workspace: ws, Sealer: sealer, Gateway: &model.Gateway{DB: db, Workspace: ws, Sealer: sealer}}
}

func ptr[T any](v T) *T { return &v }

// REQ-112, REQ-034: an MCP connection allowlists read-only tools only, keeps its secret
// sealed, and serves as the search source.
func TestMCP_AllowlistAndSearch(t *testing.T) {
	ctx := context.Background()
	db := storetest.Engines()[0].Open(t)
	a := newAPI(t, db)
	url := mcptest.SearchServer(t, func(q string) string { return "Found: " + q })
	in := api.MCPConnectionInput{Name: "Search", Transport: api.Http, Url: &url, Secret: ptr(secret),
		ToolAllowlist: []string{"search", "delete_page"}, IsSearch: true, SearchTool: ptr("search")}
	if _, err := a.CreateMCPConnection(ctx, api.CreateMCPConnectionRequestObject{Body: &in}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a destructive tool on the allowlist: err = %v", err)
	}
	in.ToolAllowlist = []string{"search"}
	res, err := a.CreateMCPConnection(ctx, api.CreateMCPConnectionRequestObject{Body: &in})
	if err != nil {
		t.Fatal(err)
	}
	c := res.(api.CreateMCPConnection201JSONResponse)
	if !c.HasSecret || c.SecretLast4 != secret[len(secret)-4:] {
		t.Errorf("connection secret shows %q", c.SecretLast4)
	}
	var sealed []byte
	if err := db.SQL.QueryRowContext(ctx, `SELECT secret_encrypted FROM mcp_connection`).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(secret)) {
		t.Errorf("the MCP secret is stored in plain text (%v)", err)
	}
	s, err := a.SearchSource(ctx)
	if err != nil || s == nil {
		t.Fatalf("search source: %v, %v", s, err)
	}
	out, err := s.Search(ctx, "stripe rate limits")
	if err != nil || !strings.Contains(out, "Found: stripe rate limits") {
		t.Errorf("search: %q, %v", out, err)
	}
}

// A secret sealed with another key (the key file was lost, or SPECCY_MASTER_KEY changed) fails
// at use with a message that says to enter it again, and where. The connection's tools still
// save, and an update takes a new secret.
func TestSecrets_SealedWithOtherKey(t *testing.T) {
	ctx := context.Background()
	db := storetest.Engines()[0].Open(t)
	a := newAPI(t, db)
	res, err := a.CreateBackend(ctx, api.CreateBackendRequestObject{Body: &api.BackendInput{Kind: api.Anthropic, Name: "Claude", Secret: ptr(secret)}})
	if err != nil {
		t.Fatal(err)
	}
	backend := res.(api.CreateBackend201JSONResponse)
	url := mcptest.SearchServer(t, func(q string) string { return "Found: " + q })
	in := api.MCPConnectionInput{Name: "Search", Transport: api.Http, Url: &url, Secret: ptr(secret),
		ToolAllowlist: []string{"search"}, IsSearch: true, SearchTool: ptr("search")}
	created, err := a.CreateMCPConnection(ctx, api.CreateMCPConnectionRequestObject{Body: &in})
	if err != nil {
		t.Fatal(err)
	}
	conn := created.(api.CreateMCPConnection201JSONResponse)

	// The key changes: a new key file, as when the old one is lost.
	other := newAPI(t, db)
	a.Sealer, a.Gateway = other.Sealer, other.Gateway

	tested, err := a.TestBackend(ctx, api.TestBackendRequestObject{BackendId: backend.Id, Body: &api.TestBackendJSONRequestBody{Model: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if msg := tested.(api.TestBackend200JSONResponse).Error; msg == nil || !strings.Contains(*msg, "sealed with another key") || !strings.Contains(*msg, "Admin → Models") {
		t.Errorf("backend test with a secret sealed with another key: %+v", tested)
	}
	if _, err := a.SearchSource(ctx); err == nil || !strings.Contains(err.Error(), "sealed with another key") || !strings.Contains(err.Error(), "Admin → MCP connections") {
		t.Errorf("search source with a secret sealed with another key: %v", err)
	}

	// The tools save although the old secret does not open.
	in.Secret = nil
	if _, err := a.UpdateMCPConnection(ctx, api.UpdateMCPConnectionRequestObject{ConnectionId: conn.Id, Body: &in}); err != nil {
		t.Fatalf("save the tools with a secret sealed with another key: %v", err)
	}
	// A new secret replaces it, and the connection works again.
	in.Secret = ptr("sk-new-secret-abcd")
	updated, err := a.UpdateMCPConnection(ctx, api.UpdateMCPConnectionRequestObject{ConnectionId: conn.Id, Body: &in})
	if err != nil {
		t.Fatalf("update with a new secret: %v", err)
	}
	if c := updated.(api.UpdateMCPConnection200JSONResponse); c.SecretLast4 != "abcd" {
		t.Errorf("after a new secret, the connection shows %q", c.SecretLast4)
	}
	s, err := a.SearchSource(ctx)
	if err != nil || s == nil {
		t.Fatalf("search source after a new secret: %v, %v", s, err)
	}
}
