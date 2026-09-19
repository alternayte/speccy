package hostauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alternayte/speccy/internal/app"
	"github.com/alternayte/speccy/internal/hostauth"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
	"github.com/alternayte/speccy/internal/store/storetest"
)

type env struct {
	srv  *httptest.Server
	auth *hostauth.Auth
	db   *store.DB
}

// newEnv serves hosted mode on a test server: auth-all on SQLite or Postgres, and the API.
func newEnv(t *testing.T, e storetest.Engine) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var h http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	db := e.Open(t)
	sealer, err := kernel.NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := hostauth.New(hostauth.Config{DB: db, Workspace: ws, BaseURL: srv.URL, Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(ctx, db, sealer, app.Options{People: auth})
	if err != nil {
		t.Fatal(err)
	}
	a.Admin.Accounts = auth
	a.Share.Sealer, a.Share.BaseURL = sealer, srv.URL
	a.API.Hosted = true
	spa := fs.FS(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}})
	h = speccyhttp.Handler(spa, a.API, speccyhttp.Options{
		Actor: auth.Actor(a.Share.Guest), Authz: &speccyhttp.Authz{DB: db, Workspace: a.Workspace}, Auth: auth.Handler(),
	})
	return &env{srv: srv, auth: auth, db: db}
}

// client is one browser: a cookie jar, and an optional API token.
type client struct {
	t      *testing.T
	env    *env
	http   *http.Client
	bearer string
}

func (e *env) client(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, env: e, http: &http.Client{Jar: jar}}
}

// call sends a JSON request and returns the status and the decoded body.
func (c *client) call(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.env.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.env.srv.URL)
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out := map[string]any{}
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func code(body map[string]any) string {
	if c, ok := body["code"].(string); ok {
		return c
	}
	if e, ok := body["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

func token(url string) string {
	_, t, _ := strings.Cut(url, "#token=")
	return t
}

const password = "correct horse battery"

// join accepts an invite link as a new user and returns their signed-in client.
func (e *env) join(t *testing.T, link, email string) *client {
	t.Helper()
	c := e.client(t)
	if s, b := c.call("POST", "/api/auth/speccy/invites/accept", map[string]string{"token": token(link), "email": email, "password": password}); s != 200 {
		t.Fatalf("accept the invite for %s: %d %v", email, s, b)
	}
	return c
}

// REQ-081, REQ-083: an invite link makes one account with its role, once. There is no open
// sign-up.
func TestInvite_OneAccountOnce(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			link, _, err := e.auth.CreateInvite(context.Background(), kernel.RoleAdmin, "cli", 0)
			if err != nil {
				t.Fatal(err)
			}
			anon := e.client(t)
			if s, b := anon.call("POST", "/api/auth/speccy/invites/check", map[string]string{"token": token(link)}); s != 200 || b["role"] != "admin" {
				t.Fatalf("check the invite: %d %v", s, b)
			}
			if s, b := anon.call("POST", "/api/auth/speccy/invites/accept", map[string]string{"token": token(link), "email": "short@x.test", "password": "short"}); s != 400 || code(b) != "WEAK_PASSWORD" {
				t.Errorf("a short password: %d %v", s, b)
			}
			admin := e.join(t, link, "admin@x.test")
			if s, b := admin.call("GET", "/api/v1/me", nil); s != 200 || b["signed_in"] != true || b["role"] != "admin" {
				t.Errorf("me after the invite: %d %v", s, b)
			}
			if s, b := e.client(t).call("POST", "/api/auth/speccy/invites/accept", map[string]string{"token": token(link), "email": "again@x.test", "password": password}); s != 400 || code(b) != "LINK_INVALID" {
				t.Errorf("a used invite: %d %v", s, b)
			}
			if s, b := e.client(t).call("POST", "/api/auth/sign-up/email", map[string]string{"email": "open@x.test", "password": password}); s != 403 || code(b) != "SIGN_UP_CLOSED" {
				t.Errorf("open sign-up: %d %v", s, b)
			}
			// An invite for an address in use stays usable.
			s, b := admin.call("POST", "/api/v1/admin/invites", map[string]string{"role": "member"})
			if s != 200 {
				t.Fatalf("create an invite: %d %v", s, b)
			}
			url := b["url"].(string)
			if s, _ := e.client(t).call("POST", "/api/auth/speccy/invites/accept", map[string]string{"token": token(url), "email": "admin@x.test", "password": password}); s == 200 {
				t.Error("an invite made a second account for an address in use")
			}
			member := e.join(t, url, "member@x.test")
			if s, b := member.call("GET", "/api/v1/me", nil); s != 200 || b["role"] != "member" {
				t.Errorf("me as the invited member: %d %v", s, b)
			}
			if s, _ := member.call("GET", "/api/v1/admin/invites", nil); s != 403 {
				t.Errorf("a member lists invites: %d, want 403", s)
			}
		})
	}
}

