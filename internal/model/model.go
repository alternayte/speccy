// Package model is the model gateway (SDD §7.2): one interface with an implementation per
// backend (REQ-100). The gateway resolves a role to a backend and model (REQ-101), enforces
// the budget (REQ-104), the timeouts and retries (REQ-103), and checks every answer against
// its JSON schema (DEC-014).
package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Roles (REQ-101).
const (
	RoleReviewer = "reviewer"
	RoleReader1  = "reader_1"
	RoleReader2  = "reader_2"
	RoleReader3  = "reader_3"
	RoleJudge    = "judge"
	RoleWriter   = "writer"
)

// Roles lists every role in display order.
var Roles = []string{RoleReviewer, RoleReader1, RoleReader2, RoleReader3, RoleJudge, RoleWriter}

// Backend kinds (REQ-100).
const (
	KindOpenAI     = "openai"
	KindAnthropic  = "anthropic"
	KindOpenRouter = "openrouter"
	KindDeepSeek   = "deepseek"
	KindAgentCLI   = "agent_cli"
	KindFake       = "fake"
)

// File is one file of the bundle snapshot. An agent CLI runs in a folder with only these
// files (SDD §14.4).
type File struct {
	Path    string
	Content []byte
}

// Call is one request to a model.
type Call struct {
	Role          string
	PromptVersion string
	// System holds the instructions. Prompt holds the task, with untrusted content inside
	// marked delimiters (SDD §14.3).
	System string
	Prompt string
	// Schema is the JSON schema of the answer (DEC-014).
	Schema    []byte
	MaxTokens int64
	Files     []File
}

// Raw is what a backend returns: the answer text and the tokens it used.
type Raw struct {
	Text      string
	TokensIn  int64
	TokensOut int64
	// Estimated is true when the backend reports no token counts (some agent CLIs).
	Estimated bool
}

// Backend calls one kind of model service.
type Backend interface {
	Call(ctx context.Context, model string, c Call) (Raw, error)
}

// StatusError is an HTTP error from a backend.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

// Transient reports whether a retry can help: HTTP 429 and 5xx (REQ-103).
func Transient(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == 429 || se.Status >= 500
	}
	return false
}

// Fingerprint identifies a call for the fake backend's scripts (BUILD §5): the role, the
// prompt version, and a hash of the content.
func Fingerprint(c Call) string {
	h := sha256.Sum256([]byte(c.System + "\x00" + c.Prompt + "\x00" + string(c.Schema)))
	return c.Role + "/" + c.PromptVersion + "/" + hex.EncodeToString(h[:6])
}

// schemaInstruction asks for JSON when a backend cannot enforce a schema itself.
func schemaInstruction(schema []byte) string {
	return "\n\nReply with one JSON object that matches this JSON Schema. Write only the JSON, with no other text and no code fence.\n" + string(schema)
}

// estimateTokens approximates tokens from text length: about 4 characters per token.
func estimateTokens(s string) int64 { return int64(len(s)+3) / 4 }

// extractJSON returns the JSON object in text. Models sometimes wrap it in a code fence or a
// sentence; the outermost braces hold the object.
func extractJSON(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		t = strings.TrimPrefix(t, "```json")
		t = strings.TrimPrefix(t, "```")
		t = strings.TrimSuffix(strings.TrimSpace(t), "```")
		t = strings.TrimSpace(t)
	}
	start := strings.IndexByte(t, '{')
	end := strings.LastIndexByte(t, '}')
	if start >= 0 && end > start {
		return t[start : end+1]
	}
	return t
}
