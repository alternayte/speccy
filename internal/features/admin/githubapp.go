package admin

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// EnvApp is the GitHub App from SPECCY_GITHUB_APP_* (docs/specs/github-app.md). When set, it
// wins over the store and locks the App fields in the UI.
type EnvApp struct {
	API           string
	ID            int64
	PrivateKey    []byte
	ClientID      string
	ClientSecret  string
	WebhookSecret string
}

// GitHubApp is the hosted GitHub App setup: the env App, the token cache, and the hooks into
// the rest of Speccy. Nil in local mode.
type GitHubApp struct {
	Env *EnvApp
	// BaseURL is SPECCY_BASE_URL, for the manifest and the redirects.
	BaseURL string
	// CallbackURL is the auth-all OAuth callback of the GitHub sign-in provider.
	CallbackURL string
	// GitHubAccount returns the GitHub account ID that auth-all linked to a Speccy user.
	GitHubAccount func(ctx context.Context, userID string) (string, bool, error)
	// Changed runs when a push or a closed pull request reaches a repo and branch.
	Changed func(ctx context.Context, repo, branch string)
	// PullRequest queues the App's review of a pull request: a push to it names its head SHA,
	// and a comment with a reply command names none (docs/specs/hosted-pr-review.md).
	PullRequest func(ctx context.Context, apiURL, repo string, pull int, sha string) error
	// Life is the server's context, for the work a webhook starts after its answer.
	Life context.Context
	HTTP *http.Client

	tokens github.Tokens
	mu     sync.Mutex
	env    *envState
}

// envState is what Speccy reads from GitHub about the env App: its owner and installation.
type envState struct {
	owner        string
	ownerIsOrg   bool
	installation int64
	slug         string
	at           time.Time
}

// appConn is the App of the workspace with its owner and installation.
type appConn struct {
	app          *github.App
	owner        string
	ownerIsOrg   bool
	installation int64
	env          bool
}

var errLinkRequired = kernel.Invalid("github_link_required", "Connect your GitHub account first. A doc change goes to GitHub as you.")

