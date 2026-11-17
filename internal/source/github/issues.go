package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Issue is one issue a push created.
type Issue struct {
	Number int    `json:"number"`
	ID     int64  `json:"id"`
	URL    string `json:"html_url"`
}

// CreateIssue opens an issue in repo.
func (c *Client) CreateIssue(ctx context.Context, repo, title, body string, assignees []string) (Issue, error) {
	in := map[string]any{"title": title, "body": body}
	if len(assignees) > 0 {
		in["assignees"] = assignees
	}
	var out Issue
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues", repo), in, &out)
	return out, err
}

// AddSubIssue makes the issue with ID child a sub-issue of issue parent in repo. The child is
// named by its ID, not its number, because it may live in another repo.
func (c *Client) AddSubIssue(ctx context.Context, repo string, parent int, child int64) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/sub_issues", repo, parent),
		map[string]any{"sub_issue_id": child}, nil)
}

// AddBlockedBy records that issue blocked in repo is blocked by the issue with ID blocker.
func (c *Client) AddBlockedBy(ctx context.Context, repo string, blocked int, blocker int64) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/dependencies/blocked_by", repo, blocked),
		map[string]any{"issue_id": blocker}, nil)
}

// Assignee is one account an issue in a repo can be assigned to. A Bot is an agent, such as
// the Copilot coding agent.
type Assignee struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Type  string `json:"type"`
}

// Assignees lists the accounts an issue in repo can be assigned to.
func (c *Client) Assignees(ctx context.Context, repo string) ([]Assignee, error) {
	var all []Assignee
	for page := 1; page <= 10; page++ {
		var out []Assignee
		q := url.Values{"per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/assignees?%s", repo, q.Encode()), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < 100 {
			break
		}
	}
	return all, nil
}
