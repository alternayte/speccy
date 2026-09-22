package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/mcpclient"
)

// mcpTarget is mcp_connection.command_or_url.
type mcpTarget struct {
	Command   []string `json:"command,omitempty"`
	URL       string   `json:"url,omitempty"`
	SecretEnv string   `json:"secret_env,omitempty"`
}

func mcpAPI(c pgdb.McpConnection) api.MCPConnection {
	var t mcpTarget
	_ = json.Unmarshal(c.CommandOrUrl, &t)
	allow := []string{}
	_ = json.Unmarshal(c.ToolAllowlist, &allow)
	out := api.MCPConnection{
		Id: c.ID, Name: c.Name, Transport: c.Transport, HasSecret: len(c.SecretEncrypted) > 0, SecretLast4: c.SecretLast4,
		ToolAllowlist: allow, IsSearch: c.IsSearch, SearchTool: c.SearchTool, FetchTool: c.FetchTool,
	}
	hosts := []string{}
	_ = json.Unmarshal(c.Hosts, &hosts)
	out.Hosts = hosts
	if len(t.Command) > 0 {
		out.Command = &t.Command
	}
	if t.URL != "" {
		out.Url = &t.URL
	}
	if t.SecretEnv != "" {
		out.SecretEnv = &t.SecretEnv
	}
	return out
}

