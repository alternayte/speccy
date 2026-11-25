package github

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// App is a GitHub App (docs/specs/github-app.md): its ID and the keys that the manifest
// conversion returned.
type App struct {
	API           string // the REST API, such as https://api.github.com
	ID            int64
	Slug          string
	ClientID      string
	ClientSecret  string
	PrivateKey    []byte // PEM
	WebhookSecret string
	HTTP          *http.Client
}

// WebURL returns the web host of an API address: https://github.com for the public API, and
// https://<host> for GitHub Enterprise Server (https://<host>/api/v3).
func WebURL(apiURL string) string {
	if apiURL == "" || strings.EqualFold(strings.TrimRight(apiURL, "/"), DefaultAPI) {
		return "https://github.com"
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "https://github.com"
	}
	return u.Scheme + "://" + u.Host
}

// InstallURL is the page where an account installs the App.
func (a *App) InstallURL() string {
	return WebURL(a.API) + "/apps/" + url.PathEscape(a.Slug) + "/installations/new"
}

// SettingsURL is the App's settings page on GitHub, where its owner deletes it.
func SettingsURL(apiURL, slug, owner string, ownerIsOrg bool) string {
	if ownerIsOrg {
		return WebURL(apiURL) + "/organizations/" + url.PathEscape(owner) + "/settings/apps/" + url.PathEscape(slug)
	}
	return WebURL(apiURL) + "/settings/apps/" + url.PathEscape(slug)
}

// JWT signs the App's short-lived token: RS256, issued 60 seconds in the past against clock
// drift, valid for 9 minutes (GitHub allows 10).
func (a *App) JWT(now time.Time) (string, error) {
	key, err := parseKey(a.PrivateKey)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(a.ID, 10),
	})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func parseKey(p []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(p)
	if block == nil {
		return nil, errors.New("the GitHub App private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App private key cannot be read: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App private key is not an RSA key")
	}
	return rk, nil
}

// appClient calls the API as the App itself.
func (a *App) appClient() (*Client, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	return &Client{API: a.API, Token: jwt, HTTP: a.HTTP}, nil
}

