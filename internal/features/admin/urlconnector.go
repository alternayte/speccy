package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// maxPageBytes is the most a connector reads of one page. The grounding stage leaves out a
// page over 200,000 characters, so a larger body is not read to its end.
const maxPageBytes = 1 << 20

// urlConnectorAPI is a connector as the API gives it.
func urlConnectorAPI(c pgdb.UrlConnector) api.URLConnector {
	profiles := []string{}
	_ = json.Unmarshal(c.Profiles, &profiles)
	kind := api.URLConnectorKindWeb
	if isGitHubAddress(c.Address) {
		kind = api.URLConnectorKindGithub
	}
	return api.URLConnector{Id: c.ID, Name: c.Name, Address: c.Address, BaseUrl: c.BaseUrl, Kind: kind,
		HeaderName: c.HeaderName, HasHeader: len(c.HeaderEncrypted) > 0, HeaderLast4: c.HeaderLast4, Profiles: profiles}
}

// isGitHubAddress says whether a connector address is a file in a GitHub repo, which the
// GitHub credential reads. A Pages site of a private repo takes no token, so its repo is read.
func isGitHubAddress(addr string) bool {
	ref, err := github.ParseURL(addr)
	return err == nil && ref.File && ref.Branch != "" && strings.Contains(addr, "/blob/")
}

