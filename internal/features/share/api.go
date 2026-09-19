package share

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// GuestCookie is the name of the signed guest cookie (REQ-086).
const GuestCookie = "speccy_guest"

// guestTTL is how long a guest cookie lasts. A revoked or expired share link ends it sooner.
const guestTTL = 30 * 24 * time.Hour

// API serves share links, guests, and bundle visibility.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Sealer    *kernel.Sealer
	// BaseURL is the public URL, for share links. Secure sets the Secure flag on the guest cookie.
	BaseURL string
	Secure  bool
}

var errShareGone = kernel.NotFound("share_not_found", "This share link does not work: it was revoked, it expired, or it is wrong. Ask the author for a new one.")

// HashToken is how a share token is stored (SDD §14.2).
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func (a *API) sharedBundle(ctx context.Context, token string) (pgdb.Bundle, error) {
	b, err := a.DB.Queries().BundleByShareToken(ctx, pgdb.BundleByShareTokenParams{ShareTokenHash: sql.NullString{String: HashToken(token), Valid: true}, Now: sql.NullTime{Time: time.Now().UTC(), Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return b, errShareGone
	}
	return b, err
}

// GetShare looks up a share link.
func (a *API) GetShare(ctx context.Context, req api.GetShareRequestObject) (api.GetShareResponseObject, error) {
	b, err := a.sharedBundle(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	return api.GetShare200JSONResponse{BundleId: b.ID, Title: b.Title}, nil
}

// joinResponse writes the guest cookie with the body.
type joinResponse struct {
	body   api.ShareInfo
	cookie *http.Cookie
}

func (r joinResponse) VisitJoinShareResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return api.JoinShare200JSONResponse(r.body).VisitJoinShareResponse(w)
}

// JoinShare records a guest with a display name and sets the signed guest cookie (REQ-086).
func (a *API) JoinShare(ctx context.Context, req api.JoinShareRequestObject) (api.JoinShareResponseObject, error) {
	if a.Sealer == nil {
		return nil, kernel.Invalid("hosted_only", "Share links work in hosted mode.")
	}
	name := strings.TrimSpace(req.Body.DisplayName)
	if name == "" || len([]rune(name)) > 60 {
		return nil, kernel.Invalid("bad_name", "Enter a display name of 1 to 60 characters.")
	}
	b, err := a.sharedBundle(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	g := pgdb.ShareGuest{ID: kernel.NewID(), BundleID: b.ID, DisplayName: name, CreatedAt: time.Now().UTC()}
	if err := a.DB.Queries().InsertShareGuest(ctx, pgdb.InsertShareGuestParams(g)); err != nil {
		return nil, err
	}
	exp := time.Now().Add(guestTTL)
	cookie := &http.Cookie{
		Name: GuestCookie, Value: a.signGuest(g.ID, b.ShareTokenHash.String, exp), Path: "/", Expires: exp,
		HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	}
	return joinResponse{body: api.ShareInfo{BundleId: b.ID, Title: b.Title}, cookie: cookie}, nil
}

// signGuest makes the cookie value: the guest ID, the share token hash it was made for, and
// the expiry, with a MAC. A new or revoked share link therefore ends the guest.
func (a *API) signGuest(id uuid.UUID, shareHash string, exp time.Time) string {
	payload := fmt.Sprintf("%s.%s.%d", id, shareHash, exp.Unix())
	mac := base64.RawURLEncoding.EncodeToString(a.Sealer.MAC("guest", []byte(payload)))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + mac
}

// Guest returns the guest of a request's cookie, or nil when there is none or it no longer
// works. It checks the MAC, the expiry, and that the bundle's share link is the same and live.
func (a *API) Guest(ctx context.Context, r *http.Request) *kernel.Guest {
	if a.Sealer == nil {
		return nil
	}
	c, err := r.Cookie(GuestCookie)
	if err != nil {
		return nil
	}
	enc, mac, ok := strings.Cut(c.Value, ".")
	if !ok {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil
	}
	want := a.Sealer.MAC("guest", raw)
	got, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil || !hmac.Equal(want, got) {
		return nil
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 {
		return nil
	}
	var unix int64
	if _, err := fmt.Sscan(parts[2], &unix); err != nil || time.Now().Unix() > unix {
		return nil
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return nil
	}
	q := a.DB.Queries()
	g, err := q.GetShareGuest(ctx, id)
	if err != nil {
		return nil
	}
	b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: g.BundleID})
	if err != nil || b.Visibility != Link || b.ArchivedAt.Valid || b.ShareTokenHash.String != parts[1] ||
		(b.ShareExpiresAt.Valid && !b.ShareExpiresAt.Time.After(time.Now())) {
		return nil
	}
	return &kernel.Guest{ID: g.ID, BundleID: g.BundleID, Name: g.DisplayName}
}

func (a *API) bundle(ctx context.Context, id uuid.UUID) (pgdb.Bundle, error) {
	return a.DB.Queries().GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Workspace, ID: id})
}

