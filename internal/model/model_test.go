package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store/storetest"
)

var answerSchema = []byte(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)

func call() Call {
	return Call{Role: RoleReviewer, PromptVersion: "test-1", System: "You answer questions.", Prompt: "Capital of France?", Schema: answerSchema}
}

// newGateway returns a gateway on a fresh database with the fake backend assigned to reviewer.
func newGateway(t *testing.T, script []string) (*Gateway, *Fake) {
	t.Helper()
	ctx := context.Background()
	db := storetest.Engines()[0].Open(t)
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fake := NewFake(map[string][]string{Fingerprint(call()): script})
	id := kernel.NewID()
	q := db.Queries()
	if err := q.InsertBackend(ctx, pgdb.InsertBackendParams{ID: id, WorkspaceID: ws, Kind: KindFake, Name: "fake",
		Config: dbtype.JSON(`{}`), SecretLast4: "", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: ws, Role: RoleReviewer, BackendID: id, Model: "fake-1"}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{DB: db, Workspace: ws, Fake: fake, backoff: func(int) time.Duration { return 0 }}
	return g, fake
}

// T-083
func TestModel_InvalidJSONRetryOnce(t *testing.T) {
	ctx := context.Background()
	g, fake := newGateway(t, []string{"!invalid", `{"answer":"Paris"}`})
	res, err := g.Call(ctx, call())
	if err != nil {
		t.Fatalf("one invalid answer, then a valid one: %v", err)
	}
	if string(res.JSON) != `{"answer":"Paris"}` || res.Attempts != 2 || len(fake.Calls()) != 2 {
		t.Errorf("result %s after %d attempts, %d calls; want the valid answer after 2", res.JSON, res.Attempts, len(fake.Calls()))
	}

	g, fake = newGateway(t, []string{"!invalid"})
	if _, err := g.Call(ctx, call()); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("always invalid: err = %v, want a failure after the retry", err)
	}
	if n := len(fake.Calls()); n != 2 {
		t.Errorf("always invalid: %d calls, want 2 (one retry)", n)
	}

	// An answer that is JSON but breaks the schema also counts as invalid.
	g, fake = newGateway(t, []string{`{"wrong":1}`})
	if _, err := g.Call(ctx, call()); err == nil || len(fake.Calls()) != 2 {
		t.Errorf("schema mismatch: err = %v after %d calls, want a failure after 2", err, len(fake.Calls()))
	}
}

// REQ-103: HTTP 429 and 5xx get 2 retries.
func TestGateway_RetriesTransientErrors(t *testing.T) {
	ctx := context.Background()
	g, fake := newGateway(t, []string{"!429", "!500", `{"answer":"Paris"}`})
	if _, err := g.Call(ctx, call()); err != nil || len(fake.Calls()) != 3 {
		t.Fatalf("429, 500, then OK: err = %v after %d calls", err, len(fake.Calls()))
	}
	g, fake = newGateway(t, []string{"!429"})
	if _, err := g.Call(ctx, call()); err == nil || len(fake.Calls()) != 3 {
		t.Errorf("always 429: err = %v after %d calls, want a failure after 3", err, len(fake.Calls()))
	}
}

