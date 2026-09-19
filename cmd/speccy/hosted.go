package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alternayte/speccy/internal/app"
	"github.com/alternayte/speccy/internal/authinvite"
	"github.com/alternayte/speccy/internal/features/admin"
	"github.com/alternayte/speccy/internal/hostauth"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
	"github.com/alternayte/speccy/web"
)

// hostedConfig is the environment of hosted mode (SDD §15.1).
type hostedConfig struct {
	databaseURL string
	masterKey   string
	baseURL     string
	listen      string
	logLevel    string
	oidc        *hostauth.Provider
	github      *hostauth.Provider
}

// loadHostedConfig reads the environment. It names every missing or bad variable at once.
func loadHostedConfig(getenv func(string) string) (hostedConfig, error) {
	c := hostedConfig{
		databaseURL: getenv("SPECCY_DATABASE_URL"), masterKey: getenv("SPECCY_MASTER_KEY"), baseURL: strings.TrimSuffix(getenv("SPECCY_BASE_URL"), "/"),
		listen: getenv("SPECCY_LISTEN"), logLevel: getenv("SPECCY_LOG_LEVEL"),
	}
	if c.listen == "" {
		c.listen = ":8080"
	}
	if c.logLevel == "" {
		c.logLevel = "info"
	}
	var problems []string
	for name, v := range map[string]string{"SPECCY_DATABASE_URL": c.databaseURL, "SPECCY_MASTER_KEY": c.masterKey, "SPECCY_BASE_URL": c.baseURL} {
		if v == "" {
			problems = append(problems, name+" is not set")
		}
	}
	if c.baseURL != "" {
		if u, err := url.Parse(c.baseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			problems = append(problems, "SPECCY_BASE_URL must be an http or https URL, such as https://speccy.example.com")
		}
	}
	if iss, id, sec := getenv("SPECCY_OIDC_ISSUER"), getenv("SPECCY_OIDC_CLIENT_ID"), getenv("SPECCY_OIDC_CLIENT_SECRET"); iss+id+sec != "" {
		if iss == "" || id == "" || sec == "" {
			problems = append(problems, "OIDC needs SPECCY_OIDC_ISSUER, SPECCY_OIDC_CLIENT_ID, and SPECCY_OIDC_CLIENT_SECRET together")
		}
		c.oidc = &hostauth.Provider{Issuer: iss, ClientID: id, ClientSecret: sec}
	}
	if id, sec := getenv("SPECCY_GITHUB_OAUTH_CLIENT_ID"), getenv("SPECCY_GITHUB_OAUTH_CLIENT_SECRET"); id+sec != "" {
		if id == "" || sec == "" {
			problems = append(problems, "GitHub sign-in needs SPECCY_GITHUB_OAUTH_CLIENT_ID and SPECCY_GITHUB_OAUTH_CLIENT_SECRET together")
		}
		c.github = &hostauth.Provider{ClientID: id, ClientSecret: sec}
	}
	if len(problems) > 0 {
		return c, errors.New(strings.Join(problems, "; "))
	}
	return c, nil
}

func (c hostedConfig) insecure() bool { return strings.HasPrefix(c.baseURL, "http://") }