// ListURLConnectors lists the URL connectors (docs/specs/url-connectors.md).
func (a *API) ListURLConnectors(ctx context.Context, _ api.ListURLConnectorsRequestObject) (api.ListURLConnectorsResponseObject, error) {
	rows, err := a.DB.Queries().ListURLConnectors(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	out := api.ListURLConnectors200JSONResponse{Items: []api.URLConnector{}}
	for _, r := range rows {
		out.Items = append(out.Items, urlConnectorAPI(r))
	}
	return out, nil
}

// CreateURLConnector adds a URL connector, after it read its index.
func (a *API) CreateURLConnector(ctx context.Context, req api.CreateURLConnectorRequestObject) (api.CreateURLConnectorResponseObject, error) {
	c, links, err := a.saveURLConnector(ctx, kernel.NewID(), *req.Body, nil)
	if err != nil {
		return nil, err
	}
	out := urlConnectorAPI(c)
	out.Links = &links
	return api.CreateURLConnector201JSONResponse(out), nil
}

// UpdateURLConnector changes a URL connector. A header value left out keeps the stored one.
func (a *API) UpdateURLConnector(ctx context.Context, req api.UpdateURLConnectorRequestObject) (api.UpdateURLConnectorResponseObject, error) {
	cur, err := a.urlConnector(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	c, links, err := a.saveURLConnector(ctx, cur.ID, *req.Body, &cur)
	if err != nil {
		return nil, err
	}
	out := urlConnectorAPI(c)
	out.Links = &links
	return api.UpdateURLConnector200JSONResponse(out), nil
}

// DeleteURLConnector deletes a URL connector.
func (a *API) DeleteURLConnector(ctx context.Context, req api.DeleteURLConnectorRequestObject) (api.DeleteURLConnectorResponseObject, error) {
	if _, err := a.urlConnector(ctx, req.ConnectionId); err != nil {
		return nil, err
	}
	if err := a.DB.Queries().DeleteURLConnector(ctx, pgdb.DeleteURLConnectorParams{WorkspaceID: a.Workspace, ID: req.ConnectionId}); err != nil {
		return nil, err
	}
	return api.DeleteURLConnector204Response{}, nil
}

func (a *API) urlConnector(ctx context.Context, id uuid.UUID) (pgdb.UrlConnector, error) {
	c, err := a.DB.Queries().GetURLConnector(ctx, pgdb.GetURLConnectorParams{WorkspaceID: a.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return c, kernel.NotFound("connector_not_found", "No URL connector has the ID %s.", id)
	}
	return c, err
}

// defaultBase is the base URL of a web index: its folder.
func defaultBase(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	dir := path.Dir(u.Path)
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	u.Path = dir
	return u.String()
}

func (a *API) saveURLConnector(ctx context.Context, id uuid.UUID, in api.URLConnectorInput, existing *pgdb.UrlConnector) (pgdb.UrlConnector, int, error) {
	name := strings.TrimSpace(in.Name)
	addr := strings.TrimSpace(in.Address)
	if name == "" {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "Give the connector a name.")
	}
	if !strings.HasPrefix(addr, "https://") && !strings.HasPrefix(addr, "http://") {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "The address must start with https:// or http://.")
	}
	gh := isGitHubAddress(addr)
	if !gh && strings.Contains(addr, "github.com/") && !strings.Contains(addr, "/blob/") {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "A GitHub address names one file at a branch, such as https://github.com/acme/platform/blob/gh-pages/llms.txt.")
	}
	base := strings.TrimSpace(deref(in.BaseUrl))
	switch {
	case base == "" && gh:
		base = defaultBase(addr) // relative links of the index only
	case base == "":
		base = defaultBase(addr)
	case !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://"):
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "The base URL must start with https:// or http://.")
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	header := strings.TrimSpace(deref(in.HeaderName))
	if gh && header != "" {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "A GitHub address takes no header: Speccy reads it with its GitHub credential.")
	}
	if header != "" && strings.ContainsAny(header, " :\r\n") {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "%q is not a header name.", header)
	}
	sealed, last4 := []byte(nil), ""
	if existing != nil && header != "" {
		sealed, last4 = existing.HeaderEncrypted, existing.HeaderLast4
	}
	if v := strings.TrimSpace(deref(in.HeaderValue)); v != "" {
		if header == "" {
			return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "Name the header that takes the value, such as Authorization.")
		}
		var err error
		if sealed, last4, err = a.seal(v); err != nil {
			return pgdb.UrlConnector{}, 0, err
		}
	}
	if header != "" && len(sealed) == 0 {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("bad_connector", "Give the value of the %s header.", header)
	}
	profiles := []string{}
	for _, p := range deref(in.Profiles) {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(profiles, p) {
			profiles = append(profiles, p)
		}
	}
	row := pgdb.UrlConnector{ID: id, WorkspaceID: a.Workspace, Name: name, Address: addr, BaseUrl: base, HeaderName: header,
		HeaderEncrypted: sealed, HeaderLast4: last4}
	profilesJSON, _ := json.Marshal(profiles)
	row.Profiles = dbtype.JSON(profilesJSON)
	// The index is read before the connector is saved: an address Speccy cannot read is a
	// mistake to fix now, not a run note on every review.
	c, err := a.connectorOf(ctx, row)
	if err != nil {
		return pgdb.UrlConnector{}, 0, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	index, err := c.Fetch(readCtx, c.Index())
	if err != nil {
		return pgdb.UrlConnector{}, 0, kernel.Invalid("connector_unreadable", "Speccy could not read %s: %s.", addr, strings.TrimSuffix(err.Error(), "."))
	}
	links := review.IndexLinkCount(c, index)
	q := a.DB.Queries()
	if existing == nil {
		err = q.InsertURLConnector(ctx, pgdb.InsertURLConnectorParams{ID: id, WorkspaceID: a.Workspace, Name: name, Address: addr,
			BaseUrl: base, HeaderName: header, HeaderEncrypted: sealed, HeaderLast4: last4, Profiles: row.Profiles, CreatedAt: time.Now().UTC()})
	} else {
		err = q.UpdateURLConnector(ctx, pgdb.UpdateURLConnectorParams{ID: id, WorkspaceID: a.Workspace, Name: name, Address: addr,
			BaseUrl: base, HeaderName: header, HeaderEncrypted: sealed, HeaderLast4: last4, Profiles: row.Profiles})
	}
	if err != nil {
		if isUnique(err) {
			return pgdb.UrlConnector{}, 0, kernel.Conflict("name_taken", "A connector named %q exists. Choose another name.", name)
		}
		return pgdb.UrlConnector{}, 0, err
	}
	saved, err := q.GetURLConnector(ctx, pgdb.GetURLConnectorParams{WorkspaceID: a.Workspace, ID: id})
	return saved, links, err
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// Connectors returns the URL connectors of the workspace for the review
// (docs/specs/url-connectors.md).
func (a *API) Connectors(ctx context.Context) ([]review.Connector, error) {
	rows, err := a.DB.Queries().ListURLConnectors(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]review.Connector, 0, len(rows))
	for _, r := range rows {
		c, err := a.connectorOf(ctx, r)
		if err != nil {
			// A connector that cannot open, such as one whose header was sealed with another
			// key, reads as a failed fetch: a run note, and no failed review.
			profiles := []string{}
			_ = json.Unmarshal(r.Profiles, &profiles)
			c = &brokenConn{urlConn: urlConn{name: r.Name, index: r.Address, base: r.BaseUrl, profiles: profiles}, err: err}
		}
		out = append(out, c)
	}
	return out, nil
}

// brokenConn is a connector that could not open. Each fetch gives the reason.
type brokenConn struct {
	urlConn
	err error
}

func (c *brokenConn) Fetch(context.Context, string) (string, error) { return "", c.err }

