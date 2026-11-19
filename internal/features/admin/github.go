package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

var errNoGitHubToken = kernel.Invalid("github_not_configured", "No GitHub token is set. An admin sets one in Admin → GitHub.")

// GitHubClient returns the GitHub client with the workspace token (DEC-019). apiURL names the
// host of the source; a token set for one host does not reach another.
func (a *API) GitHubClient(ctx context.Context, apiURL string) (*github.Client, error) {
	app, err := a.appConn(ctx)
	if err != nil {
		return nil, err
	}
	if app != nil {
		return a.installationClient(ctx, app, apiURL)
	}
	row, err := a.DB.Queries().GetGithubConnection(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoGitHubToken
	}
	if err != nil {
		return nil, err
	}
	token, err := a.Sealer.Open(row.TokenEncrypted)
	if errors.Is(err, kernel.ErrOtherKey) {
		return nil, kernel.OtherKey("The GitHub token", "Admin → GitHub")
	}
	if err != nil {
		return nil, err
	}
	if apiURL != "" && apiURL != row.ApiUrl {
		return nil, kernel.Invalid("github_other_host", "The workspace token is for %s, and this address is on %s. An admin sets the token in Admin → GitHub.", row.ApiUrl, apiURL)
	}
	return &github.Client{API: row.ApiUrl, Token: string(token)}, nil
}

// GetGithubConnection shows the workspace's one GitHub connection: an App, a token with its
// last 4 characters, or none.
func (a *API) GetGithubConnection(ctx context.Context, _ api.GetGithubConnectionRequestObject) (api.GetGithubConnectionResponseObject, error) {
	app, err := a.appState(ctx)
	if err != nil {
		return nil, err
	}
	appsOK := a.GitHubApp != nil
	if app != nil {
		return api.GetGithubConnection200JSONResponse{Configured: true, Kind: api.GithubConnectionKindApp, ApiUrl: app.ApiUrl, App: app, AppAvailable: appsOK}, nil
	}
	row, err := a.DB.Queries().GetGithubConnection(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return api.GetGithubConnection200JSONResponse{Configured: false, Kind: api.GithubConnectionKindNone, ApiUrl: github.DefaultAPI, AppAvailable: appsOK}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetGithubConnection200JSONResponse{Configured: true, Kind: api.GithubConnectionKindToken, TokenLast4: &row.TokenLast4, ApiUrl: row.ApiUrl, AppAvailable: appsOK}, nil
}

// SetGithubConnection checks the token with GitHub, then stores it encrypted (SDD §14.1).
func (a *API) SetGithubConnection(ctx context.Context, req api.SetGithubConnectionRequestObject) (api.SetGithubConnectionResponseObject, error) {
	if app, err := a.appConn(ctx); err != nil {
		return nil, err
	} else if app != nil {
		return nil, kernel.Invalid("app_exists", "The workspace reads GitHub with its App. Disconnect the App first: a workspace holds an App or a token, not both.")
	}
	token := strings.TrimSpace(req.Body.Token)
	apiURL := github.DefaultAPI
	if req.Body.ApiUrl != nil && strings.TrimSpace(*req.Body.ApiUrl) != "" {
		apiURL = strings.TrimRight(strings.TrimSpace(*req.Body.ApiUrl), "/")
		if u, err := url.Parse(apiURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, kernel.Invalid("bad_api_url", "The GitHub API address must be an https URL, such as https://github.example.com/api/v3.")
		}
	}
	login, err := (&github.Client{API: apiURL, Token: token}).User(ctx)
	if err != nil {
		return nil, kernel.Invalid("github_token_refused", "GitHub did not accept the token: %s.", strings.TrimSuffix(err.Error(), "."))
	}
	sealed, last4, err := a.seal(token)
	if err != nil {
		return nil, err
	}
	if err := a.DB.Queries().UpsertGithubConnection(ctx, pgdb.UpsertGithubConnectionParams{
		WorkspaceID: a.Workspace, TokenEncrypted: sealed, TokenLast4: last4, ApiUrl: apiURL,
		UpdatedBy: kernel.ActorFrom(ctx).UserID, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	return api.SetGithubConnection200JSONResponse{Configured: true, Kind: api.GithubConnectionKindToken, TokenLast4: &last4, ApiUrl: apiURL, Login: &login, AppAvailable: a.GitHubApp != nil}, nil
}

// DeleteGithubConnection removes the token.
func (a *API) DeleteGithubConnection(ctx context.Context, _ api.DeleteGithubConnectionRequestObject) (api.DeleteGithubConnectionResponseObject, error) {
	if err := a.DB.Queries().DeleteGithubConnection(ctx, a.Workspace); err != nil {
		return nil, err
	}
	return api.DeleteGithubConnection204Response{}, nil
}
