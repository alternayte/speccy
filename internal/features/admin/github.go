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

// GitHubClient returns the GitHub client with the workspace token (DEC-019).
func (a *API) GitHubClient(ctx context.Context) (*github.Client, error) {
	row, err := a.DB.Queries().GetGithubConnection(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoGitHubToken
	}
	if err != nil {
		return nil, err
	}
	token, err := a.Sealer.Open(row.TokenEncrypted)
	if err != nil {
		return nil, err
	}
	return &github.Client{API: row.ApiUrl, Token: string(token)}, nil
}

// GetGithubConnection shows whether a token is set, and its last 4 characters.
func (a *API) GetGithubConnection(ctx context.Context, _ api.GetGithubConnectionRequestObject) (api.GetGithubConnectionResponseObject, error) {
	row, err := a.DB.Queries().GetGithubConnection(ctx, a.Workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return api.GetGithubConnection200JSONResponse{Configured: false, ApiUrl: github.DefaultAPI}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetGithubConnection200JSONResponse{Configured: true, TokenLast4: &row.TokenLast4, ApiUrl: row.ApiUrl}, nil
}

// SetGithubConnection checks the token with GitHub, then stores it encrypted (SDD §14.1).
func (a *API) SetGithubConnection(ctx context.Context, req api.SetGithubConnectionRequestObject) (api.SetGithubConnectionResponseObject, error) {
	if a.Accounts == nil {
		return nil, kernel.Invalid("github_unavailable", "GitHub sources are for hosted mode. In local mode, clone the repo and run speccy in it.")
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
	return api.SetGithubConnection200JSONResponse{Configured: true, TokenLast4: &last4, ApiUrl: apiURL, Login: &login}, nil
}

// DeleteGithubConnection removes the token.
func (a *API) DeleteGithubConnection(ctx context.Context, _ api.DeleteGithubConnectionRequestObject) (api.DeleteGithubConnectionResponseObject, error) {
	if err := a.DB.Queries().DeleteGithubConnection(ctx, a.Workspace); err != nil {
		return nil, err
	}
	return api.DeleteGithubConnection204Response{}, nil
}
