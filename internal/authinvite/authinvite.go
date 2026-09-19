// Package authinvite is the auth-all plugin for Speccy's accounts (DEC-016): invite links
// that create an account with a role (REQ-081, REQ-083), and one-time password reset links
// that an admin makes (REQ-082). Nothing is mailed: an admin passes the link on.
//
// It also closes open sign-up. A user is created only through an invite, or by an admin.
package authinvite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/auth-all/apierr"
	"github.com/alternayte/auth-all/hook"
	"github.com/alternayte/auth-all/plugin"
	"github.com/alternayte/auth-all/plugins/admin"
	"github.com/alternayte/auth-all/ratelimit"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// ID is the plugin ID.
const ID = "speccy-invite"

// MinPassword is the shortest password Speccy accepts. The auth-all policy uses the same value.
const MinPassword = 12

// ResetTTL is how long a reset link works.
const ResetTTL = 24 * time.Hour

// DefaultInviteTTL is REQ-081's default expiry.
const DefaultInviteTTL = 7 * 24 * time.Hour

// Rate-limit operations of the plugin's routes. hostauth sets their rules.
const (
	OpInviteCheck  ratelimit.Operation = "speccy-invite-check"
	OpInviteAccept ratelimit.Operation = "speccy-invite-accept"
	OpResetCheck   ratelimit.Operation = "speccy-reset-check"
	OpReset        ratelimit.Operation = "speccy-reset"
)

// Errors of the accept and reset routes. One message for every invalid link, so a holder of a
// token learns nothing about it.
var (
	errLinkInvalid = apierr.New("LINK_INVALID", http.StatusBadRequest, "The link is invalid, used, or expired. Ask an admin for a new one.")
	errSignUpOff   = apierr.New("SIGN_UP_CLOSED", http.StatusForbidden, "Speccy has no open sign-up. Ask an admin for an invite link.")
	errWeak        = apierr.New("WEAK_PASSWORD", http.StatusBadRequest, "The password needs at least 12 characters.")
)

// Plugin is the invite and reset plugin.
type Plugin struct {
	DB        *store.DB
	Workspace uuid.UUID
	Admin     *admin.Plugin
	// BaseURL is the public URL of Speccy, for the links.
	BaseURL string

	svc plugin.Services
}

func (p *Plugin) ID() string { return ID }

type allowKey struct{}

// Allow marks ctx as a user creation that Speccy started: an invite acceptance, or an admin
// action. Every other creation (open sign-up, a first OAuth sign-in) is refused.
func Allow(ctx context.Context) context.Context { return context.WithValue(ctx, allowKey{}, true) }

// Register adds the routes and the sign-up gate.
func (p *Plugin) Register(r *plugin.Registry) error {
	p.svc = r.Services()
	r.Hooks().OnBeforeUserCreate(func(ctx context.Context, _ *hook.UserCreate) error {
		if allowed, _ := ctx.Value(allowKey{}).(bool); allowed {
			return nil
		}
		return errSignUpOff
	})
	r.Route(plugin.Route{Method: http.MethodPost, Path: "/speccy/invites/check", Handler: http.HandlerFunc(p.checkInvite)})
	r.Route(plugin.Route{Method: http.MethodPost, Path: "/speccy/invites/accept", Handler: http.HandlerFunc(p.acceptInvite)})
	r.Route(plugin.Route{Method: http.MethodPost, Path: "/speccy/reset/check", Handler: http.HandlerFunc(p.checkReset)})
	r.Route(plugin.Route{Method: http.MethodPost, Path: "/speccy/reset", Handler: http.HandlerFunc(p.reset)})
	return nil
}

// newToken returns 32 random bytes as base64url, and the SHA-256 hex that the store keeps.
func newToken() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, hashToken(plain), nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// link builds a link to an SPA page. The token is in the fragment, so it never reaches a
// server log or a Referer header.
func link(base, page, token string) string {
	return strings.TrimSuffix(base, "/") + page + "#token=" + token
}

