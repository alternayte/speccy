// Package github reaches GitHub through its REST API only (DEC-018: Speccy never runs git).
// It reads a repo tree for the GitHub source (REQ-123), and writes a branch, a commit, and a
// pull request to publish a draft.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultAPI is the GitHub API. GitHub Enterprise Server uses https://<host>/api/v3.
const DefaultAPI = "https://api.github.com"

// Client calls the GitHub API with a fine-grained personal access token (DEC-019).
type Client struct {
	API   string
	Token string
	HTTP  *http.Client
}

// Error is an answer from GitHub that is not a success.
type Error struct {
	Status  int
	Message string
	Path    string
}

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "GitHub refused the token. It may be expired or wrong. An admin can set a new one in Admin → GitHub"
	case http.StatusForbidden:
		return "the GitHub token has no access to this: " + e.Message + ". Give the token the permissions in the docs"
	case http.StatusNotFound:
		return "GitHub found nothing at " + e.Path + ". Check the repo and branch names, and that the token can read the repo"
	}
	return fmt.Sprintf("GitHub answered %d: %s", e.Status, e.Message)
}

// RepoPattern is owner/name.
var RepoPattern = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

func (c *Client) do(ctx context.Context, method, p string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	api := c.API
	if api == "" {
		api = DefaultAPI
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(api, "/")+p, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "speccy")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 200<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		return &Error{Status: res.StatusCode, Message: e.Message, Path: p}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// Head returns the commit at the tip of a branch and its tree.
func (c *Client) Head(ctx context.Context, repo, branch string) (commit, tree string, err error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(ctx, "GET", repoPath(repo)+"/git/ref/heads/"+escapeRef(branch), nil, &ref); err != nil {
		return "", "", err
	}
	var cm struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.do(ctx, "GET", repoPath(repo)+"/git/commits/"+ref.Object.SHA, nil, &cm); err != nil {
		return "", "", err
	}
	return ref.Object.SHA, cm.Tree.SHA, nil
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// Entry is one file in a tree.
type Entry struct {
	Path string `json:"path"`
	Type string `json:"type"` // blob, tree, or commit
	Mode string `json:"mode"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// Tree lists every file of a tree. GitHub cuts very large trees; truncated says so.
func (c *Client) Tree(ctx context.Context, repo, sha string) (entries []Entry, truncated bool, err error) {
	var t struct {
		Tree      []Entry `json:"tree"`
		Truncated bool    `json:"truncated"`
	}
	if err := c.do(ctx, "GET", repoPath(repo)+"/git/trees/"+sha+"?recursive=1", nil, &t); err != nil {
		return nil, false, err
	}
	return t.Tree, t.Truncated, nil
}

// Blob returns the content of a blob.
func (c *Client) Blob(ctx context.Context, repo, sha string) ([]byte, error) {
	var b struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.do(ctx, "GET", repoPath(repo)+"/git/blobs/"+sha, nil, &b); err != nil {
		return nil, err
	}
	if b.Encoding != "base64" {
		return []byte(b.Content), nil
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(b.Content, "\n", ""))
}

// Repo checks that the token can read the repo, and returns its default branch.
func (c *Client) Repo(ctx context.Context, repo string) (defaultBranch string, err error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, "GET", repoPath(repo), nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

// Change is one file of a commit: new content, or a delete when Content is nil.
type Change struct {
	Path    string
	Content []byte
}

// PullRequest is an opened pull request.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
}

// Publish commits changes on a new branch from base, and opens a pull request into target
// (REQ-123). It never changes target itself.
func (c *Client) Publish(ctx context.Context, repo, target, base, branch, message, title, body string, changes []Change) (PullRequest, error) {
	var baseCommit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.do(ctx, "GET", repoPath(repo)+"/git/commits/"+base, nil, &baseCommit); err != nil {
		return PullRequest{}, err
	}
	type treeEntry struct {
		Path string  `json:"path"`
		Mode string  `json:"mode"`
		Type string  `json:"type"`
		SHA  *string `json:"sha"`
	}
	entries := make([]treeEntry, 0, len(changes))
	for _, ch := range changes {
		e := treeEntry{Path: ch.Path, Mode: "100644", Type: "blob"}
		if ch.Content != nil {
			var blob struct {
				SHA string `json:"sha"`
			}
			if err := c.do(ctx, "POST", repoPath(repo)+"/git/blobs", map[string]string{
				"content": base64.StdEncoding.EncodeToString(ch.Content), "encoding": "base64",
			}, &blob); err != nil {
				return PullRequest{}, err
			}
			e.SHA = &blob.SHA
		}
		entries = append(entries, e)
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, "POST", repoPath(repo)+"/git/trees", map[string]any{"base_tree": baseCommit.Tree.SHA, "tree": entries}, &tree); err != nil {
		return PullRequest{}, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, "POST", repoPath(repo)+"/git/commits", map[string]any{"message": message, "tree": tree.SHA, "parents": []string{base}}, &commit); err != nil {
		return PullRequest{}, err
	}
	if err := c.do(ctx, "POST", repoPath(repo)+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": commit.SHA}, nil); err != nil {
		return PullRequest{}, err
	}
	var pr PullRequest
	err := c.do(ctx, "POST", repoPath(repo)+"/pulls", map[string]any{"title": title, "head": branch, "base": target, "body": body}, &pr)
	return pr, err
}

// User returns the login of the token's account: a check that GitHub accepts the token.
func (c *Client) User(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	err := c.do(ctx, "GET", "/user", nil, &u)
	return u.Login, err
}