// REQ-103: a call that does not answer in time fails and is not retried.
func TestGateway_Timeout(t *testing.T) {
	g, fake := newGateway(t, []string{"!timeout"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.Call(ctx, call()); err == nil || len(fake.Calls()) != 1 {
		t.Errorf("err = %v after %d calls, want a failure after 1", err, len(fake.Calls()))
	}
}

// REQ-104: when the budget is spent, calls refuse to start.
func TestGateway_Budget(t *testing.T) {
	ctx := context.Background()
	g, fake := newGateway(t, []string{`{"answer":"Paris"}`})
	b, err := g.Budget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.DB.Queries().SetBudgetLimit(ctx, pgdb.SetBudgetLimitParams{WorkspaceID: g.Workspace, Month: b.Month, TokenLimit: sql.NullInt64{Int64: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Call(ctx, call()); err != nil {
		t.Fatalf("first call under the limit: %v", err)
	}
	if _, err := g.Call(ctx, call()); !errors.Is(err, ErrBudgetSpent) {
		t.Fatalf("call after the budget is spent: err = %v, want ErrBudgetSpent", err)
	}
	if n := len(fake.Calls()); n != 1 {
		t.Errorf("the refused call reached the backend: %d calls", n)
	}
	b, _ = g.Budget(ctx)
	if b.TokensUsed == 0 {
		t.Error("tokens were not counted")
	}
}

func TestFake_MissingScriptNamesFingerprint(t *testing.T) {
	f := NewFake(nil)
	_, err := f.Call(context.Background(), "m", call())
	if err == nil || !strings.Contains(err.Error(), Fingerprint(call())) {
		t.Errorf("err = %v, want it to name the fingerprint", err)
	}
}

// The parsers read real output from the CLI versions in Presets (§19 Q4).
func TestAgentCLIParsers(t *testing.T) {
	for _, c := range []struct {
		preset, file string
		in, out      int64
	}{
		{"claude", "testdata/claude.json", 9064, 1171},
		{"cursor-agent", "testdata/cursor.json", 0, 0},
		{"opencode", "testdata/opencode.jsonl", -1, -1},
		{"pi", "testdata/pi.jsonl", 830, 7},
	} {
		t.Run(c.preset, func(t *testing.T) {
			out, err := os.ReadFile(c.file)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := Presets[c.preset].parse(out)
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Answer string }
			if err := json.Unmarshal([]byte(extractJSON(raw.Text)), &got); err != nil || got.Answer != "Paris" {
				t.Errorf("answer %q (%v)", raw.Text, err)
			}
			if c.in >= 0 && (raw.TokensIn != c.in || raw.TokensOut != c.out) {
				t.Errorf("tokens %d/%d, want %d/%d", raw.TokensIn, raw.TokensOut, c.in, c.out)
			}
			if c.in < 0 && raw.TokensIn == 0 {
				t.Error("opencode tokens were not read")
			}
		})
	}
	if _, err := parseOpencode([]byte(`{"type":"error","error":{"data":{"message":"Insufficient account funds"}}}`)); err == nil ||
		!strings.Contains(err.Error(), "Insufficient") {
		t.Errorf("opencode error event: %v", err)
	}
	if _, err := parseClaude([]byte(`{"is_error":true,"result":"Not logged in"}`)); err == nil {
		t.Error("claude is_error was not an error")
	}
}

// The agent CLI runs a custom command in a folder with the bundle and prompt.md.
func TestAgentCLI_Custom(t *testing.T) {
	be, err := newAgentCLI(AgentCLIConfig{Preset: "custom", Command: []string{"sh", "-c", `test -f assets/api.yaml && grep -q "Capital" prompt.md && echo '{"answer":"Paris"}'`}, PromptVia: "file"})
	if err != nil {
		t.Fatal(err)
	}
	c := call()
	c.Files = []File{{Path: "assets/api.yaml", Content: []byte("x")}}
	raw, err := be.Call(context.Background(), "m", c)
	if err != nil || strings.TrimSpace(raw.Text) != `{"answer":"Paris"}` || !raw.Estimated {
		t.Errorf("raw %+v, err %v", raw, err)
	}
	c.Files = []File{{Path: "../escape", Content: []byte("x")}}
	if _, err := be.Call(context.Background(), "m", c); err == nil {
		t.Error("a file path that leaves the working folder was written")
	}
}

// Each agent CLI preset must run without an interactive prompt: Speccy gives every call a
// fresh temp folder, captures stdout and stderr, and has no way to answer a question. The
// cursor-agent preset asked for workspace trust on every call, and the run blocked.
func TestPresetsNeverPrompt(t *testing.T) {
	need := map[string][]string{
		// "Trust the current workspace without prompting (only works with --print/headless
		// mode)". The preset already passes -p.
		"cursor-agent": {"--trust"},
		// The others were run live in a fresh folder and asked nothing: claude answered,
		// opencode and pi reached their model call.
		"claude":   {"--no-session-persistence"},
		"opencode": {"--pure"},
		"pi":       {"--no-session"},
	}
	for name, flags := range need {
		p, ok := Presets[name]
		if !ok {
			t.Fatalf("no preset %q", name)
		}
		for _, f := range flags {
			if !slices.Contains(p.Command, f) {
				t.Errorf("the %s preset does not pass %s, so a call can block on a prompt nobody can answer", name, f)
			}
		}
	}
}