// CreateInvite makes a single-use invite link for role (REQ-081). It needs no auth-all
// instance, so `speccy admin invite` can call it (REQ-083).
func CreateInvite(ctx context.Context, db *store.DB, workspace uuid.UUID, baseURL, role, createdBy string, ttl time.Duration) (string, pgdb.Invite, error) {
	if role != kernel.RoleAdmin && role != kernel.RoleMember {
		return "", pgdb.Invite{}, kernel.Invalid("role_unknown", "The role %q is not admin or member.", role)
	}
	if ttl <= 0 {
		ttl = DefaultInviteTTL
	}
	plain, hash, err := newToken()
	if err != nil {
		return "", pgdb.Invite{}, err
	}
	now := time.Now().UTC()
	inv := pgdb.Invite{ID: kernel.NewID(), WorkspaceID: workspace, TokenHash: hash, Role: role, ExpiresAt: now.Add(ttl), CreatedBy: createdBy, CreatedAt: now}
	err = db.Queries().InsertInvite(ctx, pgdb.InsertInviteParams{
		ID: inv.ID, WorkspaceID: workspace, TokenHash: hash, Role: role, ExpiresAt: inv.ExpiresAt, CreatedBy: createdBy, CreatedAt: now,
	})
	return link(baseURL, "/invite", plain), inv, err
}

// CreateResetLink makes a one-time password reset link for the user with email (REQ-082).
func (p *Plugin) CreateResetLink(ctx context.Context, email, createdBy string) (string, error) {
	u, err := p.svc.Users().ByEmail(ctx, email)
	if err != nil || u == nil {
		return "", kernel.NotFound("user_not_found", "No user has the email %s.", email)
	}
	plain, hash, err := newToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = p.DB.Queries().InsertResetLink(ctx, pgdb.InsertResetLinkParams{
		ID: kernel.NewID(), WorkspaceID: p.Workspace, TokenHash: hash, UserID: u.ID,
		ExpiresAt: now.Add(ResetTTL), CreatedBy: createdBy, CreatedAt: now,
	})
	return link(p.BaseURL, "/reset", plain), err
}

type tokenBody struct {
	Token string `json:"token"`
}

// limited applies the rate limit of auth-all's store limiter to one operation from one IP.
func (p *Plugin) limited(w http.ResponseWriter, r *http.Request, op ratelimit.Operation) bool {
	ok, err := p.svc.RateLimiter().Allow(r.Context(), ratelimit.Key{Operation: op, IP: p.svc.HTTP().ClientIP(r)})
	if err != nil || !ok {
		p.svc.HTTP().WriteError(w, apierr.ErrRateLimited)
		return true
	}
	return false
}

func (p *Plugin) checkInvite(w http.ResponseWriter, r *http.Request) {
	if p.limited(w, r, OpInviteCheck) {
		return
	}
	var body tokenBody
	if err := p.svc.HTTP().DecodeJSON(r, &body); err != nil {
		p.svc.HTTP().WriteError(w, apierr.ErrInvalidRequest)
		return
	}
	inv, err := p.DB.Queries().PeekInvite(r.Context(), pgdb.PeekInviteParams{TokenHash: hashToken(body.Token), Now: time.Now().UTC()})
	if err != nil {
		p.svc.HTTP().WriteError(w, errLinkInvalid)
		return
	}
	p.svc.HTTP().WriteJSON(w, http.StatusOK, map[string]any{"role": inv.Role, "expiresAt": inv.ExpiresAt})
}

