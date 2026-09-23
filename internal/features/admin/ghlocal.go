package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// LocalGitHubClient returns the client for a host in local mode (REQ-129). The token comes
// from the machine's gh login, and Speccy stores nothing gh returns, so a token revoked or
// switched in gh takes effect at once. When gh cannot give one, a token pasted in the app
// stands in, and the error says which of the three cases stopped it.
func (a *API) LocalGitHubClient(ctx context.Context, apiURL string) (*github.Client, error) {
	if apiURL == "" {
		apiURL = github.DefaultAPI
	}
	host := hostOf(apiURL)
	token, ghErr := github.GHToken(ctx, host)
	if ghErr == nil {
		return &github.Client{API: apiURL, Token: token}, nil
	}
	row, err := a.DB.Queries().GetGithubConnection(ctx, a.Workspace)
	if err == nil {
		stored, err := a.Sealer.Open(row.TokenEncrypted)
		if err != nil {
			return nil, err
		}
		return &github.Client{API: apiURL, Token: string(stored)}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	switch {
	case errors.Is(ghErr, github.ErrGHMissing):
		return nil, kernel.Invalid("gh_missing", "The gh CLI is not installed, so Speccy has no GitHub token. Install gh and run \"gh auth login\", or paste a fine-grained token in Admin → GitHub.")
	case errors.Is(ghErr, github.ErrGHLoggedOut):
		return nil, kernel.Invalid("gh_logged_out", "The gh login does not cover %s. Run \"gh auth login --hostname %s\", or paste a fine-grained token in Admin → GitHub.", host, host)
	default:
		return nil, kernel.Invalid("gh_failed", "Speccy could not get a token from gh: %s. Paste a fine-grained token in Admin → GitHub instead.", sentence(ghErr.Error()))
	}
}

// hostOf is the GitHub host of an API address. The API of github.com is api.github.com, and a
// GitHub Enterprise Server serves its API under the same host as the repos.
func hostOf(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "github.com"
	}
	if strings.EqualFold(u.Host, "api.github.com") {
		return "github.com"
	}
	return u.Host
}

func sentence(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))
	return s
}
