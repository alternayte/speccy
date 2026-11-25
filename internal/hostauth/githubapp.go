package hostauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/alternayte/auth-all/apierr"
	"github.com/alternayte/auth-all/hook"
	"github.com/alternayte/auth-all/oauth"
	"github.com/alternayte/auth-all/plugin"
	authstore "github.com/alternayte/auth-all/store"

	"github.com/alternayte/speccy/internal/source/github"
)

// GitHubProvider is the auth-all ID of the GitHub sign-in. It keeps the ID of auth-all's own
// GitHub provider, so accounts linked before the App stay linked.
const GitHubProvider = "github"

// memberClaim carries the membership check from the exchange to the hooks.
const memberClaim = "speccy_member"

// GitHubApp is the workspace's GitHub App, as the sign-in needs it (docs/specs/github-app.md).
type GitHubApp interface {
	// SignInApp returns the App, or nil when the workspace has none: then GitHub sign-in is off.
	SignInApp(ctx context.Context) (*github.App, error)
	// MayJoin says whether a GitHub login belongs to the account that owns the App.
	MayJoin(ctx context.Context, login string) (bool, error)
	// SaveUserToken keeps the person's GitHub token, so a doc change goes out as them.
	SaveUserToken(ctx context.Context, githubUserID, login string, t github.UserToken) error
}

var errNotMember = apierr.New("GITHUB_NOT_MEMBER", http.StatusForbidden,
	"This GitHub account is not a member of the account that owns Speccy's GitHub App. Ask an admin for an invite link.")

// SetGitHubApp turns on the GitHub sign-in through the App. The App is read on each sign-in,
// so an App that an admin creates in the UI works at once.
func (a *Auth) SetGitHubApp(g GitHubApp) { a.gh.Store(&g) }

func (a *Auth) githubApp() GitHubApp {
	if p := a.gh.Load(); p != nil {
		return *p
	}
	return nil
}

// providerSource gives auth-all the GitHub provider while the workspace has an App.
func (a *Auth) providerSource(ctx context.Context) ([]oauth.Provider, error) {
	g := a.githubApp()
	if g == nil {
		return nil, nil
	}
	app, err := g.SignInApp(ctx)
	if err != nil || app == nil {
		return nil, err
	}
	return []oauth.Provider{&appProvider{app: app, g: g}}, nil
}

// SignInProviders lists the sign-in providers that work now.
func (a *Auth) SignInProviders(ctx context.Context) []string {
	out := append([]string(nil), a.Providers...)
	if ps, err := a.providerSource(ctx); err == nil && len(ps) > 0 {
		out = append(out, GitHubProvider)
	}
	return out
}

// GitHubAccount returns the GitHub account ID that a Speccy user linked.
func (a *Auth) GitHubAccount(ctx context.Context, userID string) (string, bool, error) {
	accts, err := a.All.Store().Accounts().ListByUser(ctx, userID)
	if err != nil {
		return "", false, err
	}
	for _, acct := range accts {
		if acct.Provider == GitHubProvider {
			return acct.ProviderAccountID, true, nil
		}
	}
	return "", false, nil
}

// UserOfGitHub returns the Speccy account that linked a GitHub account ID.
func (a *Auth) UserOfGitHub(ctx context.Context, githubID string) (string, bool, error) {
	acct, err := a.All.Store().Accounts().GetByProviderAccount(ctx, GitHubProvider, githubID)
	if errors.Is(err, authstore.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return acct.UserID, true, nil
}

// githubHooks refuses a GitHub sign-in or a link by an account outside the App's owner, and
// lets a member of the owning org make an account with the member role. After a sign-in or a
// link, it tells Speccy which GitHub account the person has.
type githubHooks struct{ auth *Auth }

func (*githubHooks) ID() string { return "speccy-github" }

func (h *githubHooks) Register(r *plugin.Registry) error {
	r.Hooks().OnBeforeOAuthSignIn(func(_ context.Context, ev *hook.OAuthSignIn) error {
		if ev.Provider == GitHubProvider && !member(ev.Identity) {
			return errNotMember
		}
		return nil
	})
	r.Hooks().OnAfterSignIn(func(ctx context.Context, ev *hook.SignIn) error {
		if ev.User != nil {
			h.linked(ctx, ev.User.ID)
		}
		return nil
	})
	r.Hooks().OnAfterAccountLink(func(ctx context.Context, ev *hook.AccountLink) error {
		if ev.User != nil && ev.Account != nil && ev.Account.Provider == GitHubProvider {
			h.linked(ctx, ev.User.ID)
		}
		return nil
	})
	return nil
}

// linked runs OnGitHub for a person with a linked GitHub account. A failure is logged: the
// sign-in itself has worked.
func (h *githubHooks) linked(ctx context.Context, userID string) {
	if h.auth == nil || h.auth.OnGitHub == nil {
		return
	}
	id, ok, err := h.auth.GitHubAccount(ctx, userID)
	if err != nil {
		slog.WarnContext(ctx, "the GitHub account of a person was not read", "err", err)
		return
	}
	if ok {
		h.auth.OnGitHub(ctx, userID, id)
	}
}

func member(id *oauth.Identity) bool {
	if id == nil {
		return false
	}
	v, _ := id.Claims.Get(memberClaim)
	ok, _ := v.(bool)
	return ok
}

// MayCreate is the sign-up gate for the GitHub sign-in (authinvite.Plugin.MayCreate): a member
// of the owning account gets an account with no invite.
func MayCreate(_ context.Context, ev *hook.UserCreate) bool {
	return ev.Provider == GitHubProvider && member(ev.Identity)
}

// appProvider is the GitHub sign-in through the App's user OAuth. Its exchange keeps the
// person's token for doc writes, and checks the membership.
type appProvider struct {
	app *github.App
	g   GitHubApp
}

func (p *appProvider) ID() string         { return GitHubProvider }
func (p *appProvider) SupportsPKCE() bool { return false }

func (p *appProvider) AuthCodeURL(req oauth.AuthRequest) (string, error) {
	q := url.Values{}
	q.Set("client_id", p.app.ClientID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("state", req.State)
	q.Set("allow_signup", "false")
	return github.WebURL(p.app.API) + "/login/oauth/authorize?" + q.Encode(), nil
}

func (p *appProvider) Exchange(ctx context.Context, req oauth.ExchangeRequest) (*oauth.Identity, error) {
	t, err := p.app.ExchangeCode(ctx, req.Code, req.RedirectURI)
	if errors.Is(err, github.ErrUserTokenRefused) {
		return nil, fmt.Errorf("%w: %v", oauth.ErrProviderRejected, err)
	}
	if err != nil {
		return nil, err
	}
	c := &github.Client{API: p.app.API, Token: t.Access, HTTP: p.app.HTTP}
	u, err := c.Me(ctx)
	if err != nil || u.ID == 0 {
		return nil, fmt.Errorf("%w: GitHub did not say who signed in", oauth.ErrProviderRejected)
	}
	ok, err := p.g.MayJoin(ctx, u.Login)
	if err != nil {
		return nil, err
	}
	id := strconv.FormatInt(u.ID, 10)
	if ok {
		if err := p.g.SaveUserToken(ctx, id, u.Login, t); err != nil {
			return nil, err
		}
	}
	email, _ := c.VerifiedEmail(ctx)
	name := u.Name
	if name == "" {
		name = u.Login
	}
	return &oauth.Identity{
		ProviderAccountID: id, Email: email, EmailVerified: email != "", DisplayName: name, ImageURL: u.AvatarURL,
		Username: u.Login, Claims: oauth.NewClaims(map[string]any{memberClaim: ok}),
	}, nil
}
