package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// Settings are the workspace settings an admin sets (REQ-009, REQ-081, REQ-105). They live in
// workspace.settings.
type Settings struct {
	MaxFileMB     int `json:"max_file_mb"`
	MaxBundleMB   int `json:"max_bundle_mb"`
	InviteTTLDays int `json:"invite_ttl_days"`
	ParallelCalls int `json:"parallel_calls"`
}

// DefaultSettings are the SDD defaults.
var DefaultSettings = Settings{MaxFileMB: 10, MaxBundleMB: 50, InviteTTLDays: 7, ParallelCalls: 4}

// LoadSettings reads the settings; a missing value takes its default.
func LoadSettings(ctx context.Context, q store.Querier, workspace uuid.UUID) (Settings, error) {
	ws, err := q.GetWorkspace(ctx, workspace)
	if err != nil {
		return DefaultSettings, err
	}
	s := DefaultSettings
	_ = json.Unmarshal(ws.Settings, &s)
	return s.withDefaults(), nil
}

func (s Settings) withDefaults() Settings {
	d := DefaultSettings
	if s.MaxFileMB <= 0 {
		s.MaxFileMB = d.MaxFileMB
	}
	if s.MaxBundleMB <= 0 {
		s.MaxBundleMB = d.MaxBundleMB
	}
	if s.InviteTTLDays <= 0 {
		s.InviteTTLDays = d.InviteTTLDays
	}
	if s.ParallelCalls <= 0 {
		s.ParallelCalls = d.ParallelCalls
	}
	return s
}

// Limits returns the REQ-009 limits of the settings.
func (s Settings) Limits() source.Limits {
	return source.Limits{FileBytes: int64(s.MaxFileMB) << 20, BundleBytes: int64(s.MaxBundleMB) << 20}
}

// InviteTTL is REQ-081's configurable expiry.
func (s Settings) InviteTTL() time.Duration { return time.Duration(s.InviteTTLDays) * 24 * time.Hour }

func settingsAPI(s Settings) api.Settings {
	return api.Settings{MaxFileMb: s.MaxFileMB, MaxBundleMb: s.MaxBundleMB, InviteTtlDays: s.InviteTTLDays, ParallelCalls: s.ParallelCalls}
}

// GetSettings returns the workspace settings.
func (a *API) GetSettings(ctx context.Context, _ api.GetSettingsRequestObject) (api.GetSettingsResponseObject, error) {
	s, err := LoadSettings(ctx, a.DB.Queries(), a.Workspace)
	if err != nil {
		return nil, err
	}
	return api.GetSettings200JSONResponse(settingsAPI(s)), nil
}

// SetSettings changes the workspace settings.
func (a *API) SetSettings(ctx context.Context, req api.SetSettingsRequestObject) (api.SetSettingsResponseObject, error) {
	b := req.Body
	s := Settings{MaxFileMB: b.MaxFileMb, MaxBundleMB: b.MaxBundleMb, InviteTTLDays: b.InviteTtlDays, ParallelCalls: b.ParallelCalls}
	switch {
	case s.MaxFileMB < 1 || int64(s.MaxFileMB)<<20 > source.CeilingFileBytes:
		return nil, kernel.Invalid("bad_setting", "The file limit must be 1 to %d MB.", source.CeilingFileBytes>>20)
	case s.MaxBundleMB < s.MaxFileMB || int64(s.MaxBundleMB)<<20 > source.CeilingBundleBytes:
		return nil, kernel.Invalid("bad_setting", "The bundle limit must be at least the file limit, and at most %d MB.", source.CeilingBundleBytes>>20)
	case s.InviteTTLDays < 1 || s.InviteTTLDays > 90:
		return nil, kernel.Invalid("bad_setting", "Invite links must expire after 1 to 90 days.")
	case s.ParallelCalls < 1 || s.ParallelCalls > 16:
		return nil, kernel.Invalid("bad_setting", "Parallel model calls must be 1 to 16.")
	}
	raw, _ := json.Marshal(s)
	if err := a.DB.Queries().SetWorkspaceSettings(ctx, pgdb.SetWorkspaceSettingsParams{ID: a.Workspace, Settings: dbtype.JSON(raw)}); err != nil {
		return nil, err
	}
	return api.SetSettings200JSONResponse(settingsAPI(s)), nil
}

// ListInvites lists invite links, newest first. A list never carries a token.
func (a *API) ListInvites(ctx context.Context, _ api.ListInvitesRequestObject) (api.ListInvitesResponseObject, error) {
	rows, err := a.DB.Queries().ListInvites(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	out := api.ListInvites200JSONResponse{Items: []api.Invite{}}
	now := time.Now()
	for _, r := range rows {
		out.Items = append(out.Items, inviteAPI(r, now))
	}
	return out, nil
}

func inviteAPI(r pgdb.Invite, now time.Time) api.Invite {
	status := api.Pending
	switch {
	case r.UsedAt.Valid:
		status = api.Used
	case r.RevokedAt.Valid:
		status = api.Revoked
	case !r.ExpiresAt.After(now):
		status = api.Expired
	}
	inv := api.Invite{Id: r.ID, Role: api.InviteRole(r.Role), ExpiresAt: r.ExpiresAt.UTC(), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), Status: status}
	if r.UsedBy.Valid {
		inv.UsedBy = &r.UsedBy.String
	}
	return inv
}

var errLocal = kernel.Invalid("hosted_only", "Local mode has no accounts. Invite and reset links work in hosted mode.")

// CreateInvite makes a single-use invite link (REQ-081).
func (a *API) CreateInvite(ctx context.Context, req api.CreateInviteRequestObject) (api.CreateInviteResponseObject, error) {
	if a.Accounts == nil {
		return nil, errLocal
	}
	s, err := LoadSettings(ctx, a.DB.Queries(), a.Workspace)
	if err != nil {
		return nil, err
	}
	url, inv, err := a.Accounts.CreateInvite(ctx, string(req.Body.Role), kernel.ActorFrom(ctx).UserID, s.InviteTTL())
	if err != nil {
		return nil, err
	}
	return api.CreateInvite200JSONResponse{Url: url, Invite: inviteAPI(inv, time.Now())}, nil
}

// RevokeInvite ends an unused invite.
func (a *API) RevokeInvite(ctx context.Context, req api.RevokeInviteRequestObject) (api.RevokeInviteResponseObject, error) {
	n, err := a.DB.Queries().RevokeInvite(ctx, pgdb.RevokeInviteParams{WorkspaceID: a.Workspace, ID: req.InviteId,
		RevokedAt: sqlNow()})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, kernel.NotFound("invite_not_pending", "No pending invite has this ID.")
	}
	return api.RevokeInvite204Response{}, nil
}

// CreateResetLink makes a one-time password reset link (REQ-082).
func (a *API) CreateResetLink(ctx context.Context, req api.CreateResetLinkRequestObject) (api.CreateResetLinkResponseObject, error) {
	if a.Accounts == nil {
		return nil, errLocal
	}
	url, err := a.Accounts.CreateResetLink(ctx, req.Body.Email, kernel.ActorFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return api.CreateResetLink200JSONResponse{Url: url}, nil
}

func sqlNow() sql.NullTime { return sql.NullTime{Time: time.Now().UTC(), Valid: true} }