// connectorOf turns a row into the reader of its kind: the web, with its header, or a GitHub
// repo, with the GitHub credential.
func (a *API) connectorOf(ctx context.Context, r pgdb.UrlConnector) (review.Connector, error) {
	profiles := []string{}
	_ = json.Unmarshal(r.Profiles, &profiles)
	base := urlConn{name: r.Name, index: r.Address, base: r.BaseUrl, profiles: profiles}
	if isGitHubAddress(r.Address) {
		ref, _ := github.ParseURL(r.Address)
		if a.ConnectorGitHub == nil {
			return nil, kernel.Invalid("github_unavailable", "Speccy has no way to reach GitHub here, so it cannot read %s.", r.Name)
		}
		c, err := a.ConnectorGitHub(ctx, ref.APIURL())
		if err != nil {
			return nil, err
		}
		if ref, err = c.ResolveBranch(ctx, ref); err != nil {
			return nil, err
		}
		return &githubConn{urlConn: base, gh: c, ref: ref}, nil
	}
	w := &webConn{urlConn: base, header: r.HeaderName, client: a.connectorHTTP()}
	if len(r.HeaderEncrypted) > 0 {
		plain, err := a.Sealer.Open(r.HeaderEncrypted)
		if errors.Is(err, kernel.ErrOtherKey) {
			return nil, kernel.OtherKey(fmt.Sprintf("The header of the %s connector", r.Name), "Admin → Connectors")
		}
		if err != nil {
			return nil, err
		}
		w.value = string(plain)
	}
	return w, nil
}

func (a *API) connectorHTTP() *http.Client {
	if a.ConnectorHTTP != nil {
		return a.ConnectorHTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// urlConn is what both kinds of connector share.
type urlConn struct {
	name, index, base string
	profiles          []string
}

func (c urlConn) Name() string       { return c.name }
func (c urlConn) Index() string      { return c.index }
func (c urlConn) Owns(u string) bool { return review.Under(u, c.base) }
func (c urlConn) Applies(profile string) bool {
	return len(c.profiles) == 0 || slices.Contains(c.profiles, profile)
}

// webConn reads pages with a GET. Its header goes only to the base URL's host, and a redirect
// to another host does not take it.
type webConn struct {
	urlConn
	header, value string
	client        *http.Client
}

func (c *webConn) Fetch(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9, */*;q=0.5")
	baseURL, _ := url.Parse(c.base)
	if c.header != "" && baseURL != nil && strings.EqualFold(req.URL.Host, baseURL.Host) {
		req.Header.Set(c.header, c.value)
	}
	client := *c.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if baseURL == nil || !strings.EqualFold(r.URL.Host, baseURL.Host) {
			r.Header.Del(c.header)
		}
		return nil
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", target, res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxPageBytes))
	return string(body), err
}

// githubConn reads the index and its pages from a branch of a repo. A link under the base URL
// maps to the file at the same path below the branch root; a relative link of the index maps
// to the file next to it.
type githubConn struct {
	urlConn
	gh  *github.Client
	ref github.Ref
}

// Owns takes the links under the site's base URL, and the relative links of the index, which
// resolve against its GitHub address.
func (c *githubConn) Owns(u string) bool {
	return review.Under(u, c.base) || review.Under(u, strings.TrimSuffix(c.index, path.Base(c.ref.Path)))
}

func (c *githubConn) Fetch(ctx context.Context, target string) (string, error) {
	p, ok := c.repoPath(target)
	if !ok {
		return "", fmt.Errorf("%s is not under %s", target, c.base)
	}
	for _, candidate := range pageFiles(p) {
		content, found, err := c.gh.FileAt(ctx, c.ref.Repo, c.ref.Branch, candidate)
		if err != nil {
			return "", github.UnreadableRepo(c.ref.Repo, err)
		}
		if found {
			if len(content) > maxPageBytes {
				content = content[:maxPageBytes]
			}
			return string(content), nil
		}
	}
	return "", fmt.Errorf("%s has no file %s on %s", c.ref.Repo, p, c.ref.Branch)
}

// repoPath maps an address to a file path of the branch.
func (c *githubConn) repoPath(target string) (string, bool) {
	if target == c.index {
		return c.ref.Path, true
	}
	// A relative link of the index resolves against the index's own GitHub address, so it is
	// a blob URL of the same branch.
	prefix := strings.TrimSuffix(c.index, path.Base(c.ref.Path))
	if rest, ok := strings.CutPrefix(target, prefix); ok {
		return path.Join(path.Dir(c.ref.Path), rest), true
	}
	if rest, ok := strings.CutPrefix(target, c.base); ok && !strings.Contains(c.base, "github.com/") {
		return path.Clean(rest), true
	}
	return "", false
}

// pageFiles are the files a site path can be: the path itself, then the markdown copy of a
// page, then the index of a folder.
func pageFiles(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		return []string{"index.md", "README.md"}
	}
	if strings.HasSuffix(p, "/") {
		return []string{p + "index.md", p + "README.md"}
	}
	out := []string{p}
	if path.Ext(p) == "" {
		out = append(out, p+".md", p+"/index.md")
	}
	if strings.HasSuffix(p, ".html") {
		out = append(out, strings.TrimSuffix(p, ".html")+".md", p+".md")
	}
	return out
}