// Installation is one installation of the App.
type Installation struct {
	ID      int64 `json:"id"`
	AppID   int64 `json:"app_id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	SuspendedAt *time.Time `json:"suspended_at"`
	// Permissions are the permissions the installation granted, such as "checks": "write".
	Permissions map[string]string `json:"permissions"`
	// Events are the webhook events the installation receives.
	Events []string `json:"events"`
}

// permissionAliases names the key under which an installation reports a manifest permission,
// where GitHub uses another name: the manifest's emails comes back as email_addresses (#168).
var permissionAliases = map[string]string{"emails": "email_addresses"}

// Missing lists what the installation lacks of the manifest of NewManifest: a permission
// below the level it asks for, or an event it does not receive. GitHub has no API to change
// an existing App, so its owner adds them on the App's settings page.
func (in Installation) Missing() []string {
	var out []string
	for name, level := range NewManifest("", "", "", false).DefaultPermissions {
		got := in.Permissions[name]
		if got == "" && permissionAliases[name] != "" {
			got = in.Permissions[permissionAliases[name]]
		}
		if got == "" || (level == "write" && got != "write" && got != "admin") {
			out = append(out, name+": "+level)
		}
	}
	for _, e := range NewManifest("", "", "", false).DefaultEvents {
		if !slices.Contains(in.Events, e) {
			out = append(out, "event "+e)
		}
	}
	slices.Sort(out)
	return out
}

// GetInstallation reads one installation of this App. GitHub answers 404 for an installation
// of another App, so a forged installation ID fails here.
func (a *App) GetInstallation(ctx context.Context, id int64) (Installation, error) {
	c, err := a.appClient()
	if err != nil {
		return Installation{}, err
	}
	var in Installation
	err = c.do(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(id, 10), nil, &in)
	return in, err
}

// Installations lists the installations of the App.
func (a *App) Installations(ctx context.Context) ([]Installation, error) {
	c, err := a.appClient()
	if err != nil {
		return nil, err
	}
	var out []Installation
	err = c.do(ctx, http.MethodGet, "/app/installations?per_page=100", nil, &out)
	return out, err
}

// Info is the App as GitHub describes it.
type Info struct {
	ID    int64  `json:"id"`
	Slug  string `json:"slug"`
	Owner struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// Info reads the App, which proves that its ID and key belong together.
func (a *App) Info(ctx context.Context) (Info, error) {
	c, err := a.appClient()
	if err != nil {
		return Info{}, err
	}
	var out Info
	err = c.do(ctx, http.MethodGet, "/app", nil, &out)
	return out, err
}

// tokenRenewal is how long before its end an installation token is renewed.
const tokenRenewal = 10 * time.Minute

// Tokens mints installation tokens and keeps each in memory until 10 minutes before its end.
// The store never holds one.
type Tokens struct {
	mu sync.Mutex
	m  map[string]cachedToken
}

type cachedToken struct {
	token string
	until time.Time
}

// Installation returns a client with a token of the installation.
func (t *Tokens) Installation(ctx context.Context, a *App, installation int64) (*Client, error) {
	key := fmt.Sprintf("%s|%d|%d", a.API, a.ID, installation)
	t.mu.Lock()
	c, ok := t.m[key]
	t.mu.Unlock()
	if ok && time.Now().Add(tokenRenewal).Before(c.until) {
		return &Client{API: a.API, Token: c.token, HTTP: a.HTTP, InstallURL: a.InstallURL()}, nil
	}
	ac, err := a.appClient()
	if err != nil {
		return nil, err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := ac.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(installation, 10)+"/access_tokens", nil, &out); err != nil {
		return nil, fmt.Errorf("GitHub gave no token for the App's installation: %w", err)
	}
	t.mu.Lock()
	if t.m == nil {
		t.m = map[string]cachedToken{}
	}
	t.m[key] = cachedToken{token: out.Token, until: out.ExpiresAt}
	t.mu.Unlock()
	return &Client{API: a.API, Token: out.Token, HTTP: a.HTTP, InstallURL: a.InstallURL()}, nil
}

// Forget drops the cached tokens, after the App or its installation changed.
func (t *Tokens) Forget() {
	t.mu.Lock()
	t.m = nil
	t.mu.Unlock()
}

// Membership says whether login is a member of org, with a token of the App's installation
// on that org (Members: read).
func (c *Client) Membership(ctx context.Context, org, login string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/orgs/"+url.PathEscape(org)+"/members/"+url.PathEscape(login), nil, nil)
	if err == nil {
		return true, nil
	}
	var e *Error
	if errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Status == http.StatusFound) {
		return false, nil
	}
	return false, err
}

// Conversion is what GitHub returns when a manifest code is converted into an App.
type Conversion struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// ConvertManifest turns the code of the manifest flow into the App and its keys. The code
// works once, within one hour, and the call needs no credential.
func ConvertManifest(ctx context.Context, hc *http.Client, apiURL, code string) (Conversion, error) {
	c := &Client{API: apiURL, HTTP: hc}
	var out Conversion
	err := c.do(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &out)
	return out, err
}

// Manifest is the App that Speccy asks GitHub to create.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description"`
	HookAttributes     map[string]any    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	CallbackURLs       []string          `json:"callback_urls"`
	SetupURL           string            `json:"setup_url"`
	SetupOnUpdate      bool              `json:"setup_on_update"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

// NewManifest builds the manifest of a private App for Speccy at baseURL. An App on an org
// also reads the org's members, for the membership check at sign-in.
func NewManifest(name, baseURL, callbackURL string, org bool) Manifest {
	perms := map[string]string{
		"contents":      "write", // read sources, commit doc changes
		"pull_requests": "write", // open the publish pull request, read its state
		"issues":        "write", // the breakdown's work items
		"checks":        "write", // one check per bundle on a pull request (docs/specs/hosted-pr-review.md)
		"metadata":      "read",
		"emails":        "read", // the verified address of a person who signs in
	}
	if org {
		perms["members"] = "read"
	}
	return Manifest{
		Name: name, URL: baseURL, Description: "Speccy reviews spec docs in this account's repos.",
		HookAttributes:     map[string]any{"url": baseURL + "/api/github/webhook", "active": true},
		RedirectURL:        baseURL + "/api/github/app/created",
		CallbackURLs:       []string{callbackURL},
		SetupURL:           baseURL + "/api/github/app/installed",
		SetupOnUpdate:      true,
		Public:             false,
		DefaultPermissions: perms,
		DefaultEvents:      []string{"push", "pull_request", "issue_comment", "pull_request_review_comment"},
	}
}

// ManifestFormURL is where the browser posts the manifest: an org's page, or the person's own
// account when org is empty.
func ManifestFormURL(apiURL, org, state string) string {
	base := WebURL(apiURL) + "/settings/apps/new"
	if org != "" {
		base = WebURL(apiURL) + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return base + "?state=" + url.QueryEscape(state)
}

// ValidSignature checks X-Hub-Signature-256 against the webhook secret, in constant time.
func ValidSignature(secret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || secret == "" {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// UserToken is a GitHub user access token of the App, with its refresh token. GitHub ends an
// access token after 8 hours and a refresh token after 6 months, unless the App opted out.
type UserToken struct {
	Access         string
	AccessExpires  time.Time // zero: no end
	Refresh        string
	RefreshExpires time.Time
}

// ErrUserTokenRefused says that GitHub refused a user token or its refresh: the person must
// connect GitHub again.
var ErrUserTokenRefused = errors.New("GitHub refused the person's token")

// ExchangeCode redeems a code of the App's user OAuth.
func (a *App) ExchangeCode(ctx context.Context, code, redirectURI string) (UserToken, error) {
	return a.userToken(ctx, url.Values{"code": {code}, "redirect_uri": {redirectURI}})
}

// RefreshUserToken trades a refresh token for a new pair. A refresh token works once.
func (a *App) RefreshUserToken(ctx context.Context, refresh string) (UserToken, error) {
	return a.userToken(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

func (a *App) userToken(ctx context.Context, form url.Values) (UserToken, error) {
	form.Set("client_id", a.ClientID)
	form.Set("client_secret", a.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, WebURL(a.API)+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return UserToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return UserToken{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out struct {
		AccessToken           string `json:"access_token"`
		ExpiresIn             int64  `json:"expires_in"`
		RefreshToken          string `json:"refresh_token"`
		RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
		Error                 string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || res.StatusCode != http.StatusOK || out.Error != "" || out.AccessToken == "" {
		return UserToken{}, ErrUserTokenRefused
	}
	now := time.Now().UTC()
	t := UserToken{Access: out.AccessToken, Refresh: out.RefreshToken}
	if out.ExpiresIn > 0 {
		t.AccessExpires = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	if out.RefreshTokenExpiresIn > 0 {
		t.RefreshExpires = now.Add(time.Duration(out.RefreshTokenExpiresIn) * time.Second)
	}
	return t, nil
}

// GitHubUser is the account of a user token.
type GitHubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// AvatarURL is the picture of the account.
	AvatarURL string `json:"avatar_url"`
}

// Me reads the account of the client's user token.
func (c *Client) Me(ctx context.Context) (GitHubUser, error) {
	var u GitHubUser
	err := c.do(ctx, http.MethodGet, "/user", nil, &u)
	return u, err
}

// VerifiedEmail returns the primary verified address of the client's user, else any verified
// one, else "".
func (c *Client) VerifiedEmail(ctx context.Context) (string, error) {
	var es []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := c.do(ctx, http.MethodGet, "/user/emails", nil, &es); err != nil {
		return "", err
	}
	for _, e := range es {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range es {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", nil
}
