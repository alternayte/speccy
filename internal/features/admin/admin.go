// Package admin configures model backends, role assignments, and the token budget
// (REQ-100 to REQ-104). DEC-013: only admins see models; local mode's one user is an admin.
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the admin endpoints.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Sealer    *kernel.Sealer
	Gateway   *model.Gateway
	// Accounts makes invite and reset links. It is nil in local mode, which has no accounts.
	Accounts Accounts
}

// Accounts is the part of the hosted auth setup that the admin screens use.
type Accounts interface {
	CreateInvite(ctx context.Context, role, createdBy string, ttl time.Duration) (string, pgdb.Invite, error)
	CreateResetLink(ctx context.Context, email, createdBy string) (string, error)
}

// backendConfig is the non-secret config stored in model_backend.config.
type backendConfig struct {
	BaseURL   string   `json:"base_url,omitempty"`
	Preset    string   `json:"preset,omitempty"`
	Command   []string `json:"command,omitempty"`
	PromptVia string   `json:"prompt_via,omitempty"`
}

func (a *API) toAPI(ctx context.Context, b pgdb.ModelBackend) (api.Backend, error) {
	var cfg backendConfig
	_ = json.Unmarshal(b.Config, &cfg)
	out := api.Backend{
		Id: b.ID, Kind: api.BackendKind(b.Kind), Name: b.Name,
		HasSecret: len(b.SecretEncrypted) > 0, SecretLast4: b.SecretLast4, Roles: []string{},
	}
	if cfg.BaseURL != "" {
		out.BaseUrl = &cfg.BaseURL
	}
	if cfg.Preset != "" {
		out.Preset = &cfg.Preset
	}
	if len(cfg.Command) > 0 {
		out.Command = &cfg.Command
	}
	if cfg.PromptVia != "" {
		out.PromptVia = &cfg.PromptVia
	}
	roles, err := a.DB.Queries().ListAssignments(ctx, a.Workspace)
	if err != nil {
		return out, err
	}
	for _, r := range roles {
		if r.BackendID == b.ID {
			out.Roles = append(out.Roles, r.Role)
		}
	}
	return out, nil
}

// ListBackends lists the backends.
func (a *API) ListBackends(ctx context.Context, _ api.ListBackendsRequestObject) (api.ListBackendsResponseObject, error) {
	rows, err := a.DB.Queries().ListBackends(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	out := api.BackendList{Items: []api.Backend{}}
	for _, r := range rows {
		if r.Kind == model.KindFake {
			continue
		}
		b, err := a.toAPI(ctx, r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, b)
	}
	return api.ListBackends200JSONResponse(out), nil
}

// validate checks a backend input and returns its stored config.
func validate(in api.BackendInput) (backendConfig, error) {
	var cfg backendConfig
	if strings.TrimSpace(in.Name) == "" {
		return cfg, kernel.Invalid("bad_backend", "Give the backend a name.")
	}
	if in.BaseUrl != nil && strings.TrimSpace(*in.BaseUrl) != "" {
		u := strings.TrimSpace(*in.BaseUrl)
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return cfg, kernel.Invalid("bad_backend", "The base URL must start with https:// or http://.")
		}
		cfg.BaseURL = u
	}
	switch in.Kind {
	case api.AgentCli:
		if in.Preset == nil || *in.Preset == "" {
			return cfg, kernel.Invalid("bad_backend", "Choose a preset for the agent CLI: %s, or custom.", strings.Join(model.PresetNames, ", "))
		}
		cfg.Preset = *in.Preset
		if cfg.Preset == "custom" {
			if in.Command == nil || len(*in.Command) == 0 || strings.TrimSpace((*in.Command)[0]) == "" {
				return cfg, kernel.Invalid("bad_backend", "A custom agent CLI needs a command. Write one argument per item.")
			}
			cfg.Command = *in.Command
			if in.PromptVia != nil {
				cfg.PromptVia = string(*in.PromptVia)
			}
		} else if _, ok := model.Presets[cfg.Preset]; !ok {
			return cfg, kernel.Invalid("bad_backend", "No agent CLI preset is named %q.", cfg.Preset)
		}
	case api.Openai, api.Anthropic, api.Openrouter, api.Deepseek:
	default:
		return cfg, kernel.Invalid("bad_backend", "Speccy has no backend of kind %q.", in.Kind)
	}
	return cfg, nil
}

// seal encrypts a secret (T-043): the store never holds it in plain text.
func (a *API) seal(secret string) ([]byte, string, error) {
	if secret == "" {
		return nil, "", nil
	}
	sealed, err := a.Sealer.Seal([]byte(secret))
	if err != nil {
		return nil, "", err
	}
	return sealed, kernel.Last4(secret), nil
}

