package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
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
		{"cursor-agent", "testdata/cursor.json", 27391, 79},
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

// A CLI that asks its question on /dev/tty must fail at once, not wait. A review run has no
// terminal at either end, so Speccy gives the CLI its own session and no controlling terminal.
// This asserts the flag, not the behaviour: `go test` itself runs with no controlling
// terminal, so an end-to-end test of it passes whether the call is there or not.
func TestAgentCLI_LeavesTheControllingTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no controlling terminal to leave")
	}
	cmd := exec.Command("true")
	detach(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("the CLI keeps the controlling terminal, so a question on /dev/tty would wait for an answer nobody can give")
	}
}

// A call goes out at temperature 0. A model that refuses a temperature gets the call again
// with none, and every later call to it goes out with none at once.
func TestGateway_TemperatureZeroWhereTheModelTakesIt(t *testing.T) {
	ctx := context.Background()
	g, _ := newGateway(t, nil)
	var sent []*float64
	refuse := false
	g.Fake = BackendFunc(func(_ context.Context, _ string, c Call) (Raw, error) {
		sent = append(sent, c.Temperature)
		if refuse && c.Temperature != nil {
			return Raw{}, &StatusError{Status: 400, Message: "`temperature` is not supported for this model."}
		}
		return Raw{Text: `{"answer":"Paris"}`}, nil
	})
	res, err := g.Call(ctx, call())
	if err != nil || res.Temperature == nil || *res.Temperature != 0 || len(sent) != 1 {
		t.Fatalf("a model that takes a temperature: err %v, temperature %v, %d calls; want 0 in one call", err, res.Temperature, len(sent))
	}

	g, _ = newGateway(t, nil)
	g.Fake, sent, refuse = BackendFunc(func(_ context.Context, _ string, c Call) (Raw, error) {
		sent = append(sent, c.Temperature)
		if c.Temperature != nil {
			return Raw{}, &StatusError{Status: 400, Message: "`temperature` is not supported for this model."}
		}
		return Raw{Text: `{"answer":"Paris"}`}, nil
	}), nil, true
	res, err = g.Call(ctx, call())
	if err != nil || res.Temperature != nil || len(sent) != 2 {
		t.Fatalf("a model that refuses a temperature: err %v, temperature %v, %d calls; want an answer with none after 2", err, res.Temperature, len(sent))
	}
	if _, err := g.Call(ctx, call()); err != nil || len(sent) != 3 || sent[2] != nil {
		t.Errorf("the next call: err %v, %d calls in all; want one more call with no temperature", err, len(sent))
	}
}

// An answer cut off at the token limit gets one more call with a higher limit. A second cut-off
// is an error that says so, not a JSON error.
func TestGateway_CutOffAnswer(t *testing.T) {
	ctx := context.Background()
	g, _ := newGateway(t, nil)
	var limits []int64
	g.Fake = BackendFunc(func(_ context.Context, _ string, c Call) (Raw, error) {
		limits = append(limits, c.MaxTokens)
		if len(limits) == 1 {
			return Raw{Text: `{"answer":"Par`, Truncated: true}, nil
		}
		return Raw{Text: `{"answer":"Paris"}`}, nil
	})
	c := call()
	c.MaxTokens = 4000
	if _, err := g.Call(ctx, c); err != nil || !slices.Equal(limits, []int64{4000, 16000}) {
		t.Fatalf("err %v, limits %v; want an answer after a second call at 16000", err, limits)
	}

	g, _ = newGateway(t, nil)
	g.Fake = BackendFunc(func(context.Context, string, Call) (Raw, error) {
		return Raw{Text: `{"answer":"Par`, Truncated: true}, nil
	})
	_, err := g.Call(ctx, c)
	if ke, ok := kernel.AsError(err); !ok || ke.Code != "answer_cut_off" || strings.Contains(ke.Detail, "JSON") {
		t.Fatalf("a second cut-off: %v; want answer_cut_off", err)
	}
}