// ListMCPConnections lists the MCP connections.
func (a *API) ListMCPConnections(ctx context.Context, _ api.ListMCPConnectionsRequestObject) (api.ListMCPConnectionsResponseObject, error) {
	rows, err := a.DB.Queries().ListMCPConnections(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	out := api.ListMCPConnections200JSONResponse{Items: []api.MCPConnection{}}
	for _, r := range rows {
		out.Items = append(out.Items, mcpAPI(r))
	}
	return out, nil
}

// mcpHosts returns the hosts of an input, lower-cased, with no duplicate and no empty entry.
func mcpHosts(in api.MCPConnectionInput) []string {
	out := []string{}
	if in.Hosts == nil {
		return out
	}
	for _, h := range *in.Hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
		h = strings.TrimSuffix(h, "/")
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

func validateMCP(in api.MCPConnectionInput) (mcpTarget, []string, error) {
	var t mcpTarget
	if strings.TrimSpace(in.Name) == "" {
		return t, nil, kernel.Invalid("bad_mcp", "Give the connection a name.")
	}
	switch in.Transport {
	case api.Stdio:
		if in.Command == nil || len(*in.Command) == 0 || strings.TrimSpace((*in.Command)[0]) == "" {
			return t, nil, kernel.Invalid("bad_mcp", "A stdio connection needs a command. Write one argument per item.")
		}
		t.Command = *in.Command
	case api.Http:
		if in.Url == nil || !strings.HasPrefix(*in.Url, "https://") && !strings.HasPrefix(*in.Url, "http://") {
			return t, nil, kernel.Invalid("bad_mcp", "An http connection needs a URL that starts with https:// or http://.")
		}
		t.URL = strings.TrimSpace(*in.Url)
	default:
		return t, nil, kernel.Invalid("bad_mcp", "The transport must be stdio or http.")
	}
	if in.SecretEnv != nil {
		t.SecretEnv = strings.TrimSpace(*in.SecretEnv)
	}
	allow := []string{}
	for _, name := range in.ToolAllowlist {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(allow, name) {
			allow = append(allow, name)
		}
	}
	if in.IsSearch {
		if in.SearchTool == nil || !slices.Contains(allow, *in.SearchTool) {
			return t, nil, kernel.Invalid("bad_mcp", "A search connection needs its search tool on the allowlist.")
		}
	}
	if len(mcpHosts(in)) > 0 && (in.FetchTool == nil || !slices.Contains(allow, *in.FetchTool)) {
		return t, nil, kernel.Invalid("bad_mcp", "A connection with hosts needs one allowed tool marked \"reads a page\". Mark one, or clear the hosts.")
	}
	return t, allow, nil
}

// checkAllowlist refuses a tool that the server marks destructive (REQ-112: read-only tools
// only). It connects to the server to read the hints.
func (a *API) checkAllowlist(ctx context.Context, conn mcpclient.Connection, allow []string) error {
	if len(allow) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := mcpclient.Open(ctx, conn)
	if err != nil {
		return kernel.Invalid("mcp_unreachable", "Speccy could not connect to the server to check its tools: %s.", err.Error())
	}
	defer s.Close()
	tools, err := s.Tools(ctx)
	if err != nil {
		return kernel.Invalid("mcp_unreachable", "The server did not list its tools: %s.", err.Error())
	}
	byName := map[string]mcpclient.Tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	for _, name := range allow {
		t, ok := byName[name]
		if !ok {
			return kernel.Invalid("bad_mcp", "The server has no tool named %q.", name)
		}
		if t.Destructive {
			return kernel.Invalid("bad_mcp", "%s changes data on the server. Only read-only tools can be on the allowlist.", name)
		}
	}
	return nil
}

func (a *API) saveMCP(ctx context.Context, id uuid.UUID, in api.MCPConnectionInput, existing *pgdb.McpConnection) (pgdb.McpConnection, error) {
	t, allow, err := validateMCP(in)
	if err != nil {
		return pgdb.McpConnection{}, err
	}
	sealed, last4 := []byte(nil), ""
	if existing != nil {
		sealed, last4 = existing.SecretEncrypted, existing.SecretLast4
	}
	secret := ""
	if in.Secret != nil && strings.TrimSpace(*in.Secret) != "" {
		secret = strings.TrimSpace(*in.Secret)
		if sealed, last4, err = a.seal(secret); err != nil {
			return pgdb.McpConnection{}, err
		}
	} else if existing != nil && len(existing.SecretEncrypted) > 0 {
		plain, err := a.Sealer.Open(existing.SecretEncrypted)
		if err != nil {
			return pgdb.McpConnection{}, err
		}
		secret = string(plain)
	}
	conn := mcpclient.Connection{Transport: string(in.Transport), Command: t.Command, URL: t.URL, Secret: secret, SecretEnv: t.SecretEnv}
	if err := a.checkAllowlist(ctx, conn, allow); err != nil {
		return pgdb.McpConnection{}, err
	}
	target, _ := json.Marshal(t)
	allowJSON, _ := json.Marshal(allow)
	search := ""
	if in.IsSearch && in.SearchTool != nil {
		search = *in.SearchTool
	}
	hosts := mcpHosts(in)
	hostsJSON, _ := json.Marshal(hosts)
	fetch := ""
	if len(hosts) > 0 && in.FetchTool != nil {
		fetch = *in.FetchTool
	}
	q := a.DB.Queries()
	if existing == nil {
		err = q.InsertMCPConnection(ctx, pgdb.InsertMCPConnectionParams{
			ID: id, WorkspaceID: a.Workspace, Name: strings.TrimSpace(in.Name), Transport: string(in.Transport),
			CommandOrUrl: dbtype.JSON(target), SecretEncrypted: sealed, SecretLast4: last4, ToolAllowlist: dbtype.JSON(allowJSON),
			IsSearch: in.IsSearch, SearchTool: search, CreatedAt: time.Now().UTC(),
			Hosts: dbtype.JSON(hostsJSON), FetchTool: fetch,
		})
	} else {
		err = q.UpdateMCPConnection(ctx, pgdb.UpdateMCPConnectionParams{
			ID: id, WorkspaceID: a.Workspace, Name: strings.TrimSpace(in.Name), Transport: string(in.Transport),
			CommandOrUrl: dbtype.JSON(target), SecretEncrypted: sealed, SecretLast4: last4, ToolAllowlist: dbtype.JSON(allowJSON),
			IsSearch: in.IsSearch, SearchTool: search, Hosts: dbtype.JSON(hostsJSON), FetchTool: fetch,
		})
	}
	if err != nil {
		if isUnique(err) {
			return pgdb.McpConnection{}, kernel.Conflict("name_taken", "A connection named %q exists. Choose another name.", in.Name)
		}
		return pgdb.McpConnection{}, err
	}
	return q.GetMCPConnection(ctx, pgdb.GetMCPConnectionParams{WorkspaceID: a.Workspace, ID: id})
}

// CreateMCPConnection adds an MCP connection.
func (a *API) CreateMCPConnection(ctx context.Context, req api.CreateMCPConnectionRequestObject) (api.CreateMCPConnectionResponseObject, error) {
	c, err := a.saveMCP(ctx, kernel.NewID(), *req.Body, nil)
	if err != nil {
		return nil, err
	}
	return api.CreateMCPConnection201JSONResponse(mcpAPI(c)), nil
}

func (a *API) mcpConnection(ctx context.Context, id uuid.UUID) (pgdb.McpConnection, error) {
	c, err := a.DB.Queries().GetMCPConnection(ctx, pgdb.GetMCPConnectionParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return c, kernel.NotFound("mcp_not_found", "No MCP connection has the ID %s.", id)
	}
	return c, err
}

// UpdateMCPConnection changes an MCP connection.
func (a *API) UpdateMCPConnection(ctx context.Context, req api.UpdateMCPConnectionRequestObject) (api.UpdateMCPConnectionResponseObject, error) {
	cur, err := a.mcpConnection(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	c, err := a.saveMCP(ctx, cur.ID, *req.Body, &cur)
	if err != nil {
		return nil, err
	}
	return api.UpdateMCPConnection200JSONResponse(mcpAPI(c)), nil
}

// DeleteMCPConnection deletes an MCP connection.
func (a *API) DeleteMCPConnection(ctx context.Context, req api.DeleteMCPConnectionRequestObject) (api.DeleteMCPConnectionResponseObject, error) {
	if _, err := a.mcpConnection(ctx, req.ConnectionId); err != nil {
		return nil, err
	}
	if err := a.DB.Queries().DeleteMCPConnection(ctx, pgdb.DeleteMCPConnectionParams{WorkspaceID: a.Workspace, ID: req.ConnectionId}); err != nil {
		return nil, err
	}
	return api.DeleteMCPConnection204Response{}, nil
}

func (a *API) connection(c pgdb.McpConnection) (mcpclient.Connection, error) {
	var t mcpTarget
	_ = json.Unmarshal(c.CommandOrUrl, &t)
	conn := mcpclient.Connection{Transport: c.Transport, Command: t.Command, URL: t.URL, SecretEnv: t.SecretEnv}
	if len(c.SecretEncrypted) > 0 {
		plain, err := a.Sealer.Open(c.SecretEncrypted)
		if err != nil {
			return conn, err
		}
		conn.Secret = string(plain)
	}
	return conn, nil
}

// ListMCPTools connects to the server and lists its tools, for the allowlist.
func (a *API) ListMCPTools(ctx context.Context, req api.ListMCPToolsRequestObject) (api.ListMCPToolsResponseObject, error) {
	c, err := a.mcpConnection(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	conn, err := a.connection(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := mcpclient.Open(ctx, conn)
	if err != nil {
		return nil, kernel.Invalid("mcp_unreachable", "Speccy could not connect to %s: %s.", c.Name, err.Error())
	}
	defer s.Close()
	tools, err := s.Tools(ctx)
	if err != nil {
		return nil, kernel.Invalid("mcp_unreachable", "%s did not list its tools: %s.", c.Name, err.Error())
	}
	out := api.ListMCPTools200JSONResponse{Items: []api.MCPTool{}}
	for _, t := range tools {
		out.Items = append(out.Items, api.MCPTool{Name: t.Name, Description: t.Description, ReadOnly: t.ReadOnly, Destructive: t.Destructive})
	}
	return out, nil
}

// SearchSource returns the MCP connection marked search, as a review searcher, or nil when
// there is none (REQ-034).
func (a *API) SearchSource(ctx context.Context) (review.Searcher, error) {
	rows, err := a.DB.Queries().ListMCPConnections(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	for _, c := range rows {
		if !c.IsSearch || c.SearchTool == "" {
			continue
		}
		conn, err := a.connection(c)
		if err != nil {
			return nil, err
		}
		return &mcpSearcher{name: c.Name, tool: c.SearchTool, conn: conn}, nil
	}
	return nil, nil
}

type mcpSearcher struct {
	name string
	tool string
	conn mcpclient.Connection
}

func (m *mcpSearcher) Name() string { return m.name }

// Search opens a session, finds the query argument, and calls the search tool. The result is
// untrusted data (REQ-113); the review puts it in a data block.
func (m *mcpSearcher) Search(ctx context.Context, query string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s, err := mcpclient.Open(ctx, m.conn)
	if err != nil {
		return "", err
	}
	defer s.Close()
	tools, err := s.Tools(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range tools {
		if t.Name != m.tool {
			continue
		}
		arg, err := mcpclient.QueryArgument(t.InputSchema)
		if err != nil {
			return "", fmt.Errorf("%s: %w", m.tool, err)
		}
		return s.Call(ctx, m.tool, map[string]any{arg: query})
	}
	return "", fmt.Errorf("%s has no tool named %s", m.name, m.tool)
}

// FetchSource returns the connection that reads host, or nil when no connection lists it
// (DEC-021). Speccy matches by host, so no model picks the tool.
func (a *API) FetchSource(ctx context.Context, host string) (review.Fetcher, error) {
	rows, err := a.DB.Queries().ListMCPConnections(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	host = strings.ToLower(host)
	for _, c := range rows {
		hosts := []string{}
		_ = json.Unmarshal(c.Hosts, &hosts)
		if c.FetchTool == "" || !slices.Contains(hosts, host) {
			continue
		}
		conn, err := a.connection(c)
		if err != nil {
			return nil, err
		}
		return &mcpFetcher{name: c.Name, tool: c.FetchTool, conn: conn}, nil
	}
	return nil, nil
}

type mcpFetcher struct {
	name string
	tool string
	conn mcpclient.Connection
}

func (m *mcpFetcher) Name() string { return m.name }

// Fetch reads one page or issue. The result is untrusted data (REQ-113); the review puts it in
// a data block.
func (m *mcpFetcher) Fetch(ctx context.Context, target string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s, err := mcpclient.Open(ctx, m.conn)
	if err != nil {
		return "", err
	}
	defer s.Close()
	tools, err := s.Tools(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range tools {
		if t.Name != m.tool {
			continue
		}
		arg, err := mcpclient.URLArgument(t.InputSchema)
		if err != nil {
			return "", fmt.Errorf("%s: %w", m.tool, err)
		}
		return s.Call(ctx, m.tool, map[string]any{arg: target})
	}
	return "", fmt.Errorf("%s has no tool named %s", m.name, m.tool)
}