// CreateBackend adds a backend.
func (a *API) CreateBackend(ctx context.Context, req api.CreateBackendRequestObject) (api.CreateBackendResponseObject, error) {
	in := *req.Body
	cfg, err := validate(in)
	if err != nil {
		return nil, err
	}
	needsKey := in.Kind != api.AgentCli
	secret := ""
	if in.Secret != nil {
		secret = strings.TrimSpace(*in.Secret)
	}
	if needsKey && secret == "" {
		return nil, kernel.Invalid("bad_backend", "A %s backend needs an API key.", in.Kind)
	}
	sealed, last4, err := a.seal(secret)
	if err != nil {
		return nil, err
	}
	cfgJSON, _ := json.Marshal(cfg)
	row := pgdb.InsertBackendParams{
		ID: kernel.NewID(), WorkspaceID: a.Workspace, Kind: string(in.Kind), Name: strings.TrimSpace(in.Name),
		Config: dbtype.JSON(cfgJSON), SecretEncrypted: sealed, SecretLast4: last4, CreatedAt: time.Now().UTC(),
	}
	if err := a.DB.Queries().InsertBackend(ctx, row); err != nil {
		if isUnique(err) {
			return nil, kernel.Conflict("name_taken", "A backend named %q exists. Choose another name.", row.Name)
		}
		return nil, err
	}
	b, err := a.DB.Queries().GetBackend(ctx, pgdb.GetBackendParams{WorkspaceID: a.Workspace, ID: row.ID})
	if err != nil {
		return nil, err
	}
	out, err := a.toAPI(ctx, b)
	if err != nil {
		return nil, err
	}
	return api.CreateBackend201JSONResponse(out), nil
}

func (a *API) backend(ctx context.Context, id uuid.UUID) (pgdb.ModelBackend, error) {
	b, err := a.DB.Queries().GetBackend(ctx, pgdb.GetBackendParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b.Kind == model.KindFake) {
		return b, kernel.NotFound("backend_not_found", "No backend has the ID %s.", id)
	}
	return b, err
}

// UpdateBackend changes a backend. A missing secret keeps the stored one.
func (a *API) UpdateBackend(ctx context.Context, req api.UpdateBackendRequestObject) (api.UpdateBackendResponseObject, error) {
	cur, err := a.backend(ctx, req.BackendId)
	if err != nil {
		return nil, err
	}
	in := *req.Body
	if string(in.Kind) != cur.Kind {
		return nil, kernel.Invalid("bad_backend", "The kind of a backend cannot change. Add a new backend instead.")
	}
	cfg, err := validate(in)
	if err != nil {
		return nil, err
	}
	sealed, last4 := cur.SecretEncrypted, cur.SecretLast4
	if in.Secret != nil && strings.TrimSpace(*in.Secret) != "" {
		if sealed, last4, err = a.seal(strings.TrimSpace(*in.Secret)); err != nil {
			return nil, err
		}
	}
	cfgJSON, _ := json.Marshal(cfg)
	err = a.DB.Queries().UpdateBackend(ctx, pgdb.UpdateBackendParams{
		WorkspaceID: a.Workspace, ID: cur.ID, Name: strings.TrimSpace(in.Name), Config: dbtype.JSON(cfgJSON),
		SecretEncrypted: sealed, SecretLast4: last4,
	})
	if err != nil {
		if isUnique(err) {
			return nil, kernel.Conflict("name_taken", "A backend named %q exists. Choose another name.", in.Name)
		}
		return nil, err
	}
	b, err := a.backend(ctx, cur.ID)
	if err != nil {
		return nil, err
	}
	out, err := a.toAPI(ctx, b)
	if err != nil {
		return nil, err
	}
	return api.UpdateBackend200JSONResponse(out), nil
}

// DeleteBackend deletes a backend that no role uses.
func (a *API) DeleteBackend(ctx context.Context, req api.DeleteBackendRequestObject) (api.DeleteBackendResponseObject, error) {
	b, err := a.backend(ctx, req.BackendId)
	if err != nil {
		return nil, err
	}
	n, err := a.DB.Queries().CountAssignmentsForBackend(ctx, pgdb.CountAssignmentsForBackendParams{WorkspaceID: a.Workspace, BackendID: b.ID})
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, kernel.Conflict("backend_in_use", "%s is assigned to %d role(s). Assign those roles to another backend first.", b.Name, n)
	}
	if _, err := a.DB.Queries().DeleteBackend(ctx, pgdb.DeleteBackendParams{WorkspaceID: a.Workspace, ID: b.ID}); err != nil {
		return nil, err
	}
	return api.DeleteBackend204Response{}, nil
}

var testSchema = []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)

// TestBackend sends one short call, so an admin sees at once whether a key and model work.
func (a *API) TestBackend(ctx context.Context, req api.TestBackendRequestObject) (api.TestBackendResponseObject, error) {
	b, err := a.backend(ctx, req.BackendId)
	if err != nil {
		return nil, err
	}
	modelName := strings.TrimSpace(req.Body.Model)
	if modelName == "" {
		return nil, kernel.Invalid("bad_model", "Give the model to test.")
	}
	start := time.Now()
	res, err := a.Gateway.CallWith(ctx, b, modelName, model.Call{
		Role: "test", PromptVersion: "backend-test-1", MaxTokens: 256, Schema: testSchema,
		Prompt: `This is a connection test. Reply with {"ok": true}.`,
	})
	out := api.TestBackend200JSONResponse{DurationMs: time.Since(start).Milliseconds()}
	if err != nil {
		msg := err.Error()
		if ke, ok := kernel.AsError(err); ok {
			msg = ke.Detail
		}
		out.Error = &msg
		return out, nil
	}
	answer := string(res.JSON)
	out.Ok, out.Answer, out.TokensIn, out.TokensOut, out.Estimated = true, &answer, &res.TokensIn, &res.TokensOut, &res.Estimated
	return out, nil
}