// appConn returns the App of the workspace, or nil when it has none.
func (a *API) appConn(ctx context.Context) (*appConn, error) {
	g := a.GitHubApp
	if g == nil {
		return nil, nil
	}
	if e := g.Env; e != nil {
		app := &github.App{API: e.API, ID: e.ID, ClientID: e.ClientID, ClientSecret: e.ClientSecret, PrivateKey: e.PrivateKey,
			WebhookSecret: e.WebhookSecret, HTTP: g.HTTP}
		st, err := g.envInfo(ctx, app)
		if err != nil {
			return nil, err
		}
		app.Slug = st.slug
		return &appConn{app: app, owner: st.owner, ownerIsOrg: st.ownerIsOrg, installation: st.installation, env: true}, nil
	}
	row, err := a.DB.Queries().GetGithubApp(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	open := func(b []byte) (string, error) {
		p, err := a.Sealer.Open(b)
		if errors.Is(err, kernel.ErrOtherKey) {
			return "", kernel.OtherKey("The GitHub App", "Admin → GitHub")
		}
		return string(p), err
	}
	secret, err := open(row.ClientSecretEncrypted)
	if err != nil {
		return nil, err
	}
	key, err := open(row.PrivateKeyEncrypted)
	if err != nil {
		return nil, err
	}
	hook, err := open(row.WebhookSecretEncrypted)
	if err != nil {
		return nil, err
	}
	return &appConn{
		app: &github.App{API: row.ApiUrl, ID: row.AppID, Slug: row.Slug, ClientID: row.ClientID, ClientSecret: secret,
			PrivateKey: []byte(key), WebhookSecret: hook, HTTP: g.HTTP},
		owner: row.OwnerLogin, ownerIsOrg: row.OwnerIsOrg, installation: row.InstallationID.Int64,
	}, nil
}

// envInfo reads the env App's slug, owner and installation from GitHub, and keeps them for 5
// minutes, or until a webhook says the installation changed.
func (g *GitHubApp) envInfo(ctx context.Context, app *github.App) (envState, error) {
	g.mu.Lock()
	st := g.env
	g.mu.Unlock()
	if st != nil && time.Since(st.at) < 5*time.Minute {
		return *st, nil
	}
	info, err := app.Info(ctx)
	if err != nil {
		return envState{}, fmt.Errorf("GitHub refused the App of SPECCY_GITHUB_APP_ID: %w", err)
	}
	out := envState{owner: info.Owner.Login, ownerIsOrg: info.Owner.Type == "Organization", slug: info.Slug, at: time.Now()}
	ins, err := app.Installations(ctx)
	if err != nil {
		return envState{}, err
	}
	for _, in := range ins {
		if in.SuspendedAt == nil && strings.EqualFold(in.Account.Login, out.owner) {
			out.installation = in.ID
		}
	}
	g.mu.Lock()
	g.env = &out
	g.mu.Unlock()
	return out, nil
}

func (g *GitHubApp) forget() {
	g.tokens.Forget()
	g.mu.Lock()
	g.env = nil
	g.mu.Unlock()
}

func sameAPI(a, b string) bool {
	norm := func(s string) string {
		if s == "" {
			return github.DefaultAPI
		}
		return strings.TrimRight(s, "/")
	}
	return strings.EqualFold(norm(a), norm(b))
}

// installationClient returns a client with the token of the App's installation.
func (a *API) installationClient(ctx context.Context, c *appConn, apiURL string) (*github.Client, error) {
	if apiURL != "" && !sameAPI(apiURL, c.app.API) {
		return nil, kernel.Invalid("github_other_host", "The workspace's GitHub App is on %s, and this address is on %s.", github.WebURL(c.app.API), github.WebURL(apiURL))
	}
	if c.installation == 0 {
		return nil, kernel.Invalid("app_not_installed", "The Speccy App is not installed yet. An admin installs it from Admin → GitHub: %s", c.app.InstallURL())
	}
	return a.GitHubApp.tokens.Installation(ctx, c.app, c.installation)
}

// UserGitHubClient returns the client that writes a doc change for userID: the person's own
// GitHub token when the workspace has an App, so GitHub shows that person as the author. A
// token workspace writes with its token, as its owner.
func (a *API) UserGitHubClient(ctx context.Context, apiURL, userID string) (*github.Client, error) {
	c, err := a.appConn(ctx)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return a.GitHubClient(ctx, apiURL)
	}
	if apiURL != "" && !sameAPI(apiURL, c.app.API) {
		return nil, kernel.Invalid("github_other_host", "The workspace's GitHub App is on %s, and this address is on %s.", github.WebURL(c.app.API), github.WebURL(apiURL))
	}
	if userID == "" || a.GitHubApp.GitHubAccount == nil {
		return nil, errLinkRequired
	}
	ghID, ok, err := a.GitHubApp.GitHubAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errLinkRequired
	}
	q := a.DB.Queries()
	row, err := q.GetGithubUserToken(ctx, pgdb.GetGithubUserTokenParams{WorkspaceID: a.Workspace, GithubUserID: ghID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errLinkRequired
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if !row.AccessExpiresAt.Valid || now.Add(time.Minute).Before(row.AccessExpiresAt.Time) {
		access, err := a.Sealer.Open(row.AccessEncrypted)
		if err != nil {
			return nil, errLinkRequired
		}
		return &github.Client{API: c.app.API, Token: string(access), HTTP: a.GitHubApp.HTTP}, nil
	}
	refresh, err := a.Sealer.Open(row.RefreshEncrypted)
	if len(row.RefreshEncrypted) == 0 || err != nil || (row.RefreshExpiresAt.Valid && now.After(row.RefreshExpiresAt.Time)) {
		_ = q.DeleteGithubUserToken(ctx, pgdb.DeleteGithubUserTokenParams{WorkspaceID: a.Workspace, GithubUserID: ghID})
		return nil, errLinkRequired
	}
	t, err := c.app.RefreshUserToken(ctx, string(refresh))
	if errors.Is(err, github.ErrUserTokenRefused) {
		_ = q.DeleteGithubUserToken(ctx, pgdb.DeleteGithubUserTokenParams{WorkspaceID: a.Workspace, GithubUserID: ghID})
		return nil, errLinkRequired
	}
	if err != nil {
		return nil, err
	}
	if err := a.SaveUserToken(ctx, ghID, row.Login, t); err != nil {
		return nil, err
	}
	return &github.Client{API: c.app.API, Token: t.Access, HTTP: a.GitHubApp.HTTP}, nil
}

// SaveUserToken stores a person's GitHub user token, sealed.
func (a *API) SaveUserToken(ctx context.Context, githubUserID, login string, t github.UserToken) error {
	access, err := a.Sealer.Seal([]byte(t.Access))
	if err != nil {
		return err
	}
	var refresh []byte
	if t.Refresh != "" {
		if refresh, err = a.Sealer.Seal([]byte(t.Refresh)); err != nil {
			return err
		}
	}
	at := func(t time.Time) sql.NullTime { return sql.NullTime{Time: t.UTC(), Valid: !t.IsZero()} }
	return a.DB.Queries().UpsertGithubUserToken(ctx, pgdb.UpsertGithubUserTokenParams{
		WorkspaceID: a.Workspace, GithubUserID: githubUserID, Login: login, AccessEncrypted: access,
		AccessExpiresAt: at(t.AccessExpires), RefreshEncrypted: refresh, RefreshExpiresAt: at(t.RefreshExpires), UpdatedAt: time.Now().UTC(),
	})
}

// SignInApp is the App's OAuth client for the GitHub sign-in, or nil when the workspace has no
// App.
func (a *API) SignInApp(ctx context.Context) (*github.App, error) {
	c, err := a.appConn(ctx)
	if err != nil || c == nil {
		return nil, err
	}
	return c.app, nil
}

// MayJoin says whether a GitHub account may sign in: a member of the org that owns the App,
// or the one account that owns a personal App. Speccy asks GitHub at each sign-in.
func (a *API) MayJoin(ctx context.Context, login string) (bool, error) {
	c, err := a.appConn(ctx)
	if err != nil || c == nil {
		return false, err
	}
	if !c.ownerIsOrg {
		return strings.EqualFold(login, c.owner), nil
	}
	ic, err := a.installationClient(ctx, c, "")
	if err != nil {
		return false, err
	}
	return ic.Membership(ctx, c.owner, login)
}

// manifestState is the sealed state of one App creation: who started it, on which host, and
// until when. It binds the redirect from GitHub to the admin who started the flow.
type manifestState struct {
	User string    `json:"u"`
	API  string    `json:"a"`
	Org  bool      `json:"o"`
	Exp  time.Time `json:"e"`
}

var orgName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// StartGithubApp returns the manifest and the GitHub address that the browser posts it to.
func (a *API) StartGithubApp(ctx context.Context, req api.StartGithubAppRequestObject) (api.StartGithubAppResponseObject, error) {
	g := a.GitHubApp
	if g == nil {
		return nil, kernel.Invalid("hosted_only", "A GitHub App needs hosted mode. Local mode reads GitHub with the gh login.")
	}
	if g.Env != nil {
		return nil, kernel.Invalid("app_from_env", "The GitHub App comes from the SPECCY_GITHUB_APP_ variables. Change it there.")
	}
	if _, err := a.DB.Queries().GetGithubApp(ctx, a.Workspace); err == nil {
		return nil, kernel.Invalid("app_exists", "The workspace has a GitHub App. Disconnect it first.")
	}
	if _, err := a.DB.Queries().GetGithubConnection(ctx, a.Workspace); err == nil {
		return nil, kernel.Invalid("token_set", "The workspace reads GitHub with a token. Remove the token first: a workspace holds an App or a token, not both.")
	}
	org, apiURL := "", github.DefaultAPI
	if req.Body != nil {
		if req.Body.Org != nil {
			org = strings.TrimSpace(*req.Body.Org)
		}
		if req.Body.ApiUrl != nil && strings.TrimSpace(*req.Body.ApiUrl) != "" {
			apiURL = strings.TrimRight(strings.TrimSpace(*req.Body.ApiUrl), "/")
			if u, err := url.Parse(apiURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return nil, kernel.Invalid("bad_api_url", "The GitHub API address must be an https URL, such as https://github.example.com/api/v3.")
			}
		}
	}
	if org != "" && !orgName.MatchString(org) {
		return nil, kernel.Invalid("bad_org", "%q is not a GitHub org name.", org)
	}
	raw, _ := json.Marshal(manifestState{User: kernel.ActorFrom(ctx).UserID, API: apiURL, Org: org != "", Exp: time.Now().Add(time.Hour)})
	sealed, err := a.Sealer.Seal(raw)
	if err != nil {
		return nil, err
	}
	state := base64.RawURLEncoding.EncodeToString(sealed)
	host := strings.TrimPrefix(strings.TrimPrefix(g.BaseURL, "https://"), "http://")
	m := github.NewManifest("Speccy "+host, g.BaseURL, g.CallbackURL, org != "")
	if len(m.Name) > 34 {
		// GitHub limits an App name to 34 characters.
		m.Name = m.Name[:34]
	}
	body, _ := json.Marshal(m)
	return api.StartGithubApp200JSONResponse{ActionUrl: github.ManifestFormURL(apiURL, org, state), Manifest: string(body)}, nil
}

// AppCreated is the redirect_url of the manifest: it converts the code into the App and
// stores it, then sends the admin back to Admin → GitHub.
func (a *API) AppCreated(w http.ResponseWriter, r *http.Request) {
	back := func(problem string) {
		target := "/admin"
		if problem != "" {
			target += "?github_error=" + url.QueryEscape(problem)
		}
		http.Redirect(w, r, target+"#github", http.StatusFound)
	}
	ctx := r.Context()
	actor := kernel.ActorFrom(ctx)
	if actor.Role != kernel.RoleAdmin {
		back("Sign in as an admin, then create the App again.")
		return
	}
	sealed, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("state"))
	var st manifestState
	if err == nil {
		var plain []byte
		if plain, err = a.Sealer.Open(sealed); err == nil {
			err = json.Unmarshal(plain, &st)
		}
	}
	if err != nil || st.User != actor.UserID || time.Now().After(st.Exp) {
		back("The App creation expired or another person started it. Start it again.")
		return
	}
	if _, err := a.DB.Queries().GetGithubApp(ctx, a.Workspace); err == nil {
		back("The workspace has a GitHub App already.")
		return
	}
	conv, err := github.ConvertManifest(ctx, a.GitHubApp.HTTP, st.API, r.URL.Query().Get("code"))
	if err != nil {
		back("GitHub did not return the App: " + strings.TrimSuffix(err.Error(), ".") + ". Start again.")
		return
	}
	seal := func(s string) []byte {
		if err != nil {
			return nil
		}
		var b []byte
		b, err = a.Sealer.Seal([]byte(s))
		return b
	}
	secret, key, hook := seal(conv.ClientSecret), seal(conv.PEM), seal(conv.WebhookSecret)
	if err != nil {
		back("Speccy could not store the App: " + err.Error())
		return
	}
	now := time.Now().UTC()
	if err := a.DB.Queries().InsertGithubApp(ctx, pgdb.InsertGithubAppParams{
		WorkspaceID: a.Workspace, ApiUrl: st.API, AppID: conv.ID, Slug: conv.Slug, OwnerLogin: conv.Owner.Login,
		OwnerIsOrg: conv.Owner.Type == "Organization", ClientID: conv.ClientID, ClientSecretEncrypted: secret,
		PrivateKeyEncrypted: key, WebhookSecretEncrypted: hook, CreatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		back("Speccy could not store the App: " + err.Error())
		return
	}
	a.GitHubApp.forget()
	back("")
}

// AppInstalled is the setup_url of the App: GitHub sends the person here after an install or
// a change of the repos. Speccy asks GitHub whether the installation is its App's, so a forged
// ID changes nothing.
func (a *API) AppInstalled(w http.ResponseWriter, r *http.Request) {
	back := func(problem string) {
		target := "/admin"
		if problem != "" {
			target += "?github_error=" + url.QueryEscape(problem)
		}
		http.Redirect(w, r, target+"#github", http.StatusFound)
	}
	ctx := r.Context()
	if r.URL.Query().Get("setup_action") == "request" {
		back("The install waits for an owner of the account to approve it on GitHub.")
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil {
		back("GitHub sent no installation.")
		return
	}
	c, err := a.appConn(ctx)
	if err != nil || c == nil {
		back("The workspace has no GitHub App.")
		return
	}
	if err := a.recordInstallation(ctx, c, id); err != nil {
		back(err.Error())
		return
	}
	back("")
}

func (a *API) recordInstallation(ctx context.Context, c *appConn, id int64) error {
	in, err := c.app.GetInstallation(ctx, id)
	if err != nil {
		return fmt.Errorf("GitHub does not know installation %d of this App", id)
	}
	if !strings.EqualFold(in.Account.Login, c.owner) {
		return fmt.Errorf("the App is installed on %s, not on %s, which owns it", in.Account.Login, c.owner)
	}
	if !c.env {
		if err := a.DB.Queries().SetGithubAppInstallation(ctx, pgdb.SetGithubAppInstallationParams{
			WorkspaceID: a.Workspace, InstallationID: sql.NullInt64{Int64: id, Valid: true}, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	a.GitHubApp.forget()
	return nil
}

// Webhook takes the App's events: installations, pushes, and closed pull requests. It checks
// X-Hub-Signature-256, answers at once, and syncs in the background: GitHub waits 10 seconds.
func (a *API) Webhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := a.appConn(ctx)
	if err != nil || c == nil {
		http.Error(w, "no GitHub App", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if !github.ValidSignature(c.app.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var ev struct {
		Action       string `json:"action"`
		Ref          string `json:"ref"`
		Installation struct {
			ID    int64 `json:"id"`
			AppID int64 `json:"app_id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		PullRequest struct {
			Number int  `json:"number"`
			Draft  bool `json:"draft"`
			Base   struct {
				Ref string `json:"ref"`
			} `json:"base"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
		Issue struct {
			Number      int `json:"number"`
			PullRequest *struct {
				URL string `json:"url"`
			} `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	life := a.GitHubApp.Life
	if life == nil {
		life = context.WithoutCancel(ctx)
	}
	changed := func(repo, branch string) {
		if a.GitHubApp.Changed == nil || repo == "" || branch == "" {
			return
		}
		go a.GitHubApp.Changed(life, repo, branch)
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "installation":
		switch ev.Action {
		case "created", "unsuspend", "new_permissions_accepted":
			if err := a.recordInstallation(ctx, c, ev.Installation.ID); err != nil {
				slog.WarnContext(ctx, "an installation event of the GitHub App was not recorded", "err", err)
			}
		case "deleted", "suspend":
			if !c.env && ev.Installation.ID == c.installation {
				if err := a.DB.Queries().SetGithubAppInstallation(ctx, pgdb.SetGithubAppInstallationParams{
					WorkspaceID: a.Workspace, UpdatedAt: time.Now().UTC(),
				}); err != nil {
					http.Error(w, "store failed", http.StatusInternalServerError)
					return
				}
			}
			a.GitHubApp.forget()
		}
	case "installation_repositories":
		// A token lists the repos it covers when it is made.
		a.GitHubApp.tokens.Forget()
	case "push":
		if branch, ok := strings.CutPrefix(ev.Ref, "refs/heads/"); ok {
			changed(ev.Repository.FullName, branch)
		}
	case "pull_request":
		switch ev.Action {
		case "closed":
			changed(ev.Repository.FullName, ev.PullRequest.Base.Ref)
		case "opened", "synchronize", "reopened", "ready_for_review":
			a.reviewPull(ctx, c, ev.Repository.FullName, ev.PullRequest.Number, ev.PullRequest.Head.SHA)
		}
	case "issue_comment":
		if ev.Action == "created" && ev.Issue.PullRequest != nil && replyCommand(ev.Comment.Body) {
			a.reviewPull(ctx, c, ev.Repository.FullName, ev.Issue.Number, "")
		}
	case "pull_request_review_comment":
		if ev.Action == "created" && replyCommand(ev.Comment.Body) {
			a.reviewPull(ctx, c, ev.Repository.FullName, ev.PullRequest.Number, "")
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// reviewPull queues the App's review of a pull request. A failure to queue is logged: GitHub
// does not send the event again.
func (a *API) reviewPull(ctx context.Context, c *appConn, repo string, pull int, sha string) {
	if a.GitHubApp.PullRequest == nil || repo == "" || pull == 0 {
		return
	}
	if err := a.GitHubApp.PullRequest(ctx, c.app.API, repo, pull, sha); err != nil {
		slog.ErrorContext(ctx, "the App's review of a pull request was not queued", "repo", repo, "pull", pull, "err", err)
	}
}

// replyCommand reports whether a comment holds a line that starts with /speccy, which the next
// review turns into a decision.
func replyCommand(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "/speccy ") {
			return true
		}
	}
	return false
}

// DeleteGithubApp disconnects the App: Speccy forgets its keys and every person's GitHub
// token. The App stays on GitHub; its settings page deletes it there.
func (a *API) DeleteGithubApp(ctx context.Context, _ api.DeleteGithubAppRequestObject) (api.DeleteGithubAppResponseObject, error) {
	if a.GitHubApp != nil && a.GitHubApp.Env != nil {
		return nil, kernel.Invalid("app_from_env", "The GitHub App comes from the SPECCY_GITHUB_APP_ variables. Remove them to disconnect it.")
	}
	q := a.DB.Queries()
	if err := q.DeleteGithubUserTokens(ctx, a.Workspace); err != nil {
		return nil, err
	}
	if err := q.DeleteGithubApp(ctx, a.Workspace); err != nil {
		return nil, err
	}
	if a.GitHubApp != nil {
		a.GitHubApp.forget()
	}
	return api.DeleteGithubApp204Response{}, nil
}

// appState is the App part of the connection for the admin screen.
func (a *API) appState(ctx context.Context) (*api.GithubApp, error) {
	c, err := a.appConn(ctx)
	if err != nil || c == nil {
		return nil, err
	}
	out := &api.GithubApp{
		Slug: c.app.Slug, Owner: c.owner, OwnerIsOrg: c.ownerIsOrg, Installed: c.installation != 0, ApiUrl: c.app.API,
		InstallUrl: c.app.InstallURL(), SettingsUrl: github.SettingsURL(c.app.API, c.app.Slug, c.owner, c.ownerIsOrg), FromEnv: c.env,
	}
	if c.installation != 0 {
		// An App made before a Speccy release asked for more lacks the new permissions.
		if in, err := c.app.GetInstallation(ctx, c.installation); err == nil {
			if missing := in.Missing(); len(missing) > 0 {
				out.Missing = &missing
			}
		}
	}
	return out, nil
}