type acceptBody struct {
	Token    string `json:"token"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// acceptInvite spends the invite, creates the user with the invite's role, and signs them in.
// The consumed token is the subject of the operation (auth-all's subject rule).
func (p *Plugin) acceptInvite(w http.ResponseWriter, r *http.Request) {
	h := p.svc.HTTP()
	if err := h.CheckOrigin(r); err != nil {
		h.WriteError(w, err)
		return
	}
	if p.limited(w, r, OpInviteAccept) {
		return
	}
	var body acceptBody
	if err := h.DecodeJSON(r, &body); err != nil || strings.TrimSpace(body.Email) == "" {
		h.WriteError(w, apierr.ErrInvalidRequest)
		return
	}
	if len([]rune(body.Password)) < MinPassword {
		h.WriteError(w, errWeak)
		return
	}
	ctx := r.Context()
	q := p.DB.Queries()
	now := time.Now().UTC()
	inv, err := q.SpendInvite(ctx, pgdb.SpendInviteParams{TokenHash: hashToken(body.Token), Now: now, UsedAt: sql.NullTime{Time: now, Valid: true},
		UsedBy: sql.NullString{String: strings.TrimSpace(body.Email), Valid: true}})
	if err != nil {
		h.WriteError(w, errLinkInvalid)
		return
	}
	user, _, err := p.Admin.CreateUser(Allow(ctx), admin.CreateUserInput{
		Email: strings.TrimSpace(body.Email), Name: strings.TrimSpace(body.Name), Role: inv.Role, Password: body.Password,
	})
	if err != nil {
		// The invite stays usable when the account could not be made (an address in use).
		_ = q.UnspendInvite(context.WithoutCancel(ctx), inv.ID)
		h.WriteError(w, err)
		return
	}
	challenge, required, err := p.svc.MFA().Challenge(ctx, user)
	if err != nil {
		h.WriteError(w, err)
		return
	}
	if required {
		h.WriteJSON(w, http.StatusOK, map[string]any{"mfaRequired": true, "mfaToken": challenge})
		return
	}
	if _, err := p.svc.Sessions().Issue(ctx, w, r, user, ID); err != nil {
		h.WriteError(w, err)
		return
	}
	h.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Plugin) checkReset(w http.ResponseWriter, r *http.Request) {
	if p.limited(w, r, OpResetCheck) {
		return
	}
	var body tokenBody
	if err := p.svc.HTTP().DecodeJSON(r, &body); err != nil {
		p.svc.HTTP().WriteError(w, apierr.ErrInvalidRequest)
		return
	}
	rl, err := p.DB.Queries().PeekResetLink(r.Context(), pgdb.PeekResetLinkParams{TokenHash: hashToken(body.Token), Now: time.Now().UTC()})
	if err != nil {
		p.svc.HTTP().WriteError(w, errLinkInvalid)
		return
	}
	u, err := p.svc.Users().ByID(r.Context(), rl.UserID)
	if err != nil || u == nil {
		p.svc.HTTP().WriteError(w, errLinkInvalid)
		return
	}
	p.svc.HTTP().WriteJSON(w, http.StatusOK, map[string]any{"email": u.Email})
}

type resetBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// reset spends a reset link and sets the new password. auth-all revokes every session of the
// user; they sign in with the new password.
func (p *Plugin) reset(w http.ResponseWriter, r *http.Request) {
	h := p.svc.HTTP()
	if err := h.CheckOrigin(r); err != nil {
		h.WriteError(w, err)
		return
	}
	if p.limited(w, r, OpReset) {
		return
	}
	var body resetBody
	if err := h.DecodeJSON(r, &body); err != nil {
		h.WriteError(w, apierr.ErrInvalidRequest)
		return
	}
	if len([]rune(body.Password)) < MinPassword {
		h.WriteError(w, errWeak)
		return
	}
	now := time.Now().UTC()
	rl, err := p.DB.Queries().SpendResetLink(r.Context(), pgdb.SpendResetLinkParams{TokenHash: hashToken(body.Token), Now: now, UsedAt: sql.NullTime{Time: now, Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		h.WriteError(w, errLinkInvalid)
		return
	}
	if err != nil {
		h.WriteError(w, err)
		return
	}
	if _, err := p.Admin.ResetPassword(r.Context(), rl.UserID, admin.ResetOptions{Password: body.Password}); err != nil {
		h.WriteError(w, err)
		return
	}
	h.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