// openHosted opens and migrates the Postgres store and the sign-in.
func openHosted(ctx context.Context, c hostedConfig) (*store.DB, *kernel.Sealer, *hostauth.Auth, error) {
	sealer, err := kernel.SealerFromBase64(c.masterKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("SPECCY_MASTER_KEY: %w. Make one with: openssl rand -base64 32", err)
	}
	db, err := store.OpenPostgres(ctx, c.databaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	ws, err := db.Workspace(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	auth, err := hostauth.New(hostauth.Config{DB: db, Workspace: ws, BaseURL: c.baseURL, OIDC: c.oidc, GitHub: c.github, Insecure: c.insecure()})
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	if err := auth.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	return db, sealer, auth, nil
}

// runHosted is `speccy serve --hosted`.
func runHosted(stdout, stderr io.Writer) int {
	c, err := loadHostedConfig(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitUsage
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.logLevel)); err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: SPECCY_LOG_LEVEL %q is not debug, info, warn, or error.\n", c.logLevel)
		return exitUsage
	}
	// SDD §15.2: JSON logs in hosted mode.
	slog.SetDefault(slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, sealer, auth, err := openHosted(ctx, c)
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}
	defer func() { _ = db.Close() }()
	a, err := app.New(ctx, db, sealer, nil, "")
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}
	a.Admin.Accounts = auth
	a.Share.Sealer, a.Share.BaseURL, a.Share.Secure = sealer, c.baseURL, !c.insecure()
	a.API.Hosted = true
	spa, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}
	h := speccyhttp.Handler(spa, a.API, speccyhttp.Options{
		Actor: auth.Actor(a.Share.Guest), Authz: &speccyhttp.Authz{DB: db, Workspace: a.Workspace}, Auth: auth.Handler(),
	})
	ln, err := net.Listen("tcp", c.listen)
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "Speccy (hosted) is listening on %s for %s\n", ln.Addr(), c.baseURL)
	if err := speccyhttp.Serve(ctx, ln, h); err != nil {
		fmt.Fprintf(stderr, "Speccy stopped: %v.\n", err)
		return exitRun
	}
	return exitOK
}

// runAdmin is `speccy admin invite --role admin|member` (REQ-083) and
// `speccy admin reset-link <email>` (REQ-082). They read the hosted environment.
func runAdmin(args []string, stdout, stderr io.Writer) int {
	const adminUsage = `Usage:
  speccy admin invite --role admin|member   Print a single-use invite link.
  speccy admin reset-link <email>           Print a one-time password reset link.

Both read SPECCY_DATABASE_URL, SPECCY_MASTER_KEY, and SPECCY_BASE_URL.
`
	if len(args) == 0 {
		fmt.Fprint(stderr, adminUsage)
		return exitUsage
	}
	c, err := loadHostedConfig(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "%v.\n", err)
		return exitUsage
	}
	ctx := context.Background()
	switch args[0] {
	case "invite":
		fl := flag.NewFlagSet("invite", flag.ContinueOnError)
		fl.SetOutput(stderr)
		role := fl.String("role", kernel.RoleMember, "admin or member")
		if err := fl.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if *role != kernel.RoleAdmin && *role != kernel.RoleMember {
			fmt.Fprintf(stderr, "--role %q is not admin or member.\n", *role)
			return exitUsage
		}
		db, _, _, err := openHosted(ctx, c)
		if err != nil {
			fmt.Fprintf(stderr, "%v.\n", err)
			return exitRun
		}
		defer func() { _ = db.Close() }()
		ws, err := db.Workspace(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%v.\n", err)
			return exitRun
		}
		s, _ := admin.LoadSettings(ctx, db.Queries(), ws)
		link, inv, err := authinvite.CreateInvite(ctx, db, ws, c.baseURL, *role, "cli", s.InviteTTL())
		if err != nil {
			fmt.Fprintf(stderr, "%v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "%s\nThe link works once, for the %s role, until %s.\n", link, *role, inv.ExpiresAt.Format("2006-01-02 15:04 MST"))
		return exitOK
	case "reset-link":
		if len(args) != 2 {
			fmt.Fprint(stderr, adminUsage)
			return exitUsage
		}
		db, _, auth, err := openHosted(ctx, c)
		if err != nil {
			fmt.Fprintf(stderr, "%v.\n", err)
			return exitRun
		}
		defer func() { _ = db.Close() }()
		link, err := auth.CreateResetLink(ctx, args[1], "cli")
		if err != nil {
			if ke, ok := kernel.AsError(err); ok {
				fmt.Fprintf(stderr, "%s\n", ke.Detail)
				return exitUsage
			}
			fmt.Fprintf(stderr, "%v.\n", err)
			return exitRun
		}
		fmt.Fprintf(stdout, "%s\nThe link works once, for 24 hours.\n", link)
		return exitOK
	}
	fmt.Fprint(stderr, adminUsage)
	return exitUsage
}
