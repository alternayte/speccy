package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AgentCLIConfig is the config of an agent CLI backend (SDD §12.5, REQ-102).
type AgentCLIConfig struct {
	// Preset is claude, cursor-agent, opencode, pi, or custom.
	Preset string `json:"preset"`
	// Command is the command template of a custom CLI. {model}, {schema}, and {prompt_file}
	// are replaced; an empty element is kept as an empty argument.
	Command []string `json:"command,omitempty"`
	// PromptVia is stdin (default) or file for a custom CLI.
	PromptVia string `json:"prompt_via,omitempty"`
}

// promptFile is the file a CLI that does not read stdin gets its prompt from.
const promptFile = "prompt.md"

const followFile = "Follow the instructions in prompt.md in the current folder. Reply with the JSON object it asks for, and nothing else."

// Preset is a verified command line for one agent CLI (§19 Q4, resolved 2026-09-19).
type Preset struct {
	Name      string
	Command   []string
	PromptVia string // stdin or file
	// SchemaFlag is true when the CLI enforces the schema itself (claude --json-schema).
	SchemaFlag bool
	parse      func(stdout []byte) (Raw, error)
	// Verified says how the flags were checked.
	Verified string
}

// Presets are the built-in agent CLI presets. Each runs with no tools or in a read-only mode,
// in a folder with only the bundle snapshot (SDD §14.4).
var Presets = map[string]Preset{
	"claude": {
		Name: "claude",
		Command: []string{"claude", "-p", "--output-format", "json", "--json-schema", "{schema}",
			"--tools", "", "--no-session-persistence", "--strict-mcp-config", "--model", "{model}"},
		PromptVia: "stdin", SchemaFlag: true, parse: parseClaude,
		Verified: "claude 2.1.277, live call",
	},
	"cursor-agent": {
		Name: "cursor-agent",
		// --trust takes the workspace trust prompt off. Speccy gives every call a fresh temp
		// folder, so cursor-agent meets an untrusted workspace each time, and it asks a
		// question that nobody can answer: stdout and stderr are pipes, and there is no
		// person at the other end.
		Command:   []string{"cursor-agent", "-p", "--output-format", "json", "--mode", "ask", "--trust", "--model", "{model}", followFile},
		PromptVia: "file", parse: parseCursor,
		Verified: "cursor-agent 2026.06.19, flags read from the installed CLI; --trust is \"Trust the current workspace without prompting (only works with --print/headless mode)\"",
	},
	"opencode": {
		Name: "opencode",
		Command: []string{"opencode", "run", "--format", "json", "--pure", "--agent", "plan", "-m", "{model}",
			"-f", promptFile, "--", followFile},
		PromptVia: "file", parse: parseOpencode,
		Verified: "opencode 1.18.26, live call",
	},
	"pi": {
		Name: "pi",
		Command: []string{"pi", "-p", "--mode", "json", "--no-tools", "--no-session", "--no-context-files",
			"--no-extensions", "--no-skills", "--model", "{model}"},
		PromptVia: "stdin", parse: parsePi,
		Verified: "pi 0.85.1, live call",
	},
}

// PresetNames lists the presets in display order.
var PresetNames = []string{"claude", "cursor-agent", "opencode", "pi"}

type agentCLI struct {
	preset Preset
}

func newAgentCLI(cfg AgentCLIConfig) (*agentCLI, error) {
	if cfg.Preset == "custom" {
		if len(cfg.Command) == 0 {
			return nil, errors.New("a custom agent CLI needs a command")
		}
		via := cfg.PromptVia
		if via == "" {
			via = "stdin"
		}
		if via != "stdin" && via != "file" {
			return nil, fmt.Errorf("prompt_via %q is not stdin or file", via)
		}
		return &agentCLI{preset: Preset{Name: "custom", Command: cfg.Command, PromptVia: via, parse: parsePlain}}, nil
	}
	p, ok := Presets[cfg.Preset]
	if !ok {
		return nil, fmt.Errorf("unknown agent CLI preset %q", cfg.Preset)
	}
	return &agentCLI{preset: p}, nil
}

