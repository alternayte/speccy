package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// fakeGitHub answers the calls of the App flow: the manifest conversion, the installation, the
// installation token, a repo read, and the user token refresh.
type fakeGitHub struct {
	srv       *httptest.Server
	pem       string
	mu        sync.Mutex
	repoToken string // the token of the last repo read
	refreshes int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{pem: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app-manifests/good-code/conversions":
			write(map[string]any{"id": 42, "slug": "speccy-acme", "client_id": "Iv1.client", "client_secret": "client-secret",
				"webhook_secret": "hook-secret", "pem": f.pem, "owner": map[string]any{"login": "acme", "type": "Organization"}})
		case r.URL.Path == "/app/installations/7" && strings.Count(bearer, ".") == 2:
			write(map[string]any{"id": 7, "app_id": 42, "account": map[string]any{"login": "acme", "type": "Organization"}})
		case r.URL.Path == "/app/installations/7/access_tokens" && strings.Count(bearer, ".") == 2:
			write(map[string]any{"token": "ghs_installation", "expires_at": time.Now().Add(time.Hour)})
		case r.URL.Path == "/repos/acme/specs":
			f.mu.Lock()
			f.repoToken = bearer
			f.mu.Unlock()
			if bearer == "ghs_installation" || strings.HasPrefix(bearer, "ghu_") {
				write(map[string]any{"default_branch": "main"})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/repos/acme/elsewhere":
			w.WriteHeader(http.StatusNotFound)
			write(map[string]any{"message": "Not Found"})
		case r.URL.Path == "/login/oauth/access_token":
			_ = r.ParseForm()
			f.mu.Lock()
			f.refreshes++
			f.mu.Unlock()
			if r.Form.Get("refresh_token") != "ghr_good" {
				write(map[string]any{"error": "bad_refresh_token"})
				return
			}
			write(map[string]any{"access_token": "ghu_new", "expires_in": 28800, "refresh_token": "ghr_next", "refresh_token_expires_in": 15897600})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// docs/specs/github-app.md: the App comes back from GitHub, its keys are sealed, its
// installation reads repos, the webhook checks its signature, and a doc write goes out as the
// person or asks them to connect GitHub.
func TestGitHubApp_Flow(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			db := e.Open(t)
			a := newAPI(t, db)
			gh := newFakeGitHub(t)
			var changed []string
			var mu sync.Mutex
			done := make(chan struct{}, 4)
			linked := map[string]string{"u-linked": "1001"}
			a.GitHubApp = &GitHubApp{
				BaseURL: "https://speccy.example.com", CallbackURL: "https://speccy.example.com/api/auth/oauth/github/callback",
				GitHubAccount: func(_ context.Context, userID string) (string, bool, error) {
					id, ok := linked[userID]
					return id, ok, nil
				},
				Changed: func(_ context.Context, repo, branch string) {
					mu.Lock()
					changed = append(changed, repo+"@"+branch)
					mu.Unlock()
					done <- struct{}{}
				},
				Life: ctx,
			}
			admin := kernel.WithActor(ctx, kernel.Actor{UserID: "u-admin", Role: kernel.RoleAdmin})

			// Start: the manifest asks for the org's page and carries the sealed state.
			res, err := a.StartGithubApp(admin, api.StartGithubAppRequestObject{Body: &api.StartGithubAppJSONRequestBody{Org: ptr("acme"), ApiUrl: ptr(gh.srv.URL)}})
			if err != nil {
				t.Fatal(err)
			}
			started := res.(api.StartGithubApp200JSONResponse)
			u, _ := url.Parse(started.ActionUrl)
			if !strings.HasSuffix(u.Path, "/organizations/acme/settings/apps/new") || u.Query().Get("state") == "" {
				t.Fatalf("action URL %q, want the org's new-App page with a state", started.ActionUrl)
			}
			if !strings.Contains(started.Manifest, `"members":"read"`) || !strings.Contains(started.Manifest, `"public":false`) {
				t.Errorf("manifest %s, want a private App that reads org members", started.Manifest)
			}

			// Another person cannot finish the flow with the state.
			rec := httptest.NewRecorder()
			other := kernel.WithActor(ctx, kernel.Actor{UserID: "u-other", Role: kernel.RoleAdmin})
			a.AppCreated(rec, httptest.NewRequestWithContext(other, http.MethodGet, "/api/github/app/created?code=good-code&state="+u.Query().Get("state"), nil))
			if loc := rec.Header().Get("Location"); !strings.Contains(loc, "github_error=") {
				t.Fatalf("another admin finished the flow: %s", loc)
			}

			rec = httptest.NewRecorder()
			a.AppCreated(rec, httptest.NewRequestWithContext(admin, http.MethodGet, "/api/github/app/created?code=good-code&state="+u.Query().Get("state"), nil))
			if loc := rec.Header().Get("Location"); loc != "/admin#github" {
				t.Fatalf("created redirect %q, want /admin#github", loc)
			}
			var sealed []byte
			if err := db.SQL.QueryRowContext(ctx, `SELECT private_key_encrypted FROM github_app`).Scan(&sealed); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(sealed), "PRIVATE KEY") {
				t.Error("the App's private key is stored in plain text")
			}

			// A workspace holds an App or a token, never both.
			if _, err := a.SetGithubConnection(admin, api.SetGithubConnectionRequestObject{Body: &api.SetGithubConnectionJSONRequestBody{Token: "github_pat_x"}}); !hasCode(err, "app_exists") {
				t.Errorf("a token was accepted next to the App: %v", err)
			}

			// Before the install, a read says so.
			if _, err := a.GitHubClient(ctx, gh.srv.URL); !hasCode(err, "app_not_installed") {
				t.Errorf("read before the install: %v, want app_not_installed", err)
			}
			rec = httptest.NewRecorder()
			a.AppInstalled(rec, httptest.NewRequest(http.MethodGet, "/api/github/app/installed?installation_id=7&setup_action=install", nil))
			if loc := rec.Header().Get("Location"); loc != "/admin#github" {
				t.Fatalf("installed redirect %q", loc)
			}
			c, err := a.GitHubClient(ctx, gh.srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Repo(ctx, "acme/specs"); err != nil || gh.repoToken != "ghs_installation" {
				t.Errorf("repo read: %v with token %q, want the installation token", err, gh.repoToken)
			}
			// A repo the installation does not cover names the install page.
			_, err = c.Repo(ctx, "acme/elsewhere")
			if err := github.UnreadableRepo("acme/elsewhere", err); !hasCode(err, "app_not_installed") || !strings.Contains(err.Error(), "/apps/speccy-acme/installations/new") {
				t.Errorf("uncovered repo: %v", err)
			}

			// The webhook refuses a bad signature, and a push syncs its repo and branch.
			push := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/specs"}}`)
			rec = httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", strings.NewReader(string(push)))
			req.Header.Set("X-GitHub-Event", "push")
			req.Header.Set("X-Hub-Signature-256", sign("wrong", push))
			a.Webhook(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("bad signature answered %d, want 401", rec.Code)
			}
			rec = httptest.NewRecorder()
			req = httptest.NewRequest(http.MethodPost, "/api/github/webhook", strings.NewReader(string(push)))
			req.Header.Set("X-GitHub-Event", "push")
			req.Header.Set("X-Hub-Signature-256", sign("hook-secret", push))
			a.Webhook(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("signed push answered %d", rec.Code)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the push did not sync")
			}
			mu.Lock()
			if len(changed) != 1 || changed[0] != "acme/specs@main" {
				t.Errorf("synced %v, want acme/specs@main", changed)
			}
			mu.Unlock()

			// A doc write: a person with no GitHub link must connect first.
			if _, err := a.UserGitHubClient(ctx, gh.srv.URL, "u-nolink"); !hasCode(err, "github_link_required") {
				t.Errorf("write with no link: %v, want github_link_required", err)
			}
			// A linked person with an expired access token gets it refreshed, once.
			if err := a.SaveUserToken(ctx, "1001", "alice", github.UserToken{Access: "ghu_old", AccessExpires: time.Now().Add(-time.Hour),
				Refresh: "ghr_good", RefreshExpires: time.Now().Add(24 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
			uc, err := a.UserGitHubClient(ctx, gh.srv.URL, "u-linked")
			if err != nil || uc.Token != "ghu_new" {
				t.Fatalf("refresh: %v, client %+v", err, uc)
			}
			// A refused refresh asks the person to connect again.
			if err := a.SaveUserToken(ctx, "1001", "alice", github.UserToken{Access: "ghu_old", AccessExpires: time.Now().Add(-time.Hour),
				Refresh: "ghr_revoked"}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.UserGitHubClient(ctx, gh.srv.URL, "u-linked"); !hasCode(err, "github_link_required") {
				t.Errorf("revoked refresh: %v, want github_link_required", err)
			}

			// Disconnect forgets the App and the person tokens.
			if _, err := a.DeleteGithubApp(admin, api.DeleteGithubAppRequestObject{}); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := db.SQL.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM github_app) + (SELECT COUNT(*) FROM github_user_token)`).Scan(&n); err != nil || n != 0 {
				t.Errorf("after disconnect %d rows remain (%v)", n, err)
			}
		})
	}
}

func hasCode(err error, code string) bool {
	var e *kernel.Error
	return errors.As(err, &e) && e.Code == code
}
