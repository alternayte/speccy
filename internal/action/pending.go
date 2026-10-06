package action

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
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
	// Config is the .speccy.yaml that the review used.
	Config *api.UrlReviewConfig `json:"config,omitempty"`
	// Keys are the finding keys of Comments, in their order. A plain comment has no marker, so
	// its key is only here.
	Keys []string `json:"-"`

	// current holds the key of each finding of the bundles in reviewed: the folders of the
	// bundles whose review ran every stage and did not fail. A Speccy comment in one of those
	// folders whose key is not in current is about a finding that is gone.
	current  map[string]bool
	reviewed []string
	plain    bool
}

// Plain reports whether the review names no tool: it has no heading, no catalog link and no
// marker, so the local state must keep its marks.
func (p PendingReview) Plain() bool { return p.plain }

// key is the finding key of the comment at i.
func (p PendingReview) key(i int) string {
	if i < len(p.Keys) && p.Keys[i] != "" {
		return p.Keys[i]
	}
	return keyIn(p.Comments[i].Body)
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
	out := PendingReview{Repo: o.Repo, Pull: o.PR, CommitID: o.HeadSHA, Comments: []github.ReviewComment{}, plain: o.Unattributed}
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
	who := "Speccy reviewed"
	if o.Unattributed {
		who = "Reviewed"
	}
	fmt.Fprintf(&body, "%s %d spec doc%s at %s.\n", who, len(bundles), plural(len(bundles)), short(o.HeadSHA))
	for _, b := range bundles {
		title := verdictText[b.Verdict]
		if b.Error != "" {
			title = "The review failed"
		}
		fmt.Fprintf(&body, "\n**%s**: %s. %s\n", b.Slug, title, bundleSummary(b))
		inline, rest := candidates(b, changed, o.Levels, o.Unattributed)
		for i, c := range inline {
			if i >= o.InlineLimit {
				if c.summary != "" {
					rest = append(rest, c)
				}
				continue
			}
			out.Comments = append(out.Comments, github.ReviewComment{Path: c.path, Line: c.line, Side: "RIGHT", Body: c.body})
			out.Keys = append(out.Keys, c.key)
		}
		if len(rest) > 0 {
			body.WriteString("\nOn lines that this pull request does not change, or past the inline limit:\n")
			for _, c := range rest {
				body.WriteString(c.summary + "\n")
			}
		}
	}
	out.Body = body.String()
	if !o.Unattributed {
		out.Body = bodyStart + "\n" + out.Body + bodyEnd
	}
	return out
}

// Marks are what the local state keeps of a pending review that names no tool, where no
// marker says which comments Speccy wrote.
type Marks []api.PendingMark

func (m Marks) find(id string, kind api.PendingMarkKind) (api.PendingMark, bool) {
	for _, x := range m {
		if x.CommentId == id && x.Kind == kind {
			return x, true
		}
	}
	return api.PendingMark{}, false
}

// has reports whether a mark names the comment or the review id.
func (m Marks) has(id string) bool {
	for _, x := range m {
		if x.CommentId == id {
			return true
		}
	}
	return false
}

// findingKey is the finding key of a comment: from its marker, or from the local state.
func (m Marks) findingKey(c github.PendingComment) string {
	if k := keyIn(c.Body); k != "" {
		return k
	}
	if x, ok := m.find(c.ID, api.PendingMarkKindFinding); ok {
		return x.Key
	}
	return ""
}

// Posted returns the marks of a pending review that Speccy made from pr: the review with its
// body, and each comment of pr that r holds on the same file and line with the same text.
func Posted(r *github.PendingReview, pr PendingReview) Marks {
	out := Marks{{CommentId: r.ID, Kind: api.PendingMarkKindReview, Body: pr.Body}}
	return append(out, Added(r, pr.Comments, pr.Keys, api.PendingMarkKindFinding)...)
}

// Added returns the marks of the comments that Speccy added to the pending review r, each of
// kind with its key from keys.
func Added(r *github.PendingReview, comments []github.ReviewComment, keys []string, kind api.PendingMarkKind) Marks {
	var out Marks
	used := map[string]bool{}
	for i, c := range comments {
		for _, x := range r.Comments {
			if used[x.ID] || x.Path != c.Path || x.Line != c.Line || strings.TrimSpace(x.Body) != strings.TrimSpace(c.Body) {
				continue
			}
			used[x.ID] = true
			k := ""
			if i < len(keys) {
				k = keys[i]
			}
			out = append(out, api.PendingMark{CommentId: x.ID, Kind: kind, Key: k})
			break
		}
	}
	return out
}