// Call runs the CLI once. SDD §14.4: the working folder holds only the bundle snapshot and
// the prompt; the command comes from admin config, never from doc content.
func (a *agentCLI) Call(ctx context.Context, model string, c Call) (Raw, error) {
	dir, err := os.MkdirTemp("", "speccy-agent-*")
	if err != nil {
		return Raw{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for _, f := range c.Files {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		rel, err := filepath.Rel(dir, dst)
		if err != nil || strings.HasPrefix(rel, "..") {
			return Raw{}, fmt.Errorf("the bundle file %q leaves the working folder", f.Path)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return Raw{}, err
		}
		if err := os.WriteFile(dst, f.Content, 0o600); err != nil {
			return Raw{}, err
		}
	}
	prompt := c.Prompt
	if c.System != "" {
		prompt = c.System + "\n\n" + prompt
	}
	if !a.preset.SchemaFlag {
		prompt += schemaInstruction(c.Schema)
	}
	if err := os.WriteFile(filepath.Join(dir, promptFile), []byte(prompt), 0o600); err != nil {
		return Raw{}, err
	}

	command := a.preset.Command
	if c.Search && a.preset.Name == "claude" {
		command = withClaudeSearch(command)
	}
	args := make([]string, len(command))
	for i, arg := range command {
		arg = strings.ReplaceAll(arg, "{model}", model)
		arg = strings.ReplaceAll(arg, "{schema}", string(c.Schema))
		arg = strings.ReplaceAll(arg, "{prompt_file}", promptFile)
		args[i] = arg
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	detach(cmd)
	if a.preset.PromptVia == "stdin" {
		cmd.Stdin = strings.NewReader(prompt)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Raw{}, ctx.Err()
		}
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			return Raw{}, fmt.Errorf("%s is not installed or not on PATH", args[0])
		}
		return Raw{}, fmt.Errorf("%s exited with an error: %v: %s", args[0], err, tail(stderr.String(), 400))
	}
	raw, err := a.preset.parse(stdout.Bytes())
	if err != nil {
		return Raw{}, fmt.Errorf("%s: %w", args[0], err)
	}
	if raw.TokensIn == 0 && raw.TokensOut == 0 {
		raw.TokensIn, raw.TokensOut, raw.Estimated = estimateTokens(prompt), estimateTokens(raw.Text), true
	}
	return raw, nil
}

// withClaudeSearch lets claude use its web tools and nothing else (REQ-034).
func withClaudeSearch(command []string) []string {
	out := make([]string, 0, len(command)+2)
	for i := 0; i < len(command); i++ {
		if command[i] == "--tools" && i+1 < len(command) {
			out = append(out, "--tools", "WebSearch,WebFetch", "--allowedTools", "WebSearch,WebFetch")
			i++
			continue
		}
		out = append(out, command[i])
	}
	return out
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// parseClaude reads claude --output-format json: one result object, with the schema answer
// in structured_output.
func parseClaude(out []byte) (Raw, error) {
	var r struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &r); err != nil {
		return Raw{}, fmt.Errorf("the output is not the JSON result object: %w", err)
	}
	if r.IsError {
		return Raw{}, fmt.Errorf("the run failed: %s", tail(r.Result, 300))
	}
	text := r.Result
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		text = string(r.StructuredOutput)
	}
	return Raw{Text: text, TokensIn: r.Usage.InputTokens + r.Usage.CacheCreationInputTokens + r.Usage.CacheReadInputTokens, TokensOut: r.Usage.OutputTokens}, nil
}

