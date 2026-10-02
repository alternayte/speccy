package action

import (
	"fmt"
	"strings"

	"github.com/alternayte/speccy/internal/source/github"
)

// PendingReview is the review that a reviewer posts from a laptop and sees alone, until that
// person submits it (#91). It holds the comments the Action would post inline, under the same
// rules: levels, relaxed checks, waivers and the inline limit. A finding on a line outside the
// diff goes into the body, because GitHub takes a comment only on a line of the diff.
type PendingReview struct {
	Repo     string                 `json:"repo"`
	Pull     int                    `json:"pull"`
	CommitID string                 `json:"commit_id"`
	Body     string                 `json:"body"`
	Comments []github.ReviewComment `json:"comments"`
}

// Pending builds the pending review of the bundles of a pull request. It posts nothing.
func Pending(o Options, bundles []Bundle, files []github.PRFile) PendingReview {
	if o.InlineLimit <= 0 {
		o.InlineLimit = DefaultInlineLimit
	}
	changed := map[string]map[int]bool{}
	for _, f := range files {
		changed[f.Filename] = ChangedLines(f.Patch)
	}
	out := PendingReview{Repo: o.Repo, Pull: o.PR, CommitID: o.HeadSHA, Comments: []github.ReviewComment{}}
	var body strings.Builder
	fmt.Fprintf(&body, "Speccy reviewed %d spec doc%s at %s.\n", len(bundles), plural(len(bundles)), short(o.HeadSHA))
	for _, b := range bundles {
		title := verdictText[b.Verdict]
		if b.Error != "" {
			title = "The review failed"
		}
		fmt.Fprintf(&body, "\n**%s**: %s. %s\n", b.Slug, title, bundleSummary(b))
		inline, rest := candidates(b, changed)
		for i, c := range inline {
			if i >= o.InlineLimit {
				if c.summary != "" {
					rest = append(rest, c)
				}
				continue
			}
			out.Comments = append(out.Comments, github.ReviewComment{Path: c.path, Line: c.line, Side: "RIGHT", Body: c.body})
		}
		if len(rest) > 0 {
			body.WriteString("\nOn lines that this pull request does not change, or past the inline limit:\n")
			for _, c := range rest {
				body.WriteString(c.summary + "\n")
			}
		}
	}
	out.Body = body.String()
	return out
}