// Merge returns what to change in the reviewer's pending review: the comments whose finding has
// no comment there yet, with their keys, the new body, where Speccy's part replaces the part it
// wrote before, and Speccy's comments whose finding is gone. The reviewer's own comments and
// text, and the reviewer's questions, stay as they are. marks are what the local state keeps of
// the comments that carry no marker.
func Merge(existing *github.PendingReview, pr PendingReview, marks Marks) (add []github.ReviewComment, keys []string, body string, stale []github.PendingComment) {
	have := map[string]bool{}
	for _, c := range existing.Comments {
		k := marks.findingKey(c)
		if k == "" {
			continue
		}
		have[k] = true
		if !pr.current[k] && inAny(c.Path, pr.reviewed) {
			stale = append(stale, c)
		}
	}
	for i, c := range pr.Comments {
		if k := pr.key(i); k != "" && have[k] {
			continue
		}
		add = append(add, c)
		keys = append(keys, pr.key(i))
	}
	prev := ""
	if m, ok := marks.find(existing.ID, api.PendingMarkKindReview); ok {
		prev = m.Body
	}
	return add, keys, replaceBlock(existing.Body, pr.Body, prev), stale
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
func BodyComment(r *github.PendingReview, marks Marks) *github.PendingComment {
	for i, c := range r.Comments {
		if _, ok := marks.find(c.ID, api.PendingMarkKindBody); ok || strings.Contains(c.Body, bodyStart) {
			return &r.Comments[i]
		}
	}
	return nil
}

// AskBody is the body of a pending review that a question of the reviewer starts. GitHub does
// not let anyone edit a review body that started empty, so it is never empty.
func AskBody(plain bool) string {
	const text = "Questions on the spec docs of this pull request.\n"
	if plain {
		return text
	}
	return bodyStart + "\n" + text + bodyEnd
}

// AskOnFile is a question with no line in the diff, as a comment on the whole file. where names
// the section.
func AskOnFile(where, question, id string, plain bool) string {
	return fmt.Sprintf("**%s**: %s", where, question) + askTail(id, "\n\n", plain)
}

// askTail is the marker of a question, after sep, or nothing for a plain one.
func askTail(id, sep string, plain bool) string {
	if plain {
		return ""
	}
	return sep + askMarker + id + " -->"
}

// replaceBlock puts block in place of the Speccy part of body: the part between the markers,
// or prev, the part it wrote before with no marker. With neither, block goes after body.
func replaceBlock(body, block, prev string) string {
	if i := strings.Index(body, bodyStart); i >= 0 {
		if j := strings.Index(body[i:], bodyEnd); j >= 0 {
			return body[:i] + block + body[i+j+len(bodyEnd):]
		}
	}
	if prev != "" && strings.Contains(body, prev) {
		return strings.Replace(body, prev, block, 1)
	}
	if strings.TrimSpace(body) == "" {
		return block
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block
}

// AskComment is the comment of a reviewer's question, with a marker that says Speccy wrote it
// and that the same question is not posted twice. A plain question has no marker.
func AskComment(question, id string, plain bool) string {
	return question + askTail(id, "\n\n", plain)
}

// AskInBody adds a question that has no line in the diff to the body of a pending review. where
// names the doc and the section.
func AskInBody(body, where, question, id string, plain bool) string {
	line := fmt.Sprintf("**%s**: %s", where, question) + askTail(id, " ", plain)
	if strings.TrimSpace(body) == "" {
		return line
	}
	return strings.TrimRight(body, "\n") + "\n\n" + line
}

// Asked reports whether the pending review already holds the question with this id: by its
// marker, or by a mark of the local state on the review or on one of its comments.
func Asked(r *github.PendingReview, id string, marks Marks) bool {
	m := askMarker + id + " -->"
	if strings.Contains(r.Body, m) {
		return true
	}
	ids := map[string]bool{r.ID: true}
	for _, c := range r.Comments {
		if strings.Contains(c.Body, m) {
			return true
		}
		ids[c.ID] = true
	}
	for _, x := range marks {
		if x.Kind == api.PendingMarkKindAsk && x.Key == id && ids[x.CommentId] {
			return true
		}
	}
	return false
}

// BySpeccy reports whether Speccy wrote a comment: a finding, a reviewer's question, or its
// part of the body. A marker in the text says so, or a mark of the local state.
func BySpeccy(c github.PendingComment, marks Marks) bool {
	return strings.Contains(c.Body, keyMarker) || strings.Contains(c.Body, askMarker) || strings.Contains(c.Body, bodyStart) || marks.has(c.ID)
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

// PROptions sets the inline limit, the levels and the attribution of o from the .speccy.yaml
// that the review used. A flag wins: levels when it names any, and attribution when it is
// source.AttributionSpeccy or source.AttributionNone.
func PROptions(o *Options, cfg api.UrlReviewConfig, levels []string, attribution string) {
	o.InlineLimit = cfg.Pr.InlineLimit
	o.Levels = levels
	if len(levels) == 0 {
		for _, l := range cfg.Pr.Levels {
			o.Levels = append(o.Levels, string(l))
		}
	}
	if attribution == "" {
		attribution = string(cfg.Pr.Attribution)
	}
	o.Unattributed = attribution == source.AttributionNone
}

// ConfigLine says in words which .speccy.yaml a review used, for a person.
func ConfigLine(cfg api.UrlReviewConfig, commit string) string {
	switch cfg.Source {
	case api.UrlReviewConfigSourceRepo:
		return fmt.Sprintf("The review used the .speccy.yaml of the repo at %s.", short(commit))
	case api.UrlReviewConfigSourceLocal:
		return fmt.Sprintf("The repo has no .speccy.yaml at %s, so the review used %s.", short(commit), cfg.Path)
	default:
		return fmt.Sprintf("The repo has no .speccy.yaml at %s, and the local folder has none, so the review used the defaults.", short(commit))
	}
}