func (a *API) access(ctx context.Context, b pgdb.Bundle) (api.BundleAccess, error) {
	q := a.DB.Queries()
	authors, err := q.ListBundleAuthors(ctx, b.ID)
	if err != nil {
		return api.BundleAccess{}, err
	}
	reviewers, err := q.ListBundleReviewers(ctx, b.ID)
	if err != nil {
		return api.BundleAccess{}, err
	}
	canEdit, err := CanEdit(ctx, q, kernel.ActorFrom(ctx), b)
	if err != nil {
		return api.BundleAccess{}, err
	}
	out := api.BundleAccess{Visibility: api.Visibility(b.Visibility), Authors: nonNil(authors), Reviewers: nonNil(reviewers),
		ShareActive: b.ShareTokenHash.Valid && (!b.ShareExpiresAt.Valid || b.ShareExpiresAt.Time.After(time.Now())), CanEdit: canEdit}
	if b.ShareExpiresAt.Valid {
		t := b.ShareExpiresAt.Time.UTC()
		out.ShareExpiresAt = &t
	}
	return out, nil
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// GetBundleAccess returns who can see the bundle.
func (a *API) GetBundleAccess(ctx context.Context, req api.GetBundleAccessRequestObject) (api.GetBundleAccessResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	out, err := a.access(ctx, b)
	if err != nil {
		return nil, err
	}
	return api.GetBundleAccess200JSONResponse(out), nil
}

// SetVisibility sets the visibility. Leaving link visibility revokes the share link.
func (a *API) SetVisibility(ctx context.Context, req api.SetVisibilityRequestObject) (api.SetVisibilityResponseObject, error) {
	v := string(req.Body.Visibility)
	if v != Private && v != Internal && v != Link {
		return nil, kernel.Invalid("bad_visibility", "Visibility is private, internal, or link.")
	}
	q := a.DB.Queries()
	now := time.Now().UTC()
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	if err := q.SetBundleVisibility(ctx, pgdb.SetBundleVisibilityParams{ID: b.ID, Visibility: v, UpdatedAt: now}); err != nil {
		return nil, err
	}
	if v != Link {
		if err := q.SetBundleShare(ctx, pgdb.SetBundleShareParams{ID: b.ID, UpdatedAt: now}); err != nil {
			return nil, err
		}
	}
	if b, err = a.bundle(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := a.access(ctx, b)
	if err != nil {
		return nil, err
	}
	return api.SetVisibility200JSONResponse(out), nil
}

// CreateShareLink makes a new share link, which replaces the old one, and sets link
// visibility (REQ-085). The token is stored hashed; the URL appears once.
func (a *API) CreateShareLink(ctx context.Context, req api.CreateShareLinkRequestObject) (api.CreateShareLinkResponseObject, error) {
	if a.Sealer == nil {
		return nil, kernel.Invalid("hosted_only", "Share links work in hosted mode. In local mode, share the file or export the bundle.")
	}
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	p := pgdb.SetBundleShareParams{ID: b.ID, UpdatedAt: now, ShareTokenHash: sql.NullString{String: HashToken(token), Valid: true}}
	if req.Body != nil && req.Body.ExpiresAt != nil {
		if !req.Body.ExpiresAt.After(now) {
			return nil, kernel.Invalid("bad_expiry", "The expiry must be in the future.")
		}
		p.ShareExpiresAt = sql.NullTime{Time: req.Body.ExpiresAt.UTC(), Valid: true}
	}
	err = a.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if err := q.SetBundleVisibility(ctx, pgdb.SetBundleVisibilityParams{ID: b.ID, Visibility: Link, UpdatedAt: now}); err != nil {
			return err
		}
		return q.SetBundleShare(ctx, p)
	})
	if err != nil {
		return nil, err
	}
	if b, err = a.bundle(ctx, b.ID); err != nil {
		return nil, err
	}
	acc, err := a.access(ctx, b)
	if err != nil {
		return nil, err
	}
	return api.CreateShareLink200JSONResponse{Url: strings.TrimSuffix(a.BaseURL, "/") + "/share/" + token, Access: acc}, nil
}

// RevokeShareLink ends the share link. Its guests lose access at once.
func (a *API) RevokeShareLink(ctx context.Context, req api.RevokeShareLinkRequestObject) (api.RevokeShareLinkResponseObject, error) {
	b, err := a.bundle(ctx, req.BundleId)
	if err != nil {
		return nil, err
	}
	if err := a.DB.Queries().SetBundleShare(ctx, pgdb.SetBundleShareParams{ID: b.ID, UpdatedAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	if b, err = a.bundle(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := a.access(ctx, b)
	if err != nil {
		return nil, err
	}
	return api.RevokeShareLink200JSONResponse(out), nil
}
