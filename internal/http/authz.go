package http

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	nethttp "net/http"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// access is who may call an operation (SDD §3, §14.2).
type access int

const (
	public     access = iota + 1 // anyone, signed in or not
	reader                       // a member, or a guest on any share link
	member                       // a signed-in member
	bundleRead                   // a member or guest who can see the path's bundle (REQ-084)
	bundleAI                     // like bundleRead, but never a guest: guests do not ask the AI (REQ-086)
	bundleEdit                   // an author of the path's bundle, or an admin
	adminOnly                    // an admin (SDD §3: the only role that sees model configuration)
)

// operations is the role table: one row for every operation in api/openapi.yaml. An
// operation with no row is refused. T-041 checks that the table is complete and enforced.
var operations = map[string]access{
	"getMeta": public,
	"getMe":   public,
	// Share links (REQ-085, REQ-086): the token is the credential.
	"getShare":  public,
	"joinShare": public,

	"renderMarkdown": reader,

	"listBundles":  member,
	"createBundle": member,
	"importBundle": member,
	"listProfiles": member,

	"getBundle":       bundleRead,
	"listFiles":       bundleRead,
	"getFileContent":  bundleRead,
	"listVersions":    bundleRead,
	"diffVersions":    bundleRead,
	"exportBundle":    bundleRead,
	"listRuns":        bundleRead,
	"listAssumptions": bundleRead,
	"getTrace":        bundleRead,
	"getRun":          bundleRead,
	"runEvents":       bundleRead,
	"listFindings":    bundleRead,
	"listClaims":      bundleRead,
	"listQuestions":   bundleRead,
	"getBundleAccess": bundleRead,

	"startRun":    bundleAI,
	"estimateRun": bundleAI,

	"putFileContent":  bundleEdit,
	"deleteFile":      bundleEdit,
	"renameFile":      bundleEdit,
	"addTraceIds":     bundleEdit,
	"setVisibility":   bundleEdit,
	"createShareLink": bundleEdit,
	"revokeShareLink": bundleEdit,

	"listBackends":        adminOnly,
	"createBackend":       adminOnly,
	"updateBackend":       adminOnly,
	"deleteBackend":       adminOnly,
	"testBackend":         adminOnly,
	"listPresets":         adminOnly,
	"listRoles":           adminOnly,
	"assignRole":          adminOnly,
	"unassignRole":        adminOnly,
	"getBudget":           adminOnly,
	"setBudget":           adminOnly,
	"listMCPConnections":  adminOnly,
	"createMCPConnection": adminOnly,
	"updateMCPConnection": adminOnly,
	"deleteMCPConnection": adminOnly,
	"listMCPTools":        adminOnly,
	"listInvites":         adminOnly,
	"createInvite":        adminOnly,
	"revokeInvite":        adminOnly,
	"createResetLink":     adminOnly,
	"getSettings":         adminOnly,
	"setSettings":         adminOnly,
}

// Authz enforces the role table for one workspace.
type Authz struct {
	DB        *store.DB
	Workspace uuid.UUID
}

var (
	errSignIn    = kernel.Unauthorized("sign_in_required", "Sign in to use Speccy.")
	errForbidden = kernel.Forbidden("forbidden", "Your role does not allow this.")
	errGuest     = kernel.Forbidden("guest_not_allowed", "A guest can read and comment. Sign in to do this.")
	errNoBundle  = kernel.NotFound("bundle_not_found", "No bundle has this ID, or you cannot see it.")
)

// Middleware is the strict-server middleware that checks each operation.
func (z *Authz) Middleware(f api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
	return func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, req any) (any, error) {
		if err := z.check(ctx, r, operationID); err != nil {
			return nil, err
		}
		return f(ctx, w, r, req)
	}
}

func (z *Authz) check(ctx context.Context, r *nethttp.Request, op string) error {
	// oapi-codegen passes the Go name (GetBundle); the table uses the operationId (getBundle).
	if op != "" {
		op = strings.ToLower(op[:1]) + op[1:]
	}
	need, ok := operations[op]
	if !ok {
		slog.ErrorContext(ctx, "an operation has no row in the role table", "operation", op)
		return errForbidden
	}
	a := kernel.ActorFrom(ctx)
	switch need {
	case public:
		return nil
	case reader:
		if a.Anonymous() {
			return errSignIn
		}
		return nil
	case member:
		return z.memberOnly(a)
	case adminOnly:
		if err := z.memberOnly(a); err != nil {
			return err
		}
		if !a.IsAdmin() {
			return errForbidden
		}
		return nil
	}

	if a.Anonymous() {
		return errSignIn
	}
	if need != bundleRead && a.Guest != nil {
		return errGuest
	}
	b, err := z.pathBundle(ctx, r)
	if errors.Is(err, sql.ErrNoRows) {
		return errNoBundle
	}
	if err != nil {
		return err
	}
	q := z.DB.Queries()
	canRead, err := share.CanRead(ctx, q, a, b)
	if err != nil {
		return err
	}
	if !canRead {
		return errNoBundle // do not show that a hidden bundle exists
	}
	if need == bundleEdit {
		canEdit, err := share.CanEdit(ctx, q, a, b)
		if err != nil {
			return err
		}
		if !canEdit {
			return kernel.Forbidden("not_author", "Only an author of this bundle or an admin can change it.")
		}
	}
	return nil
}

func (z *Authz) memberOnly(a kernel.Actor) error {
	if a.Guest != nil {
		return errGuest
	}
	if a.UserID == "" {
		return errSignIn
	}
	return nil
}

// pathBundle returns the bundle that the request's path names, directly or through a run.
func (z *Authz) pathBundle(ctx context.Context, r *nethttp.Request) (pgdb.Bundle, error) {
	q := z.DB.Queries()
	if s := r.PathValue("bundleId"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			return pgdb.Bundle{}, sql.ErrNoRows
		}
		return q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: z.Workspace, ID: id})
	}
	if s := r.PathValue("runId"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			return pgdb.Bundle{}, sql.ErrNoRows
		}
		run, err := q.GetRun(ctx, pgdb.GetRunParams{WorkspaceID: z.Workspace, ID: id})
		if err != nil {
			return pgdb.Bundle{}, err
		}
		return q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: z.Workspace, ID: run.BundleID})
	}
	return pgdb.Bundle{}, errors.New("the operation names no bundle in its path")
}
