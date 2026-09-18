package model

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// REQ-103 limits.
const (
	CallTimeout      = 120 * time.Second
	transientRetries = 2
	parseRetries     = 1
)

// ErrBudgetSpent means the workspace spent its monthly token budget (REQ-104).
var ErrBudgetSpent = kernel.Conflict("budget_spent",
	"Review stopped: the monthly token budget is spent. An admin can raise it in Admin → Models.")

// Result is a checked answer.
type Result struct {
	JSON      json.RawMessage
	TokensIn  int64
	TokensOut int64
	Estimated bool
	Backend   string
	Model     string
	// Fingerprint names the backend and model, for reader diversity (REQ-046) and the cache.
	Fingerprint string
	Attempts    int
}

// Gateway sends calls for roles to the assigned backend.
type Gateway struct {
	DB        *store.DB
	Workspace uuid.UUID
	Sealer    *kernel.Sealer
	// Fake is the backend that a "fake" backend row uses: a scripted Fake or a BackendFunc.
	// Tests set it; it is never reachable through the API.
	Fake Backend

	// now and backoff are fixed in tests.
	now     func() time.Time
	backoff func(attempt int) time.Duration
	// build returns the backend for a row; tests can replace it.
	build func(pgdb.ModelBackend) (Backend, error)
}

func (g *Gateway) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now().UTC()
}

func (g *Gateway) wait(ctx context.Context, attempt int) error {
	d := time.Duration(0)
	if g.backoff != nil {
		d = g.backoff(attempt)
	} else {
		// 1 s, then 2 s, with up to 25% jitter.
		base := time.Second << attempt
		d = base + time.Duration(rand.Int64N(int64(base/4)+1))
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Assignment is a role's backend and model.
type Assignment struct {
	Backend pgdb.ModelBackend
	Model   string
}

// Assigned returns the backend and model assigned to role.
func (g *Gateway) Assigned(ctx context.Context, role string) (Assignment, error) {
	q := g.DB.Queries()
	a, err := q.GetAssignment(ctx, pgdb.GetAssignmentParams{WorkspaceID: g.Workspace, Role: role})
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, kernel.Invalid("role_unassigned",
			"No model is assigned to the %s role. An admin assigns one in Admin → Models.", role)
	}
	if err != nil {
		return Assignment{}, err
	}
	b, err := q.GetBackend(ctx, pgdb.GetBackendParams{WorkspaceID: g.Workspace, ID: a.BackendID})
	if err != nil {
		return Assignment{}, err
	}
	return Assignment{Backend: b, Model: a.Model}, nil
}

// Call sends c to the backend assigned to c.Role.
func (g *Gateway) Call(ctx context.Context, c Call) (Result, error) {
	a, err := g.Assigned(ctx, c.Role)
	if err != nil {
		return Result{}, err
	}
	return g.CallWith(ctx, a.Backend, a.Model, c)
}

// CallWith sends c to a given backend row and model: the role path above, and the admin's
// "Test" button.
func (g *Gateway) CallWith(ctx context.Context, row pgdb.ModelBackend, model string, c Call) (Result, error) {
	schema, err := compileSchema(c.Schema)
	if err != nil {
		return Result{}, err
	}
	build := g.build
	if build == nil {
		build = g.backendFor
	}
	be, err := build(row)
	if err != nil {
		return Result{}, err
	}
	res := Result{Backend: row.Kind, Model: model, Fingerprint: row.Kind + ":" + model}
	transientLeft, parseLeft := transientRetries, parseRetries
	var lastParse error
	for attempt := 0; ; attempt++ {
		if err := g.checkBudget(ctx); err != nil {
			return res, err
		}
		callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
		raw, err := be.Call(callCtx, model, c)
		timedOut := callCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		res.Attempts = attempt + 1
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			if timedOut {
				return res, fmt.Errorf("the %s backend did not answer within %s", row.Name, CallTimeout)
			}
			if Transient(err) && transientLeft > 0 {
				transientLeft--
				if err := g.wait(ctx, transientRetries-transientLeft-1); err != nil {
					return res, err
				}
				continue
			}
			return res, fmt.Errorf("the %s backend failed: %w", row.Name, err)
		}
		res.TokensIn += raw.TokensIn
		res.TokensOut += raw.TokensOut
		res.Estimated = res.Estimated || raw.Estimated
		if err := g.spend(ctx, raw.TokensIn+raw.TokensOut); err != nil {
			return res, err
		}
		// DEC-014: the answer must be JSON that matches the schema. One retry on a parse error.
		answer, perr := checkAnswer(raw.Text, schema)
		if perr == nil {
			res.JSON = answer
			return res, nil
		}
		lastParse = perr
		if parseLeft == 0 {
			return res, fmt.Errorf("the %s backend returned an answer that is not valid JSON for the step, twice: %w", row.Name, lastParse)
		}
		parseLeft--
	}
}

