// Package mcpclient connects to MCP servers that an admin configured (DEC-023, REQ-112). Tool
// results are untrusted data (REQ-113, SDD §14.3); callers put them inside marked delimiters.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/speccy/internal/kernel"
)

// maxResultBytes bounds one tool result, so a large page cannot fill a prompt.
const maxResultBytes = 20000

// Connection is one configured MCP server.
type Connection struct {
	Transport string   // stdio or http
	Command   []string // stdio
	URL       string   // http
	Secret    string
	// SecretEnv names the environment variable that carries the secret to a stdio server.
	// An http server gets it as a bearer token.
	SecretEnv string
}

// Tool is a tool the server offers.
type Tool struct {
	Name        string
	Description string
	// ReadOnly and Destructive are the server's own hints.
	ReadOnly    bool
	Destructive bool
	InputSchema json.RawMessage
}

// Session is an open connection.
type Session struct {
	cs *mcp.ClientSession
}

// Open connects to the server.
func Open(ctx context.Context, c Connection) (*Session, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "speccy", Version: kernel.Version}, nil)
	var t mcp.Transport
	switch c.Transport {
	case "stdio":
		if len(c.Command) == 0 {
			return nil, errors.New("the stdio connection has no command")
		}
		cmd := exec.Command(c.Command[0], c.Command[1:]...)
		cmd.Env = os.Environ()
		if c.Secret != "" && c.SecretEnv != "" {
			cmd.Env = append(cmd.Env, c.SecretEnv+"="+c.Secret)
		}
		t = &mcp.CommandTransport{Command: cmd}
	case "http":
		if c.URL == "" {
			return nil, errors.New("the http connection has no URL")
		}
		hc := &http.Client{}
		if c.Secret != "" {
			hc.Transport = bearer{token: c.Secret, next: http.DefaultTransport}
		}
		t = &mcp.StreamableClientTransport{Endpoint: c.URL, HTTPClient: hc, MaxRetries: -1}
	default:
		return nil, fmt.Errorf("unknown transport %q", c.Transport)
	}
	cs, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &Session{cs: cs}, nil
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// Close ends the session.
func (s *Session) Close() { _ = s.cs.Close() }

// Tools lists the server's tools, sorted by name.
func (s *Session) Tools(ctx context.Context) ([]Tool, error) {
	var out []Tool
	for tool, err := range s.cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		schema, _ := json.Marshal(tool.InputSchema)
		t := Tool{Name: tool.Name, Description: tool.Description, InputSchema: schema}
		if a := tool.Annotations; a != nil {
			t.ReadOnly = a.ReadOnlyHint
			t.Destructive = a.DestructiveHint != nil && *a.DestructiveHint
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Call runs a tool and returns its text content, capped at maxResultBytes.
func (s *Session) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	res, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 && res.StructuredContent != nil {
		js, _ := json.Marshal(res.StructuredContent)
		b.Write(js)
	}
	text := b.String()
	if len(text) > maxResultBytes {
		text = text[:maxResultBytes] + "\n[cut: the result was longer]"
	}
	if res.IsError {
		return "", fmt.Errorf("the tool %s failed: %s", name, strings.TrimSpace(text))
	}
	return text, nil
}

// URLArgument returns the argument name that takes one page address or key, else the tool's
// first required string property.
func URLArgument(schema json.RawMessage) (string, error) {
	return argument(schema, []string{"url", "uri", "link", "page_url", "id", "key"},
		"the tool has no string argument for a URL")
}

// QueryArgument returns the name of the tool's query argument.
func QueryArgument(schema json.RawMessage) (string, error) {
	return argument(schema, []string{"query", "q", "search", "search_query"},
		"the tool has no string argument for a query")
}

func argument(schema json.RawMessage, prefer []string, missing string) (string, error) {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return "", fmt.Errorf("the tool's input schema does not parse: %w", err)
	}
	for _, name := range prefer {
		if _, ok := s.Properties[name]; ok {
			return name, nil
		}
	}
	for _, name := range s.Required {
		if s.Properties[name].Type == "string" {
			return name, nil
		}
	}
	return "", errors.New(missing)
}