// T-042
func TestGuest_Restrictions(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			link, _, _ := e.auth.CreateInvite(context.Background(), kernel.RoleMember, "cli", 0)
			author := e.join(t, link, "author@x.test")
			s, b := author.call("POST", "/api/v1/bundles", map[string]string{"profile": "sdd", "name": "pay"})
			if s != 201 && s != 200 {
				t.Fatalf("create a bundle: %d %v", s, b)
			}
			id := b["id"].(string)
			version := b["current_version"].(map[string]any)["id"].(string)
			s, b = author.call("POST", "/api/v1/bundles/"+id+"/share", map[string]any{})
			if s != 200 {
				t.Fatalf("create a share link: %d %v", s, b)
			}
			shareToken := b["url"].(string)[strings.LastIndex(b["url"].(string), "/")+1:]

			guest := e.client(t)
			if s, b := guest.call("POST", "/api/v1/share/"+shareToken, map[string]string{"display_name": "Robin"}); s != 200 || b["bundle_id"] != id {
				t.Fatalf("join as a guest: %d %v", s, b)
			}
			if s, b := guest.call("GET", "/api/v1/me", nil); s != 200 || b["signed_in"] != false || b["guest"] == nil {
				t.Errorf("me as a guest: %d %v", s, b)
			}
			// A guest reads the shared bundle.
			for _, p := range []string{"/api/v1/bundles/" + id, "/api/v1/bundles/" + id + "/files", "/api/v1/bundles/" + id + "/runs"} {
				if s, b := guest.call("GET", p, nil); s != 200 {
					t.Errorf("guest GET %s: %d %v", p, s, b)
				}
			}
			// A guest cannot edit, ask the AI, change access, or see other bundles. Waivers and
			// approvals arrive at M9 with the same role table.
			for _, c := range []struct{ method, path string }{
				{"PUT", "/api/v1/bundles/" + id + "/files/content?path=SPEC.md&base_version=" + version},
				{"POST", "/api/v1/bundles/" + id + "/runs"},
				{"GET", "/api/v1/bundles/" + id + "/runs/estimate"},
				{"PUT", "/api/v1/bundles/" + id + "/visibility"},
				{"POST", "/api/v1/bundles/" + id + "/share"},
				{"POST", "/api/v1/bundles/" + id + "/trace/ids?base_version=" + version},
				{"GET", "/api/v1/bundles"},
				{"POST", "/api/v1/bundles"},
			} {
				if s, b := guest.call(c.method, c.path, map[string]any{}); s != 403 || code(b) != "guest_not_allowed" {
					t.Errorf("guest %s %s: %d %v; want 403 guest_not_allowed", c.method, c.path, s, b)
				}
			}
			// Revoking the link ends the guest at once (REQ-085).
			if s, b := author.call("DELETE", "/api/v1/bundles/"+id+"/share", nil); s != 200 {
				t.Fatalf("revoke: %d %v", s, b)
			}
			if s, _ := guest.call("GET", "/api/v1/bundles/"+id, nil); s != 401 {
				t.Errorf("guest after revoke: %d, want 401", s)
			}
			if s, _ := guest.call("GET", "/api/v1/share/"+shareToken, nil); s != 404 {
				t.Errorf("a revoked share link: %d, want 404", s)
			}
		})
	}
}

