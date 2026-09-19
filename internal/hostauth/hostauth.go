// Package hostauth builds the sign-in of hosted mode on auth-all (DEC-016): email and
// password, optional OIDC and GitHub (REQ-080), the admin and member roles, API tokens
// (REQ-110), and Speccy's invite plugin. It turns each request into a kernel.Actor.
package hostauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	authall "github.com/alternayte/auth-all"
	"github.com/alternayte/auth-all/migrations"
	"github.com/alternayte/auth-all/oauth"
	"github.com/alternayte/auth-all/oauth/github"
	"github.com/alternayte/auth-all/oauth/oidc"
	"github.com/alternayte/auth-all/plugins/admin"
	"github.com/alternayte/auth-all/plugins/apikeys"
	"github.com/alternayte/auth-all/plugins/roles"
	"github.com/alternayte/auth-all/ratelimit"
	"github.com/alternayte/auth-all/ratelimit/storelimit"
	"github.com/alternayte/auth-all/schema"
	authpg "github.com/alternayte/auth-all/store/postgres"
	authsqlite "github.com/alternayte/auth-all/store/sqlite"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/authinvite"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Prefix names every auth-all table, so they sit apart from Speccy's tables.
const Prefix = "auth_"

// versionTable is the goose version table of the auth-all migrations.
const versionTable = "auth_goose_db_version"

// OIDCProvider is the auth-all ID of the OIDC provider: /api/auth/oauth/oidc.
const OIDCProvider = "oidc"

// APIKeyPrefix starts every personal API token.
const APIKeyPrefix = "spy_"

// Config is the hosted sign-in configuration (SDD §15.1).
type Config struct {
	DB        *store.DB
	Workspace uuid.UUID
	// BaseURL is SPECCY_BASE_URL: the public URL, for links, redirects, and cookies.
	BaseURL string
	// OIDC and GitHub are optional sign-in providers (REQ-080). A provider signs in an
	// existing user only: accounts come from invites.
	OIDC   *Provider
	GitHub *Provider
	// Insecure allows a plain HTTP BaseURL (development only): the cookies lose Secure.
	Insecure bool
	// Now is for tests.
	Now func() time.Time
}

// Provider is an OAuth client.
type Provider struct {
	Issuer       string // OIDC only
	ClientID     string
	ClientSecret string
}

// Auth is the hosted sign-in.
type Auth struct {
	All     *authall.Auth
	Admin   *admin.Plugin
	Invites *authinvite.Plugin
	// Providers are the configured OAuth provider IDs.
	Providers []string
	limiter   *storelimit.Limiter
	db        *store.DB
	ws        uuid.UUID
	baseURL   string
}

// New builds the auth-all instance.
func New(cfg Config) (*Auth, error) {
	r := roles.New(roles.Hierarchy(kernel.RoleMember, kernel.RoleAdmin), roles.Default(kernel.RoleMember))
	adm := admin.New(admin.AdminRole(kernel.RoleAdmin))
	inv := &authinvite.Plugin{DB: cfg.DB, Workspace: cfg.Workspace, Admin: adm, BaseURL: cfg.BaseURL}
	var st = authsqlite.New(cfg.DB.SQL)
	if cfg.DB.Engine == store.Postgres {
		st = authpg.New(cfg.DB.SQL)
	}
	// SDD §14.2: rate limits on the auth endpoints, counted in the database, so every
	// instance shares one count.
	limiter, err := storelimit.New(st, Rules())
	if err != nil {
		return nil, fmt.Errorf("set up the rate limits: %w", err)
	}
	opts := []authall.Option{
		authall.WithStore(st),
		authall.WithRateLimiter(limiter),
		authall.WithStrictRateLimiting(),
		authall.WithSchema(schema.Options{Prefix: Prefix, IDType: schema.IDUUID}),
		authall.WithBaseURL(cfg.BaseURL),
		authall.WithEmailPassword(),
		authall.WithPasswordPolicy(authall.PasswordPolicy{MinLength: authinvite.MinPassword, MaxLength: 4096}),
		authall.WithPlugins(r, adm, apikeys.New(apikeys.Prefix(APIKeyPrefix), apikeys.AllowNoExpiry()), inv),
		authall.WithSchemaCheck(authall.SchemaCheckCatalog),
	}
	if cfg.Insecure {
		secure := false
		opts = append(opts, authall.WithCookie(authall.CookieOptions{Secure: &secure}))
	}
	if cfg.Now != nil {
		opts = append(opts, authall.WithClock(cfg.Now))
	}
	var providers []oauth.Provider
	if p := cfg.OIDC; p != nil {
		// A fixed ID keeps the callback URL and the linked accounts when the issuer changes.
		providers = append(providers, oidc.New(oidc.WithID(OIDCProvider), oidc.WithIssuer(p.Issuer), oidc.WithClientID(p.ClientID), oidc.WithClientSecret(p.ClientSecret)))
	}
	if p := cfg.GitHub; p != nil {
		providers = append(providers, github.New(github.WithClientID(p.ClientID), github.WithClientSecret(p.ClientSecret)))
	}
	if len(providers) > 0 {
		opts = append(opts, authall.WithProvider(providers...))
	}
	all, err := authall.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("set up sign-in: %w", err)
	}
	ids := make([]string, len(providers))
	for i, p := range providers {
		ids[i] = p.ID()
	}
	return &Auth{All: all, Admin: adm, Invites: inv, Providers: ids, limiter: limiter, db: cfg.DB, ws: cfg.Workspace, baseURL: cfg.BaseURL}, nil
}