// parseCursor reads cursor-agent --output-format json: one result object. It reports no
// token counts.
func parseCursor(out []byte) (Raw, error) {
	var r struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		// Usage holds the counts the CLI reports. A version that reports none leaves them at
		// zero, and the caller estimates them instead.
		Usage struct {
			InputTokens     int64 `json:"inputTokens"`
			OutputTokens    int64 `json:"outputTokens"`
			CacheReadTokens int64 `json:"cacheReadTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &r); err != nil {
		return Raw{}, fmt.Errorf("the output is not the JSON result object: %w", err)
	}
	if r.IsError {
		return Raw{}, fmt.Errorf("the run failed: %s", tail(r.Result, 300))
	}
	// The cached tokens went into the request, so they count as input, as they do for the
	// other backends.
	return Raw{Text: r.Result, TokensIn: r.Usage.InputTokens + r.Usage.CacheReadTokens, TokensOut: r.Usage.OutputTokens}, nil
}

// parseOpencode reads opencode run --format json: one JSON event per line. Text parts hold
// the answer, step_finish parts hold tokens, and an error event ends the run.
func parseOpencode(out []byte) (Raw, error) {
	var raw Raw
	var text strings.Builder
	err := eachLine(out, func(line []byte) error {
		var ev struct {
			Type  string `json:"type"`
			Error struct {
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
			Part struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Tokens struct {
					Input  int64 `json:"input"`
					Output int64 `json:"output"`
					Cache  struct {
						Read  int64 `json:"read"`
						Write int64 `json:"write"`
					} `json:"cache"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if json.Unmarshal(line, &ev) != nil {
			return nil //nolint:nilerr // a line that is not an event (a log line) is skipped
		}
		switch ev.Type {
		case "error":
			return fmt.Errorf("the run failed: %s", ev.Error.Data.Message)
		case "text":
			text.WriteString(ev.Part.Text)
		case "step_finish":
			t := ev.Part.Tokens
			raw.TokensIn += t.Input + t.Cache.Read + t.Cache.Write
			raw.TokensOut += t.Output
		}
		return nil
	})
	if err != nil {
		return Raw{}, err
	}
	raw.Text = text.String()
	if raw.Text == "" {
		return Raw{}, errors.New("the run printed no answer")
	}
	return raw, nil
}

// parsePi reads pi --mode json: one JSON event per line. The agent_end event holds the
// messages; the last assistant message holds the answer and its token usage.
func parsePi(out []byte) (Raw, error) {
	var raw Raw
	found := false
	err := eachLine(out, func(line []byte) error {
		var ev struct {
			Type     string `json:"type"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
				Usage        struct {
					Input      int64 `json:"input"`
					Output     int64 `json:"output"`
					CacheRead  int64 `json:"cacheRead"`
					CacheWrite int64 `json:"cacheWrite"`
				} `json:"usage"`
			} `json:"messages"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.Type != "agent_end" {
			return nil //nolint:nilerr // only the agent_end event matters
		}
		for i := len(ev.Messages) - 1; i >= 0; i-- {
			m := ev.Messages[i]
			if m.Role != "assistant" {
				continue
			}
			if m.StopReason == "error" {
				return fmt.Errorf("the run failed: %s", m.ErrorMessage)
			}
			var text strings.Builder
			for _, c := range m.Content {
				if c.Type == "text" {
					text.WriteString(c.Text)
				}
			}
			raw = Raw{Text: text.String(), TokensIn: m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheWrite, TokensOut: m.Usage.Output}
			found = true
			return nil
		}
		return nil
	})
	if err != nil {
		return Raw{}, err
	}
	if !found {
		return Raw{}, errors.New("the run printed no agent_end event")
	}
	return raw, nil
}

// parsePlain treats stdout as the answer (a custom CLI).
func parsePlain(out []byte) (Raw, error) {
	return Raw{Text: string(out)}, nil
}

func eachLine(out []byte, fn func([]byte) error) error {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Installed reports whether a preset's program is on PATH.
func Installed(preset string) bool {
	p, ok := Presets[preset]
	if !ok {
		return false
	}
	_, err := exec.LookPath(p.Command[0])
	return err == nil
}