// REQ-082: an admin's reset link sets a new password once, and ends the user's sessions.
// REQ-110: a personal API token authenticates the API.
func TestResetLinkAndAPIToken(t *testing.T) {
	e := newEnv(t, storetest.Engines()[0])
	adminLink, _, _ := e.auth.CreateInvite(context.Background(), kernel.RoleAdmin, "cli", 0)
	admin := e.join(t, adminLink, "admin@x.test")
	memberLink, _, _ := e.auth.CreateInvite(context.Background(), kernel.RoleMember, "cli", 0)
	member := e.join(t, memberLink, "member@x.test")

	s, b := admin.call("POST", "/api/v1/admin/reset-links", map[string]string{"email": "member@x.test"})
	if s != 200 {
		t.Fatalf("create a reset link: %d %v", s, b)
	}
	reset := token(b["url"].(string))
	anon := e.client(t)
	if s, b := anon.call("POST", "/api/auth/speccy/reset/check", map[string]string{"token": reset}); s != 200 || b["email"] != "member@x.test" {
		t.Errorf("check the reset link: %d %v", s, b)
	}
	if s, b := anon.call("POST", "/api/auth/speccy/reset", map[string]string{"token": reset, "password": "a new long password"}); s != 200 {
		t.Fatalf("reset: %d %v", s, b)
	}
	if s, b := anon.call("POST", "/api/auth/speccy/reset", map[string]string{"token": reset, "password": "another long password"}); s != 400 || code(b) != "LINK_INVALID" {
		t.Errorf("a used reset link: %d %v", s, b)
	}
	if _, b := member.call("GET", "/api/v1/me", nil); b["signed_in"] != false {
		t.Errorf("the member's old session still works after the reset: %v", b)
	}
	if s, b := member.call("POST", "/api/auth/sign-in/email", map[string]string{"email": "member@x.test", "password": "a new long password"}); s != 200 {
		t.Fatalf("sign in with the new password: %d %v", s, b)
	}

	s, b = member.call("POST", "/api/auth/api-keys", map[string]string{"name": "CI"})
	if s != 200 && s != 201 {
		t.Fatalf("create an API token: %d %v", s, b)
	}
	cli := e.client(t)
	cli.bearer = b["plaintext"].(string)
	if !strings.HasPrefix(cli.bearer, hostauth.APIKeyPrefix) {
		t.Errorf("token %q lacks the prefix %s", cli.bearer, hostauth.APIKeyPrefix)
	}
	if s, b := cli.call("GET", "/api/v1/me", nil); s != 200 || b["signed_in"] != true || b["email"] != "member@x.test" {
		t.Errorf("me with the API token: %d %v", s, b)
	}
}

// T-043, extended to the tokens of M8: invite, reset, and share tokens are stored as hashes.
func TestSecrets_TokensHashedAtRest(t *testing.T) {
	e := newEnv(t, storetest.Engines()[0])
	adminLink, _, _ := e.auth.CreateInvite(context.Background(), kernel.RoleAdmin, "cli", 0)
	admin := e.join(t, adminLink, "admin@x.test")
	pending, _, _ := e.auth.CreateInvite(context.Background(), kernel.RoleMember, "cli", 0)
	_, b := admin.call("POST", "/api/v1/admin/reset-links", map[string]string{"email": "admin@x.test"})
	reset := b["url"].(string)
	_, b = admin.call("POST", "/api/v1/bundles", map[string]string{"profile": "sdd", "name": "pay"})
	_, b = admin.call("POST", "/api/v1/bundles/"+b["id"].(string)+"/share", map[string]any{})
	share := b["url"].(string)
	plain := []string{token(adminLink), token(pending), token(reset), share[strings.LastIndex(share, "/")+1:]}
	for _, q := range []string{"SELECT token_hash FROM invite", "SELECT token_hash FROM reset_link", "SELECT share_token_hash FROM bundle WHERE share_token_hash IS NOT NULL"} {
		rows, err := e.db.SQL.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var v string
			_ = rows.Scan(&v)
			for _, p := range plain {
				if strings.Contains(v, p) {
					t.Errorf("%s holds a plaintext token", q)
				}
			}
		}
		_ = rows.Close()
	}
}

// SDD §14.2: the invite route is rate limited, so a token cannot be guessed by brute force.
func TestInvite_RateLimited(t *testing.T) {
	e := newEnv(t, storetest.Engines()[0])
	c := e.client(t)
	var last int
	for range 11 {
		last, _ = c.call("POST", "/api/auth/speccy/invites/accept", map[string]string{"token": "guess", "email": "x@x.test", "password": password})
	}
	if last != 429 {
		t.Errorf("the 11th acceptance in an hour: %d, want 429", last)
	}
}