func compileSchema(raw []byte) (*jsonschema.Schema, error) {
	if len(raw) == 0 {
		return nil, errors.New("the call has no JSON schema")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("the call's JSON schema does not parse: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("answer.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("answer.json")
}

func checkAnswer(text string, schema *jsonschema.Schema) (json.RawMessage, error) {
	js := extractJSON(text)
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(js)))
	if err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	if err := schema.Validate(v); err != nil {
		return nil, fmt.Errorf("does not match the schema: %w", err)
	}
	return json.RawMessage(js), nil
}

func (g *Gateway) month() string { return g.clock().Format("2006-01") }

// Budget returns this month's row, and creates it with last month's limit.
func (g *Gateway) Budget(ctx context.Context) (pgdb.Budget, error) {
	q := g.DB.Queries()
	month := g.month()
	b, err := q.GetBudget(ctx, pgdb.GetBudgetParams{WorkspaceID: g.Workspace, Month: month})
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	var limit sql.NullInt64
	if last, err := q.LatestBudget(ctx, g.Workspace); err == nil {
		limit = last.TokenLimit
	} else if !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if err := q.InsertBudget(ctx, pgdb.InsertBudgetParams{WorkspaceID: g.Workspace, Month: month, TokenLimit: limit}); err != nil {
		return b, err
	}
	return q.GetBudget(ctx, pgdb.GetBudgetParams{WorkspaceID: g.Workspace, Month: month})
}

func (g *Gateway) checkBudget(ctx context.Context) error {
	b, err := g.Budget(ctx)
	if err != nil {
		return err
	}
	if b.TokenLimit.Valid && b.TokensUsed >= b.TokenLimit.Int64 {
		return ErrBudgetSpent
	}
	return nil
}

func (g *Gateway) spend(ctx context.Context, tokens int64) error {
	if _, err := g.Budget(ctx); err != nil {
		return err
	}
	return g.DB.Queries().AddBudgetTokens(ctx, pgdb.AddBudgetTokensParams{WorkspaceID: g.Workspace, Month: g.month(), Tokens: tokens})
}

// backendFor builds the backend for a stored row, with its secret decrypted.
func (g *Gateway) backendFor(row pgdb.ModelBackend) (Backend, error) {
	var secret string
	if len(row.SecretEncrypted) > 0 {
		if g.Sealer == nil {
			return nil, errors.New("no secret key is loaded")
		}
		plain, err := g.Sealer.Open(row.SecretEncrypted)
		if err != nil {
			return nil, err
		}
		secret = string(plain)
	}
	switch row.Kind {
	case KindAnthropic:
		var cfg struct {
			BaseURL string `json:"base_url"`
		}
		_ = json.Unmarshal(row.Config, &cfg)
		return newAnthropic(secret, cfg.BaseURL), nil
	case KindOpenAI, KindOpenRouter, KindDeepSeek:
		var cfg struct {
			BaseURL string `json:"base_url"`
		}
		_ = json.Unmarshal(row.Config, &cfg)
		return newOpenAICompatible(row.Kind, secret, cfg.BaseURL), nil
	case KindAgentCLI:
		var cfg AgentCLIConfig
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("the agent CLI config does not parse: %w", err)
		}
		return newAgentCLI(cfg)
	case KindFake:
		if g.Fake == nil {
			return nil, errors.New("the fake backend is for tests only")
		}
		return g.Fake, nil
	}
	return nil, fmt.Errorf("unknown backend kind %q", row.Kind)
}
