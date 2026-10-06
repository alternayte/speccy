package github

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Pull is one open pull request of a repo.
type Pull struct {
	Number  int
	Draft   bool
	HeadSHA string
	URL     string
	// UpdatedAt is the last change to the pull request: a push, a comment or an edit.
	UpdatedAt time.Time
}

// OpenPulls lists the open pull requests of a repo.
func (c *Client) OpenPulls(ctx context.Context, repo string) ([]Pull, error) {
	var out []Pull
	for page := 1; ; page++ {
		var batch []struct {
			Number    int       `json:"number"`
			Draft     bool      `json:"draft"`
			HTMLURL   string    `json:"html_url"`
			UpdatedAt time.Time `json:"updated_at"`
			Head      struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		if err := c.do(ctx, "GET", fmt.Sprintf("%s/pulls?state=open&per_page=100&page=%d", repoPath(repo), page), nil, &batch); err != nil {
			return nil, err
		}
		for _, p := range batch {
			out = append(out, Pull{Number: p.Number, Draft: p.Draft, HeadSHA: p.Head.SHA, URL: p.HTMLURL, UpdatedAt: p.UpdatedAt})
		}
		if len(batch) < 100 || page >= 30 {
			return out, nil
		}
	}
}

// RequestedPulls lists the numbers of the open pull requests of a repo that are not drafts and
// ask the user of the token for a review, directly or through a team.
func (c *Client) RequestedPulls(ctx context.Context, repo string) ([]int, error) {
	q := url.QueryEscape("repo:" + repo + " is:pr is:open draft:false review-requested:@me")
	var out []int
	for page := 1; ; page++ {
		var res struct {
			Items []struct {
				Number int `json:"number"`
			} `json:"items"`
		}
		if err := c.do(ctx, "GET", fmt.Sprintf("/search/issues?q=%s&per_page=100&page=%d", q, page), nil, &res); err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			out = append(out, it.Number)
		}
		if len(res.Items) < 100 || page >= 10 {
			return out, nil
		}
	}
}

// PendingReview is the pending review of the user of the token on one pull request. Only its
// author sees it, until that person submits it.
type PendingReview struct {
	ID       string // the GraphQL node ID
	URL      string
	Body     string
	Comments []PendingComment
}

// PendingComment is one comment of a pending review. ID is its GraphQL node ID, DatabaseID the
// number a person names it by.
type PendingComment struct {
	ID         string
	DatabaseID int64
	Path       string
	Line       int
	Body       string
}

type pendingComments struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []struct {
		ID         string `json:"id"`
		DatabaseID int64  `json:"databaseId"`
		Path       string `json:"path"`
		Line       *int   `json:"line"`
		Original   *int   `json:"originalLine"`
		Body       string `json:"body"`
	} `json:"nodes"`
}

func (r *PendingReview) add(page pendingComments) {
	for _, n := range page.Nodes {
		line := 0
		if n.Line != nil {
			line = *n.Line
		} else if n.Original != nil {
			line = *n.Original
		}
		r.Comments = append(r.Comments, PendingComment{ID: n.ID, DatabaseID: n.DatabaseID, Path: n.Path, Line: line, Body: n.Body})
	}
}

