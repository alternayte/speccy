package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// PRFile is one file that a pull request changes. Patch is the unified diff, which GitHub
// leaves out for large or binary files.
type PRFile struct {
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Patch    string `json:"patch"`
}

// PRFiles lists the files of a pull request.
func (c *Client) PRFiles(ctx context.Context, repo string, number int) ([]PRFile, error) {
	var out []PRFile
	for page := 1; ; page++ {
		var batch []PRFile
		if err := c.do(ctx, "GET", fmt.Sprintf("%s/pulls/%d/files?per_page=100&page=%d", repoPath(repo), number, page), nil, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < 100 || page >= 30 {
			return out, nil
		}
	}
}

// IssueComment is a comment on a pull request's conversation.
type IssueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// IssueComments lists the conversation comments of an issue or pull request.
func (c *Client) IssueComments(ctx context.Context, repo string, number int) ([]IssueComment, error) {
	var out []IssueComment
	for page := 1; ; page++ {
		var batch []IssueComment
		if err := c.do(ctx, "GET", fmt.Sprintf("%s/issues/%d/comments?per_page=100&page=%d", repoPath(repo), number, page), nil, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < 100 || page >= 30 {
			return out, nil
		}
	}
}

// CreateIssueComment adds a conversation comment.
func (c *Client) CreateIssueComment(ctx context.Context, repo string, number int, body string) error {
	return c.do(ctx, "POST", fmt.Sprintf("%s/issues/%d/comments", repoPath(repo), number), map[string]string{"body": body}, nil)
}

// UpdateIssueComment replaces a conversation comment's text.
func (c *Client) UpdateIssueComment(ctx context.Context, repo string, id int64, body string) error {
	return c.do(ctx, "PATCH", repoPath(repo)+"/issues/comments/"+strconv.FormatInt(id, 10), map[string]string{"body": body}, nil)
}

// ReviewComment is one inline comment of a review, on a line of the new side of the diff.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// CreateReview posts a review with inline comments on a commit of a pull request.
func (c *Client) CreateReview(ctx context.Context, repo string, number int, commit, body string, comments []ReviewComment) error {
	return c.do(ctx, "POST", fmt.Sprintf("%s/pulls/%d/reviews", repoPath(repo), number), map[string]any{
		"commit_id": commit, "event": "COMMENT", "body": body, "comments": comments,
	}, nil)
}

// CheckRun is the result of one check on a commit.
type CheckRun struct {
	Name       string
	HeadSHA    string
	Conclusion string // success, neutral, or failure
	Title      string
	Summary    string
	DetailsURL string
}

// CreateCheckRun sets a completed check run on a commit.
func (c *Client) CreateCheckRun(ctx context.Context, repo string, r CheckRun) error {
	body := map[string]any{"name": r.Name, "head_sha": r.HeadSHA, "status": "completed", "conclusion": r.Conclusion,
		"output": map[string]string{"title": r.Title, "summary": r.Summary}}
	if r.DetailsURL != "" {
		body["details_url"] = r.DetailsURL
	}
	return c.do(ctx, "POST", repoPath(repo)+"/check-runs", body, nil)
}

// Thread is a review thread of a pull request, with the text of its first comment.
type Thread struct {
	ID       string
	Resolved bool
	Body     string
}

// graphql runs a GraphQL query. GitHub serves it at /graphql, or /api/graphql on GHES.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	var res struct {
		Data   any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	res.Data = out
	gc := *c
	// GHES: the REST API at https://host/api/v3 has GraphQL at https://host/api/graphql.
	gc.API = strings.TrimSuffix(strings.TrimRight(c.API, "/"), "/v3")
	if err := gc.do(ctx, "POST", "/graphql", map[string]any{"query": query, "variables": vars}, &res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		return fmt.Errorf("GitHub GraphQL: %s", res.Errors[0].Message)
	}
	return nil
}

// ReviewThreads lists the review threads of a pull request (the first 100).
func (c *Client) ReviewThreads(ctx context.Context, repo string, number int) ([]Thread, error) {
	owner, name := splitRepo(repo)
	var data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments   struct {
							Nodes []struct {
								Body string `json:"body"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	q := `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){pullRequest(number:$n){reviewThreads(first:100){nodes{id isResolved comments(first:1){nodes{body}}}}}}}`
	if err := c.graphql(ctx, q, map[string]any{"owner": owner, "name": name, "n": number}, &data); err != nil {
		return nil, err
	}
	var out []Thread
	for _, t := range data.Repository.PullRequest.ReviewThreads.Nodes {
		th := Thread{ID: t.ID, Resolved: t.IsResolved}
		if len(t.Comments.Nodes) > 0 {
			th.Body = t.Comments.Nodes[0].Body
		}
		out = append(out, th)
	}
	return out, nil
}

// ResolveThread marks a review thread resolved.
func (c *Client) ResolveThread(ctx context.Context, id string) error {
	var data any
	return c.graphql(ctx, `mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{id}}}`, map[string]any{"id": id}, &data)
}

func splitRepo(repo string) (string, string) {
	owner, name, _ := strings.Cut(repo, "/")
	return owner, name
}