// Rules are the rate limits of hosted mode: auth-all's sign-in defaults, and limits for the
// other routes that take a secret.
func Rules() []ratelimit.Rule {
	ip := func(op ratelimit.Operation, limit int, window time.Duration) ratelimit.Rule {
		return ratelimit.Rule{Operation: op, Scope: ratelimit.ScopeIP, Limit: limit, Window: window}
	}
	return append(ratelimit.DefaultSignInRules(),
		ip(ratelimit.OpTOTP, 10, time.Minute),
		ip(ratelimit.OpPasswordChange, 10, 15*time.Minute),
		ip(authinvite.OpInviteCheck, 30, time.Minute),
		ip(authinvite.OpInviteAccept, 10, time.Hour),
		ip(authinvite.OpResetCheck, 30, time.Minute),
		ip(authinvite.OpReset, 10, time.Hour),
	)
}

// CleanUp removes expired rate-limit counters every hour until ctx ends.
func (a *Auth) CleanUp(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := a.limiter.Cleanup(ctx, time.Now().Add(-time.Hour)); err != nil && ctx.Err() == nil {
				slog.Error("clean-up of the rate-limit counters failed", "err", err)
			}
		}
	}
}

// Migrate applies the auth-all migrations, in their own goose version table, and checks the
// schema.
func (a *Auth) Migrate(ctx context.Context) error {
	d := schema.Postgres
	if a.db.Engine == store.SQLite {
		d = schema.SQLite
	}
	files, err := a.All.ExportMigrations(d, migrations.Goose)
	if err != nil {
		return err
	}
	fsys := fstest.MapFS{}
	for _, f := range files {
		fsys[f.Name] = &fstest.MapFile{Data: []byte(f.Content)}
	}
	if err := a.db.MigrateSet(ctx, fsys, versionTable); err != nil {
		return err
	}
	return a.All.CheckSchema(ctx)
}

// Handler serves auth-all at /api/auth.
func (a *Auth) Handler() http.Handler { return a.All.Handler() }

// Actor sets the actor of each API request: a signed-in user or an API token (auth-all), else a
// guest from the signed guest cookie, else nobody. guest may be nil.
func (a *Auth) Actor(guest func(context.Context, *http.Request) *kernel.Guest) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return a.All.LoadSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var actor kernel.Actor
			if p := authall.PrincipalFrom(ctx); p != nil && p.User != nil {
				if p.User.MustChangePassword {
					writeJSON(w, http.StatusForbidden, "password_change_required", "Change your password before you go on.")
					return
				}
				role := p.Role
				if role != kernel.RoleAdmin {
					role = kernel.RoleMember
				}
				actor = kernel.Actor{UserID: p.User.ID, Email: p.User.Email, Role: role, APIKey: p.Method == authall.MethodAPIKey}
			} else if guest != nil {
				if g := guest(ctx, r); g != nil {
					actor = kernel.Actor{Guest: g}
				}
			}
			next.ServeHTTP(w, r.WithContext(kernel.WithActor(ctx, actor)))
		}))
	}
}

func writeJSON(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":%q,"status":%d,"code":%q,"detail":%q}`, http.StatusText(status), status, code, detail)
}

// CreateInvite makes an invite link (the admin screen's Accounts).
func (a *Auth) CreateInvite(ctx context.Context, role, createdBy string, ttl time.Duration) (string, pgdb.Invite, error) {
	return authinvite.CreateInvite(ctx, a.db, a.ws, a.baseURL, role, createdBy, ttl)
}

// CreateResetLink makes a password reset link.
func (a *Auth) CreateResetLink(ctx context.Context, email, createdBy string) (string, error) {
	return a.Invites.CreateResetLink(ctx, email, createdBy)
}