// The model list comes from the backend's models endpoint with the stored key. OpenRouter
// gives the price of one token; the list has dollars per million tokens. The other API
// backends give no price.
func TestModels_OpenRouterPrices(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"openai/gpt-5","name":"OpenAI: GPT-5","pricing":{"prompt":"0.00000125","completion":"0.00001"}},
			{"id":"anthropic/claude-sonnet-4.5","name":"Anthropic: Claude Sonnet 4.5","pricing":{"prompt":"0.000003","completion":"0.000015"}},
			{"id":"openrouter/auto","name":"Auto Router","pricing":{"prompt":"-1","completion":"-1"}}]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	g := &Gateway{}
	for _, kind := range []string{KindOpenRouter, KindOpenAI} {
		g.build = func(pgdb.ModelBackend) (Backend, error) { return newOpenAICompatible(kind, "sk-test", srv.URL), nil }
		got, err := g.Models(ctx, pgdb.ModelBackend{Name: kind, Kind: kind})
		if err != nil || len(got) != 3 || auth != "Bearer sk-test" {
			t.Fatalf("%s: %d models, err %v, Authorization %q", kind, len(got), err, auth)
		}
		// The list is in the order of the IDs.
		sonnet, gpt, auto := got[0], got[1], got[2]
		if sonnet.ID != "anthropic/claude-sonnet-4.5" || gpt.ID != "openai/gpt-5" || auto.PriceIn != nil {
			t.Fatalf("%s: models %+v", kind, got)
		}
		if kind == KindOpenAI {
			if sonnet.PriceIn != nil || gpt.PriceOut != nil {
				t.Errorf("an OpenAI backend gave prices: %+v", got)
			}
			continue
		}
		if *sonnet.PriceIn != 3 || *sonnet.PriceOut != 15 || *gpt.PriceIn != 1.25 || *gpt.PriceOut != 10 {
			t.Errorf("prices per million tokens: sonnet %v/%v, gpt %v/%v", *sonnet.PriceIn, *sonnet.PriceOut, *gpt.PriceIn, *gpt.PriceOut)
		}
	}
	// An agent CLI has no models endpoint.
	g.build = func(pgdb.ModelBackend) (Backend, error) { return BackendFunc(nil), nil }
	if _, err := g.Models(ctx, pgdb.ModelBackend{Name: "claude", Kind: KindAgentCLI}); err == nil {
		t.Error("an agent CLI gave a model list")
	} else if ke, ok := kernel.AsError(err); !ok || ke.Code != "no_model_list" {
		t.Errorf("an agent CLI: %v", err)
	}
}

// #121: each backend with a web search reports the pages that the search returned, so the
// review can drop a source that the model names and did not read.
func TestBackends_ReportThePagesOfTheSearch(t *testing.T) {
	serve := func(body string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	search := call()
	search.Search = true
	want := []string{"https://docs.stripe.com/rate-limits"}

	t.Run("openrouter", func(t *testing.T) {
		url := serve(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"answer\":\"Paris\"}","annotations":[
			{"type":"url_citation","url_citation":{"url":"https://docs.stripe.com/rate-limits","title":"Rate limits"}},
			{"type":"file","file":{"name":"a.pdf"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
		raw, err := newOpenAICompatible(KindOpenRouter, "key", url).Call(context.Background(), "m", search)
		if err != nil || !slices.Equal(raw.Sources, want) {
			t.Errorf("sources %v (%v), want %v", raw.Sources, err, want)
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		url := serve(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2},"content":[
			{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"stripe rate limit"}},
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://docs.stripe.com/rate-limits","title":"Rate limits","encrypted_content":"x","page_age":null}]},
			{"type":"text","text":"{\"answer\":\"Paris\"}"}]}`)
		raw, err := newAnthropic("key", url).Call(context.Background(), "claude-test", search)
		if err != nil || !slices.Equal(raw.Sources, want) || !strings.Contains(raw.Text, "Paris") {
			t.Errorf("sources %v, text %q (%v), want %v", raw.Sources, raw.Text, err, want)
		}
	})
	t.Run("claude", func(t *testing.T) {
		out, err := os.ReadFile("testdata/claude-stream.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := parseClaudeStream(out)
		if err != nil {
			t.Fatal(err)
		}
		// The links of the search, then the page that the fetch read.
		links := []string{"https://docs.stripe.com/rate-limits.md", "https://docs.stripe.com/docs/rate-limits", "https://docs.stripe.com/rate-limits?lang=java", "https://docs.stripe.com/rate-limits"}
		if !slices.Equal(raw.Sources, links) || !strings.Contains(raw.Text, "Paris") || raw.TokensOut != 1365 {
			t.Errorf("sources %v, text %q, %d tokens out", raw.Sources, raw.Text, raw.TokensOut)
		}
		// The search needs the stream of events: the one result object holds no tool result.
		cmd := strings.Join(withClaudeSearch(Presets["claude"].Command), " ")
		if !strings.Contains(cmd, "--output-format stream-json --verbose") || !strings.Contains(cmd, "--tools WebSearch,WebFetch") {
			t.Errorf("the search command: %s", cmd)
		}
	})
}
