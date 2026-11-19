package admin

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// setupInviteTTL is how long the first admin's invite link works.
const setupInviteTTL = time.Hour

// setupOpen says whether the first-run setup takes the token: SPECCY_SETUP_TOKEN is set, and
// no admin exists yet (docs/specs/github-app.md).
func (a *API) setupOpen(ctx context.Context) (bool, error) {
	if a.SetupToken == "" || a.Accounts == nil {
		return false, nil
	}
	people, err := a.Accounts.People(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range people {
		if p.Role == kernel.RoleAdmin {
			return false, nil
		}
	}
	return true, nil
}

// GetSetup says whether /setup is open.
func (a *API) GetSetup(ctx context.Context, _ api.GetSetupRequestObject) (api.GetSetupResponseObject, error) {
	open, err := a.setupOpen(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetSetup200JSONResponse{Open: open}, nil
}

// StartSetup trades the setup token for a single-use admin invite. The invite page makes the
// account with email and password, then sends the person on to the GitHub step.
func (a *API) StartSetup(ctx context.Context, req api.StartSetupRequestObject) (api.StartSetupResponseObject, error) {
	open, err := a.setupOpen(ctx)
	if err != nil {
		return nil, err
	}
	if !open {
		return nil, kernel.NotFound("setup_closed", "Setup is closed: an admin exists. Ask an admin for an invite link.")
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.Body.Token)), []byte(a.SetupToken)) != 1 {
		return nil, kernel.Forbidden("setup_token_wrong", "The setup token is wrong. It is the value of SPECCY_SETUP_TOKEN.")
	}
	link, _, err := a.Accounts.CreateInvite(ctx, kernel.RoleAdmin, "setup", setupInviteTTL)
	if err != nil {
		return nil, err
	}
	return api.StartSetup200JSONResponse{InviteUrl: link + "&next=setup"}, nil
}
