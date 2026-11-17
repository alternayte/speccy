package breakdown

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/mcpclient"
	"github.com/alternayte/speccy/internal/source/github"
)

// The trackers a push writes to.
const (
	TrackerLinear = "linear"
	TrackerJira   = "jira"
	TrackerGitHub = "github"
)

// LinearHost is the host of Linear's MCP connection.
const LinearHost = "linear.app"

// issue is one issue a tracker created. Ref is the tracker's own ID, which a parent link and a
// blocking relation name.
type issue struct {
	Key string
	URL string
	Ref string
}

// tracker makes the fixed calls of one tracker. The model never picks a call.
type tracker interface {
	// check confirms the server has every tool the push calls, before the push writes anything.
	check(ctx context.Context, deps bool) error
	createEpic(ctx context.Context, title, body string) (issue, error)
	// createItem creates an item under epic, assigned to who, blocked by the issues it depends on.
	createItem(ctx context.Context, title, body string, epic issue, who *Assignee, blockedBy []issue) (issue, error)
	assignees(ctx context.Context, query string) ([]Assignee, error)
	close()
}

// openTracker opens the tracker a push names. target is the Linear team, the Jira project URL,
// or the GitHub repo.
func (a *API) openTracker(ctx context.Context, kind, target string) (tracker, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, kernel.Invalid("no_target", "Name where the issues go: the Linear team, the Jira project URL, or the GitHub repo.")
	}
	switch kind {
	case TrackerLinear:
		s, err := a.session(ctx, LinearHost, "Linear")
		if err != nil {
			return nil, err
		}
		return &linear{s: s, team: target}, nil
	case TrackerJira:
		host, project, err := jiraProject(target)
		if err != nil {
			return nil, err
		}
		s, err := a.session(ctx, host, "Jira at "+host)
		if err != nil {
			return nil, err
		}
		j := &jira{s: s, host: host, project: project, cloud: strings.HasSuffix(host, ".atlassian.net")}
		return j, nil
	case TrackerGitHub:
		repo, apiURL, err := githubRepo(target)
		if err != nil {
			return nil, err
		}
		if a.GitHub == nil {
			return nil, kernel.Invalid("no_github", "Speccy has no GitHub credential, so it cannot create issues in %s.", repo)
		}
		c, err := a.GitHub(ctx, apiURL)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, kernel.Invalid("no_github", "Speccy has no GitHub credential, so it cannot create issues in %s.", repo)
		}
		return &gh{c: c, repo: repo}, nil
	}
	return nil, kernel.Invalid("bad_tracker", "The tracker must be linear, jira or github.")
}

// session opens the admin MCP connection that lists host.
func (a *API) session(ctx context.Context, host, name string) (*mcpclient.Session, error) {
	if a.Connection == nil {
		return nil, noConnection(name, host)
	}
	conn, ok, err := a.Connection(ctx, host)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, noConnection(name, host)
	}
	s, err := mcpclient.Open(ctx, conn)
	if err != nil {
		return nil, kernel.Invalid("mcp_unreachable", "Speccy could not connect to %s: %s.", name, err.Error())
	}
	return s, nil
}

func noConnection(name, host string) error {
	return kernel.Invalid("no_connection",
		"No MCP connection lists the host %s, so Speccy cannot write to %s. An admin adds one in Admin → MCP connections, or export the breakdown as markdown or CSV.",
		host, name)
}

// jiraProject reads a Jira project URL: https://host/browse/KEY, https://host/projects/KEY, or
// host/KEY.
func jiraProject(target string) (host, project string, err error) {
	raw := target
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" {
		return "", "", kernel.Invalid("bad_target", "Paste the Jira project URL, such as https://acme.atlassian.net/browse/PAY.")
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if p == "browse" || p == "projects" || p == "jira" || p == "software" || p == "c" || p == "board" || p == "boards" {
			continue
		}
		key, _, _ := strings.Cut(p, "-")
		if jiraKeyRe.MatchString(key) {
			return strings.ToLower(u.Host), key, nil
		}
	}
	return "", "", kernel.Invalid("bad_target", "The URL %s names no Jira project. Paste one such as https://acme.atlassian.net/browse/PAY.", target)
}

var jiraKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}$`)

// githubRepo reads owner/name or a GitHub repo URL.
func githubRepo(target string) (repo, apiURL string, err error) {
	if !strings.Contains(target, "://") && strings.Count(target, "/") == 1 {
		return target, "", nil
	}
	u, perr := url.Parse(target)
	parts := []string{}
	if perr == nil {
		parts = strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	}
	if perr != nil || len(parts) < 2 {
		return "", "", kernel.Invalid("bad_target", "Name the GitHub repo as owner/name or its URL.")
	}
	repo = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if h := strings.ToLower(u.Host); h != "github.com" && h != "www.github.com" {
		apiURL = "https://" + u.Host + "/api/v3"
	}
	return repo, apiURL, nil
}

// tools lists the server's tools by name.
func tools(ctx context.Context, s *mcpclient.Session) (map[string]mcpclient.Tool, error) {
	list, err := s.Tools(ctx)
	if err != nil {
		return nil, kernel.Invalid("mcp_unreachable", "The server did not list its tools: %s.", err.Error())
	}
	out := map[string]mcpclient.Tool{}
	for _, t := range list {
		out[t.Name] = t
	}
	return out, nil
}

// properties are the argument names of a tool's input schema.
func properties(t mcpclient.Tool) map[string]bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(t.InputSchema, &s)
	out := map[string]bool{}
	for k := range s.Properties {
		out[k] = true
	}
	return out
}

// pick returns the first argument name the tool takes, or "".
func pick(props map[string]bool, names ...string) string {
	for _, n := range names {
		if props[n] {
			return n
		}
	}
	return ""
}

func missingTools(server string, names []string) error {
	return kernel.Invalid("missing_tool", "%s has no tool %s, so Speccy cannot push to it. Check the server's version.",
		server, strings.Join(names, " or "))
}

// field finds the first value of one of keys in a tool result, which is JSON or text holding
// JSON. A tool's result shape is not published, so Speccy reads it defensively.
func field(result string, keys ...string) string {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &v); err != nil {
		if i := strings.IndexAny(result, "{["); i >= 0 {
			_ = json.Unmarshal([]byte(result[i:]), &v)
		}
	}
	for _, k := range keys {
		if s := find(v, k); s != "" {
			return s
		}
	}
	return ""
}

func find(v any, key string) string {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x[key]; ok {
			switch t := s.(type) {
			case string:
				return t
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if s := find(x[k], key); s != "" {
				return s
			}
		}
	case []any:
		for _, e := range x {
			if s := find(e, key); s != "" {
				return s
			}
		}
	}
	return ""
}

// records reads the list of objects in a tool result: the result itself when it is a list, or
// the first list of objects it holds.
func records(result string) []map[string]any {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &v); err != nil {
		if i := strings.IndexAny(result, "{["); i >= 0 {
			_ = json.Unmarshal([]byte(result[i:]), &v)
		}
	}
	var walk func(any) []map[string]any
	walk = func(v any) []map[string]any {
		switch x := v.(type) {
		case []any:
			var out []map[string]any
			for _, e := range x {
				if m, ok := e.(map[string]any); ok {
					out = append(out, m)
				}
			}
			if len(out) > 0 {
				return out
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if out := walk(x[k]); len(out) > 0 {
					return out
				}
			}
		}
		return nil
	}
	return walk(v)
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func callTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 60*time.Second)
}

// linear writes to Linear through its MCP server. An epic is a parent issue and an item a
// sub-issue. A dependency is a blocking relation, set when the item is created, so no issue is
// ever edited.
type linear struct {
	s      *mcpclient.Session
	team   string
	create string
	props  map[string]bool
	users  string
}

func (l *linear) close() { l.s.Close() }

func (l *linear) check(ctx context.Context, deps bool) error {
	ts, err := tools(ctx, l.s)
	if err != nil {
		return err
	}
	for _, name := range []string{"save_issue", "create_issue"} {
		if t, ok := ts[name]; ok {
			l.create, l.props = name, properties(t)
			break
		}
	}
	if l.create == "" {
		return missingTools("Linear", []string{"save_issue", "create_issue"})
	}
	if pick(l.props, "parentId", "parent") == "" {
		return kernel.Invalid("missing_tool", "Linear's %s takes no parent issue, so Speccy cannot make sub-issues.", l.create)
	}
	if deps && pick(l.props, "blockedBy") == "" {
		return kernel.Invalid("missing_tool", "Linear's %s takes no blockedBy, so Speccy cannot set the dependencies.", l.create)
	}
	if _, ok := ts["list_users"]; ok {
		l.users = "list_users"
	}
	return nil
}

func (l *linear) save(ctx context.Context, args map[string]any) (issue, error) {
	ctx, cancel := callTimeout(ctx)
	defer cancel()
	args["team"] = l.team
	res, err := l.s.Call(ctx, l.create, args)
	if err != nil {
		return issue{}, err
	}
	out := issue{Ref: field(res, "id"), Key: field(res, "identifier"), URL: field(res, "url")}
	if out.Ref == "" || out.Key == "" {
		return issue{}, fmt.Errorf("the Linear answer names no id and identifier for the new issue: %s", short(res))
	}
	return out, nil
}

func (l *linear) createEpic(ctx context.Context, title, body string) (issue, error) {
	return l.save(ctx, map[string]any{"title": title, "description": body})
}

func (l *linear) createItem(ctx context.Context, title, body string, epic issue, who *Assignee, blockedBy []issue) (issue, error) {
	args := map[string]any{"title": title, "description": body, pick(l.props, "parentId", "parent"): epic.Ref}
	if who != nil {
		// Assigning an agent app makes it the delegate, which starts its agent session.
		if who.Kind == "agent" && l.props["delegate"] {
			args["delegate"] = who.ID
		} else {
			args[pick(l.props, "assignee", "assigneeId")] = who.ID
		}
	}
	if len(blockedBy) > 0 {
		var refs []string
		for _, b := range blockedBy {
			refs = append(refs, b.Ref)
		}
		args["blockedBy"] = refs
	}
	return l.save(ctx, args)
}

func (l *linear) assignees(ctx context.Context, query string) ([]Assignee, error) {
	if l.users == "" {
		return nil, missingTools("Linear", []string{"list_users"})
	}
	ctx, cancel := callTimeout(ctx)
	defer cancel()
	args := map[string]any{"limit": 250, "team": l.team}
	if query != "" {
		args["query"] = query
	}
	res, err := l.s.Call(ctx, l.users, args)
	if err != nil {
		return nil, err
	}
	var out []Assignee
	for _, u := range records(res) {
		id := str(u, "id")
		if id == "" {
			continue
		}
		kind := "person"
		if app, _ := u["app"].(bool); app {
			kind = "agent"
		}
		if t := strings.ToLower(str(u, "type", "kind")); t == "app" || t == "agent" || t == "bot" {
			kind = "agent"
		}
		out = append(out, Assignee{Kind: kind, ID: id, Name: firstNonEmpty(str(u, "displayName", "name", "email"), id)})
	}
	return out, nil
}

// jira writes to Jira through its MCP server, chosen by host: Atlassian's Rovo server for Jira
// Cloud, and mcp-atlassian for any other host, which serves Data Center.
type jira struct {
	s       *mcpclient.Session
	host    string
	project string
	cloud   bool
	cloudID string
	link    mcpclient.Tool
	users   mcpclient.Tool
	hasLink bool
	hasUser bool
}

func (j *jira) close() { j.s.Close() }

func (j *jira) server() string {
	if j.cloud {
		return "Atlassian's MCP server"
	}
	return "mcp-atlassian"
}

func (j *jira) check(ctx context.Context, deps bool) error {
	ts, err := tools(ctx, j.s)
	if err != nil {
		return err
	}
	if j.cloud {
		var missing []string
		for _, n := range []string{"createJiraIssue", "getAccessibleAtlassianResources"} {
			if _, ok := ts[n]; !ok {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			return missingTools(j.server(), missing)
		}
		j.link, j.hasLink = ts["createJiraIssueLink"]
		j.users, j.hasUser = ts["lookupJiraAccountId"]
		if deps && !j.hasLink {
			return missingTools(j.server(), []string{"createJiraIssueLink"})
		}
		return j.resolveCloud(ctx)
	}
	if _, ok := ts["jira_create_issue"]; !ok {
		return missingTools(j.server(), []string{"jira_create_issue"})
	}
	j.link, j.hasLink = ts["jira_create_issue_link"]
	j.users, j.hasUser = ts["jira_search_assignable_users"]
	if deps && !j.hasLink {
		return missingTools(j.server(), []string{"jira_create_issue_link"})
	}
	return nil
}

// resolveCloud finds the cloud ID of the site the project URL names.
func (j *jira) resolveCloud(ctx context.Context) error {
	ctx, cancel := callTimeout(ctx)
	defer cancel()
	res, err := j.s.Call(ctx, "getAccessibleAtlassianResources", map[string]any{})
	if err != nil {
		return err
	}
	for _, r := range records(res) {
		u, err := url.Parse(str(r, "url"))
		if err == nil && strings.EqualFold(u.Host, j.host) {
			j.cloudID = str(r, "id")
			return nil
		}
	}
	return kernel.Invalid("no_site", "The Atlassian connection cannot reach the site %s.", j.host)
}

func (j *jira) createIssue(ctx context.Context, kind, title, body, parent string, who *Assignee) (issue, error) {
	ctx, cancel := callTimeout(ctx)
	defer cancel()
	var res string
	var err error
	if j.cloud {
		args := map[string]any{"cloudId": j.cloudID, "projectKey": j.project, "issueType": kind, "summary": title,
			"description": body}
		if parent != "" {
			args["parent"] = parent
		}
		if who != nil {
			args["assignee"] = who.ID
		}
		res, err = j.s.Call(ctx, "createJiraIssue", args)
	} else {
		args := map[string]any{"project_key": j.project, "summary": title, "issue_type": kind, "description": body}
		if parent != "" {
			// mcp-atlassian maps epicKey to the Epic Link field Data Center uses, and to parent on Cloud.
			extra, _ := json.Marshal(map[string]string{"epicKey": parent})
			args["additional_fields"] = string(extra)
		}
		if who != nil {
			args["assignee"] = who.ID
		}
		res, err = j.s.Call(ctx, "jira_create_issue", args)
	}
	if err != nil {
		return issue{}, err
	}
	key := field(res, "key")
	if key == "" {
		return issue{}, fmt.Errorf("the Jira answer names no key for the new issue: %s", short(res))
	}
	u := field(res, "browse_url", "url", "self")
	if u == "" || strings.Contains(u, "/rest/api/") {
		u = "https://" + j.host + "/browse/" + key
	}
	return issue{Key: key, URL: u, Ref: key}, nil
}

func (j *jira) createEpic(ctx context.Context, title, body string) (issue, error) {
	return j.createIssue(ctx, "Epic", title, body, "", nil)
}

func (j *jira) createItem(ctx context.Context, title, body string, epic issue, who *Assignee, blockedBy []issue) (issue, error) {
	out, err := j.createIssue(ctx, "Task", title, body, epic.Key, who)
	if err != nil {
		return issue{}, err
	}
	for _, b := range blockedBy {
		if err := j.block(ctx, b, out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// block links blocker to blocked with the Blocks link type. In Jira's link, the inward issue
// "blocks" the outward issue.
func (j *jira) block(ctx context.Context, blocker, blocked issue) error {
	ctx, cancel := callTimeout(ctx)
	defer cancel()
	props := properties(j.link)
	args := map[string]any{}
	if j.cloud {
		args["cloudId"] = j.cloudID
	}
	args[firstNonEmpty(pick(props, "link_type", "linkType", "type"), "type")] = "Blocks"
	args[firstNonEmpty(pick(props, "inward_issue_key", "inwardIssueKey", "inwardIssue"), "inwardIssue")] = blocker.Key
	args[firstNonEmpty(pick(props, "outward_issue_key", "outwardIssueKey", "outwardIssue"), "outwardIssue")] = blocked.Key
	_, err := j.s.Call(ctx, j.link.Name, args)
	return err
}

func (j *jira) assignees(ctx context.Context, query string) ([]Assignee, error) {
	if !j.hasUser {
		name := "jira_search_assignable_users"
		if j.cloud {
			name = "lookupJiraAccountId"
		}
		return nil, missingTools(j.server(), []string{name})
	}
	if strings.TrimSpace(query) == "" {
		return nil, kernel.Invalid("no_query", "Type part of a name or an email to find a Jira user.")
	}
	ctx, cancel := callTimeout(ctx)
	defer cancel()
	args := map[string]any{"query": query}
	if j.cloud {
		args["cloudId"] = j.cloudID
		props := properties(j.users)
		args[firstNonEmpty(pick(props, "searchString", "query"), "query")] = query
	} else {
		args["project_key"] = j.project
		args["limit"] = 20
	}
	res, err := j.s.Call(ctx, j.users.Name, args)
	if err != nil {
		return nil, err
	}
	var out []Assignee
	for _, u := range records(res) {
		id := str(u, "accountId", "account_id", "key", "name", "username")
		if id == "" {
			continue
		}
		out = append(out, Assignee{Kind: "person", ID: id, Name: firstNonEmpty(str(u, "displayName", "display_name", "name"), id)})
	}
	return out, nil
}

// gh writes to GitHub Issues with the GitHub credential Speccy holds. An epic is an issue, an
// item a sub-issue of it, and a dependency a blocked-by relation.
type gh struct {
	c    *github.Client
	repo string
}

func (g *gh) close() {}

func (g *gh) check(ctx context.Context, _ bool) error {
	if _, err := g.c.Repo(ctx, g.repo); err != nil {
		return github.UnreadableRepo(g.repo, err)
	}
	return nil
}

func (g *gh) createEpic(ctx context.Context, title, body string) (issue, error) {
	is, err := g.c.CreateIssue(ctx, g.repo, title, body, nil)
	if err != nil {
		return issue{}, err
	}
	return ghIssue(g.repo, is), nil
}

func (g *gh) createItem(ctx context.Context, title, body string, epic issue, who *Assignee, blockedBy []issue) (issue, error) {
	var as []string
	if who != nil {
		as = []string{who.ID}
	}
	is, err := g.c.CreateIssue(ctx, g.repo, title, body, as)
	if err != nil {
		return issue{}, err
	}
	out := ghIssue(g.repo, is)
	if n := ghNumber(epic.Key); n > 0 {
		if err := g.c.AddSubIssue(ctx, g.repo, n, is.ID); err != nil {
			return out, err
		}
	}
	for _, b := range blockedBy {
		id, err := strconv.ParseInt(b.Ref, 10, 64)
		if err != nil {
			continue
		}
		if err := g.c.AddBlockedBy(ctx, g.repo, is.Number, id); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (g *gh) assignees(ctx context.Context, query string) ([]Assignee, error) {
	list, err := g.c.Assignees(ctx, g.repo)
	if err != nil {
		return nil, err
	}
	var out []Assignee
	q := strings.ToLower(strings.TrimSpace(query))
	for _, u := range list {
		if q != "" && !strings.Contains(strings.ToLower(u.Login), q) {
			continue
		}
		kind := "person"
		if u.Type == "Bot" {
			kind = "agent"
		}
		out = append(out, Assignee{Kind: kind, ID: u.Login, Name: u.Login})
	}
	return out, nil
}

func ghIssue(repo string, is github.Issue) issue {
	return issue{Key: fmt.Sprintf("%s#%d", repo, is.Number), URL: is.URL, Ref: strconv.FormatInt(is.ID, 10)}
}

func ghNumber(key string) int {
	_, n, ok := strings.Cut(key, "#")
	if !ok {
		return 0
	}
	v, _ := strconv.Atoi(n)
	return v
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
