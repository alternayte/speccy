package action

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
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
	// File is a spec doc that the pull request changes. When the reviewer's pending review has
	// an empty body, which GitHub does not let anyone edit, Body goes on this file as a comment.
	File string `json:"file,omitempty"`

	// current holds the key of each finding of the bundles in reviewed: the folders of the
	// bundles whose review ran every stage and did not fail. A Speccy comment in one of those
	// folders whose key is not in current is about a finding that is gone.
	current  map[string]bool
	reviewed []string
}

// The part of a pending review's body that Speccy wrote sits between these markers, so a later
// batch replaces its own part and keeps what the reviewer wrote.
const (
	bodyStart = "<!-- speccy:body -->"
	bodyEnd   = "<!-- /speccy:body -->"
	askMarker = "<!-- speccy:ask:"
)

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
	for _, b := range bundles {
		if p := path.Join(b.Dir, b.MainDoc); b.MainDoc != "" && changed[p] != nil && out.File == "" {
			out.File = p
		}
	}
	out.current = map[string]bool{}
	for _, b := range bundles {
		if !o.Prune || b.Error != "" || b.Kind != "full" {
			continue
		}
		out.reviewed = append(out.reviewed, b.Dir)
		for _, k := range findingKeys(b) {
			out.current[k] = true
		}
	}
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
	out.Body = bodyStart + "\n" + body.String() + bodyEnd
	return out
}

// Merge returns what to change in the reviewer's pending review: the comments whose finding has
// no comment there yet, the new body, where Speccy's part replaces the part it wrote before,
// and Speccy's comments whose finding is gone. The reviewer's own comments and text, and the
// reviewer's questions, stay as they are.
func Merge(existing *github.PendingReview, pr PendingReview) (add []github.ReviewComment, body string, stale []github.PendingComment) {
	have := map[string]bool{}
	for _, c := range existing.Comments {
		k := keyIn(c.Body)
		if k == "" {
			continue
		}
		have[k] = true
		if !pr.current[k] && inAny(c.Path, pr.reviewed) {
			stale = append(stale, c)
		}
	}
	for _, c := range pr.Comments {
		if k := keyIn(c.Body); k != "" && have[k] {
			continue
		}
		add = append(add, c)
	}
	return add, replaceBlock(existing.Body, pr.Body), stale
}

// inAny reports whether the repo path p is in one of the folders.
func inAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if d == "." || d == "" || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// BodyComment is the comment that holds Speccy's part of the body, in a pending review whose
// body started empty, or nil.
func BodyComment(r *github.PendingReview) *github.PendingComment {
	for i, c := range r.Comments {
		if strings.Contains(c.Body, bodyStart) {
			return &r.Comments[i]
		}
	}
	return nil
}

// AskBody is the body of a pending review that a question of the reviewer starts. GitHub does
// not let anyone edit a review body that started empty, so it is never empty.
func AskBody() string {
	return bodyStart + "\nQuestions on the spec docs of this pull request.\n" + bodyEnd
}

// AskOnFile is a question with no line in the diff, as a comment on the whole file. where names
// the section.
func AskOnFile(where, question, id string) string {
	return fmt.Sprintf("**%s**: %s\n\n%s%s -->", where, question, askMarker, id)
}

// replaceBlock puts block in place of the Speccy part of body, or after body when it has none.
func replaceBlock(body, block string) string {
	if i := strings.Index(body, bodyStart); i >= 0 {
		if j := strings.Index(body[i:], bodyEnd); j >= 0 {
			return body[:i] + block + body[i+j+len(bodyEnd):]
		}
	}
	if strings.TrimSpace(body) == "" {
		return block
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block
}

// AskComment is the comment of a reviewer's question, with a marker that says Speccy wrote it
// and that the same question is not posted twice.
func AskComment(question, id string) string {
	return question + "\n\n" + askMarker + id + " -->"
}

// AskInBody adds a question that has no line in the diff to the body of a pending review. where
// names the doc and the section.
func AskInBody(body, where, question, id string) string {
	line := fmt.Sprintf("**%s**: %s %s%s -->", where, question, askMarker, id)
	if strings.TrimSpace(body) == "" {
		return line
	}
	return strings.TrimRight(body, "\n") + "\n\n" + line
}

// Asked reports whether the pending review already holds the question with this id.
func Asked(r *github.PendingReview, id string) bool {
	m := askMarker + id + " -->"
	if strings.Contains(r.Body, m) {
		return true
	}
	for _, c := range r.Comments {
		if strings.Contains(c.Body, m) {
			return true
		}
	}
	return false
}

// BySpeccy reports whether Speccy wrote a comment: a finding, a reviewer's question, or its
// part of the body.
func BySpeccy(body string) bool {
	return strings.Contains(body, keyMarker) || strings.Contains(body, askMarker) || strings.Contains(body, bodyStart)
}

// BundlesOf turns the review of a pull request's URL into the bundles that Pending and Run
// take. A comment needs the text of its line, so each file that a finding names is read at the
// commit the review read. checks gives the profile of a key.
func BundlesOf(ctx context.Context, gh *github.Client, rev *api.UrlReview, checks func(key string) (profile.Profile, bool), lint bool) ([]Bundle, error) {
	var bundles []Bundle
	for _, d := range rev.Docs {
		ab := Bundle{Slug: d.Slug, Dir: d.Dir, Files: map[string][]byte{}}
		if d.Review == nil {
			if d.Error != nil {
				ab.Error = *d.Error
			}
			bundles = append(bundles, ab)
			continue
		}
		v := d.Review
		ab.MainDoc, ab.Profile, ab.Kind = v.MainDoc, v.ProfileKey, "full"
		if lint {
			ab.Kind = "lint"
		}
		ab.Verdict, ab.Score, ab.Must, ab.Should = string(v.Verdict.Result), v.Verdict.Score, v.Verdict.Must, v.Verdict.Should
		ab.Waivers, ab.Relaxed, ab.Findings = v.Verdict.WaiverCount, v.Verdict.RelaxedCount, v.Findings
		for _, f := range append([]api.Finding{{Anchor: api.Anchor{File: v.MainDoc}}}, v.Findings...) {
			if _, done := ab.Files[f.Anchor.File]; done || f.Anchor.File == "" {
				continue
			}
			content, _, err := gh.FileAt(ctx, rev.Repo, rev.Commit, path.Join(d.Dir, f.Anchor.File))
			if err != nil {
				return nil, fmt.Errorf("%s does not read: %w", path.Join(d.Dir, f.Anchor.File), err)
			}
			ab.Files[f.Anchor.File] = content
		}
		if p, ok := checks(v.ProfileKey); ok {
			ab.Prefixes, ab.CoverPrefixes, ab.Checks = p.Trace.Prefixes, p.Trace.Cover, p
		}
		bundles = append(bundles, ab)
	}
	return bundles, nil
}