// Pending returns the pending review of the user of the token on a pull request, or nil when
// that person has none. GitHub allows one pending review per person on a pull request.
func (c *Client) Pending(ctx context.Context, repo string, number int) (*PendingReview, error) {
	owner, name := splitRepo(repo)
	const fields = `pageInfo{hasNextPage endCursor} nodes{id databaseId path line originalLine body}`
	q := `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){pullRequest(number:$n){` +
		`reviews(states:[PENDING],first:20){nodes{id url body viewerDidAuthor comments(first:100){` + fields + `}}}}}}`
	more := `query($id:ID!,$after:String){node(id:$id){... on PullRequestReview{comments(first:100,after:$after){` + fields + `}}}}`
	var data struct {
		Repository struct {
			PullRequest *struct {
				Reviews struct {
					Nodes []struct {
						ID       string          `json:"id"`
						URL      string          `json:"url"`
						Body     string          `json:"body"`
						Mine     bool            `json:"viewerDidAuthor"`
						Comments pendingComments `json:"comments"`
					} `json:"nodes"`
				} `json:"reviews"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, q, map[string]any{"owner": owner, "name": name, "n": number}, &data); err != nil {
		return nil, err
	}
	if data.Repository.PullRequest == nil {
		return nil, fmt.Errorf("%s has no pull request %d", repo, number)
	}
	for _, r := range data.Repository.PullRequest.Reviews.Nodes {
		if !r.Mine {
			continue
		}
		out := &PendingReview{ID: r.ID, URL: r.URL, Body: r.Body}
		out.add(r.Comments)
		for page := r.Comments.PageInfo; page.HasNextPage; {
			var rest struct {
				Node struct {
					Comments pendingComments `json:"comments"`
				} `json:"node"`
			}
			if err := c.graphql(ctx, more, map[string]any{"id": r.ID, "after": page.EndCursor}, &rest); err != nil {
				return nil, err
			}
			out.add(rest.Node.Comments)
			page = rest.Node.Comments.PageInfo
		}
		return out, nil
	}
	return nil, nil
}

// threadAdded is the answer of addPullRequestReviewThread: the node ID of the new comment.
type threadAdded struct {
	Add struct {
		Thread struct {
			Comments struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"thread"`
	} `json:"addPullRequestReviewThread"`
}

func (t threadAdded) id() string {
	if n := t.Add.Thread.Comments.Nodes; len(n) > 0 {
		return n[0].ID
	}
	return ""
}

// AddPendingComment adds a comment on a line of the new side of the diff to a pending review,
// and returns the node ID of the comment.
func (c *Client) AddPendingComment(ctx context.Context, reviewID, path string, line int, body string) (string, error) {
	var data threadAdded
	q := `mutation($id:ID!,$path:String!,$line:Int!,$body:String!){addPullRequestReviewThread(input:{pullRequestReviewId:$id,path:$path,line:$line,side:RIGHT,body:$body}){thread{id comments(first:1){nodes{id}}}}}`
	err := c.graphql(ctx, q, map[string]any{"id": reviewID, "path": path, "line": line, "body": body}, &data)
	return data.id(), err
}

// SetPendingBody replaces the body of a pending review.
func (c *Client) SetPendingBody(ctx context.Context, reviewID, body string) error {
	var data any
	q := `mutation($id:ID!,$body:String!){updatePullRequestReview(input:{pullRequestReviewId:$id,body:$body}){pullRequestReview{id}}}`
	return c.graphql(ctx, q, map[string]any{"id": reviewID, "body": body}, &data)
}

// DeletePendingComment removes one comment of a pending review.
func (c *Client) DeletePendingComment(ctx context.Context, commentID string) error {
	var data any
	q := `mutation($id:ID!){deletePullRequestReviewComment(input:{id:$id}){pullRequestReview{id}}}`
	return c.graphql(ctx, q, map[string]any{"id": commentID}, &data)
}

// DiscardPending removes a pending review and its comments.
func (c *Client) DiscardPending(ctx context.Context, reviewID string) error {
	var data any
	q := `mutation($id:ID!){deletePullRequestReview(input:{pullRequestReviewId:$id}){pullRequestReview{id}}}`
	return c.graphql(ctx, q, map[string]any{"id": reviewID}, &data)
}

// AddPendingFileComment adds a comment on a whole file of the diff to a pending review. It holds
// what has no line in the diff when the review's body cannot change: GitHub refuses to edit a
// review body that started empty. It returns the node ID of the comment.
func (c *Client) AddPendingFileComment(ctx context.Context, reviewID, path, body string) (string, error) {
	var data threadAdded
	q := `mutation($id:ID!,$path:String!,$body:String!){addPullRequestReviewThread(input:{pullRequestReviewId:$id,path:$path,subjectType:FILE,body:$body}){thread{id comments(first:1){nodes{id}}}}}`
	err := c.graphql(ctx, q, map[string]any{"id": reviewID, "path": path, "body": body}, &data)
	return data.id(), err
}

// UpdatePendingComment replaces the text of one comment of a pending review.
func (c *Client) UpdatePendingComment(ctx context.Context, commentID, body string) error {
	var data any
	q := `mutation($id:ID!,$body:String!){updatePullRequestReviewComment(input:{pullRequestReviewCommentId:$id,body:$body}){pullRequestReviewComment{id}}}`
	return c.graphql(ctx, q, map[string]any{"id": commentID, "body": body}, &data)
}