// ListPresets lists the agent CLI presets and whether each is installed.
func (a *API) ListPresets(context.Context, api.ListPresetsRequestObject) (api.ListPresetsResponseObject, error) {
	out := api.PresetList{Items: []api.Preset{}}
	for _, name := range model.PresetNames {
		p := model.Presets[name]
		out.Items = append(out.Items, api.Preset{Name: name, Installed: model.Installed(name), Command: p.Command, Verified: p.Verified})
	}
	return api.ListPresets200JSONResponse(out), nil
}

// ListRoles lists every role, assigned or not.
func (a *API) ListRoles(ctx context.Context, _ api.ListRolesRequestObject) (api.ListRolesResponseObject, error) {
	rows, err := a.DB.Queries().ListAssignments(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	byRole := map[string]pgdb.RoleAssignment{}
	for _, r := range rows {
		byRole[r.Role] = r
	}
	out := api.RoleList{Items: []api.Role{}}
	for _, role := range model.Roles {
		r := api.Role{Role: api.RoleName(role)}
		if a, ok := byRole[role]; ok {
			r.BackendId, r.Model = &a.BackendID, &a.Model
			pi, po := float32(a.PriceInPerMtok), float32(a.PriceOutPerMtok)
			r.PriceInPerMtok, r.PriceOutPerMtok = &pi, &po
		}
		out.Items = append(out.Items, r)
	}
	return api.ListRoles200JSONResponse(out), nil
}

// AssignRole assigns a backend and model to a role (REQ-101).
func (a *API) AssignRole(ctx context.Context, req api.AssignRoleRequestObject) (api.AssignRoleResponseObject, error) {
	in := *req.Body
	if _, err := a.backend(ctx, in.BackendId); err != nil {
		return nil, err
	}
	m := strings.TrimSpace(in.Model)
	if m == "" {
		return nil, kernel.Invalid("bad_model", "Give the model for the %s role.", req.Role)
	}
	var pin, pout float64
	if in.PriceInPerMtok != nil {
		pin = float64(*in.PriceInPerMtok)
	}
	if in.PriceOutPerMtok != nil {
		pout = float64(*in.PriceOutPerMtok)
	}
	if pin < 0 || pout < 0 {
		return nil, kernel.Invalid("bad_price", "A price cannot be negative.")
	}
	err := a.DB.Queries().UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{
		WorkspaceID: a.Workspace, Role: string(req.Role), BackendID: in.BackendId, Model: m,
		PriceInPerMtok: pin, PriceOutPerMtok: pout,
	})
	if err != nil {
		return nil, err
	}
	fin, fout := float32(pin), float32(pout)
	return api.AssignRole200JSONResponse{Role: req.Role, BackendId: &in.BackendId, Model: &m, PriceInPerMtok: &fin, PriceOutPerMtok: &fout}, nil
}

// UnassignRole removes a role's assignment.
func (a *API) UnassignRole(ctx context.Context, req api.UnassignRoleRequestObject) (api.UnassignRoleResponseObject, error) {
	if err := a.DB.Queries().DeleteAssignment(ctx, pgdb.DeleteAssignmentParams{WorkspaceID: a.Workspace, Role: string(req.Role)}); err != nil {
		return nil, err
	}
	return api.UnassignRole204Response{}, nil
}

// GetBudget returns this month's budget.
func (a *API) GetBudget(ctx context.Context, _ api.GetBudgetRequestObject) (api.GetBudgetResponseObject, error) {
	b, err := a.Gateway.Budget(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetBudget200JSONResponse(budgetAPI(b)), nil
}

// SetBudget sets the monthly token limit (REQ-104).
func (a *API) SetBudget(ctx context.Context, req api.SetBudgetRequestObject) (api.SetBudgetResponseObject, error) {
	b, err := a.Gateway.Budget(ctx)
	if err != nil {
		return nil, err
	}
	var limit sql.NullInt64
	if req.Body.TokenLimit != nil {
		limit = sql.NullInt64{Int64: *req.Body.TokenLimit, Valid: true}
	}
	if err := a.DB.Queries().SetBudgetLimit(ctx, pgdb.SetBudgetLimitParams{WorkspaceID: a.Workspace, Month: b.Month, TokenLimit: limit}); err != nil {
		return nil, err
	}
	b.TokenLimit = limit
	return api.SetBudget200JSONResponse(budgetAPI(b)), nil
}

func budgetAPI(b pgdb.Budget) api.Budget {
	out := api.Budget{Month: b.Month, TokensUsed: b.TokensUsed}
	if b.TokenLimit.Valid {
		out.TokenLimit = &b.TokenLimit.Int64
	}
	return out
}

func isUnique(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate key")
}
