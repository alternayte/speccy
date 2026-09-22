// Package action is the GitHub Action's work on a pull request (SDD §12.4): one summary comment
// that it updates on each push, inline review comments on changed lines (REQ-135) with
// suggestion blocks for deterministic fixes only (REQ-136), and one check run per bundle.
package action

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// DefaultInlineLimit is REQ-135's default number of inline comments per bundle.
const DefaultInlineLimit = 15

// Bundle is the review of one bundle that the pull request changed.
type Bundle struct {
	Slug    string
	Dir     string // the bundle's folder in the repo
	MainDoc string // the main doc's path in the bundle
	Profile string
	Kind    string // lint or full
	Verdict string
	Score   int
	Must    int
	Should  int
	Waivers int
	Relaxed int
	Error   string
	// Findings of the review; Files are the bundle's files, by their path in the bundle.
	Findings []api.Finding
	Files    map[string][]byte
	// Prefixes are the profile's trace ID prefixes, for suggested IDs (REQ-136). CoverPrefixes
	// are the upstream prefixes an acknowledgement may name, and DocScope says which checks a
	// waiver covers for the whole doc.
	Prefixes      []string
	CoverPrefixes []string
	DocScope      map[string]bool
	// Report is a link to the full report, or "".
	Report string
}

// Options are the pull request and the settings.
type Options struct {
	GitHub      *github.Client
	Repo        string
	PR          int
	HeadSHA     string
	InlineLimit int
	// Blocking is enforcement: blocking. In advisory mode a verdict never fails the job
	// (REQ-125, T-091).
	Blocking bool
	// RunURL links to the workflow run, where the HTML reports are artifacts.
	RunURL string
	// HeadRef is the branch the pull request changes, where a decision is committed. BaseRef is
	// the branch it merges into, which says which waivers are merged already. Fork is a pull
	// request from another repo, where the Action has no write token (DEC-009).
	HeadRef string
	BaseRef string
	Fork    bool
	// Sidecar reads a doc's sidecar from the checkout.
	Sidecar func(docPath string) (source.Decisions, error)
	// Relaxed are the check slugs in adoption mode, and Ready are the ones that now pass on
	// every mapped doc, which the summary comment offers to enforce (REQ-133).
	Relaxed []string
	Ready   []string
	// Config is .speccy.yaml as it stands in the checkout, for an enforce command.
	Config []byte
}

// Result is what the command prints and returns.
type Result struct {
	Summary  string // the summary comment, also for the job summary
	Posted   int    // new inline comments
	Resolved int    // inline comments whose findings are gone
	Decided  int    // decisions written from reply commands
	Enforced int    // checks taken out of adoption mode
	Failed   bool   // the job fails: a Not Build Ready verdict in blocking mode
	Warnings []string
}

const (
	summaryMarker = "<!-- speccy:summary -->"
	keyMarker     = "<!-- speccy:key:"
)

// candidate is an inline comment before it is posted.
type candidate struct {
	key     string
	path    string
	line    int
	level   api.FindingLevel
	body    string
	summary string // the line in the summary comment when the comment is not posted inline
}

// ChangedLines parses the unified diff of a file into the new-side line numbers that the pull
// request added or changed. A review comment can only go on a line in the diff.
func ChangedLines(patch string) map[int]bool {
	out := map[int]bool{}
	line := 0
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			// @@ -a,b +c,d @@
			if i := strings.Index(l, "+"); i >= 0 {
				rest := l[i+1:]
				if j := strings.IndexAny(rest, ", "); j >= 0 {
					rest = rest[:j]
				}
				line, _ = strconv.Atoi(rest)
			}
		case strings.HasPrefix(l, "+"):
			out[line] = true
			line++
		case strings.HasPrefix(l, "-"), strings.HasPrefix(l, `\`):
		default:
			line++
		}
	}
	return out
}

// key identifies a finding across pushes: the same finding on a moved line keeps its key.
func key(slug, check, message, quote string) string {
	h := sha256.Sum256([]byte(slug + "\x00" + check + "\x00" + message + "\x00" + strings.Join(strings.Fields(quote), " ")))
	return hex.EncodeToString(h[:8])
}

func lineOf(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	return bytes.Count(src[:offset], []byte("\n")) + 1
}

func lineText(src []byte, line int) (string, bool) {
	lines := strings.Split(string(src), "\n")
	if line < 1 || line > len(lines) {
		return "", false
	}
	return lines[line-1], true
}

// Deterministic fixes (REQ-136). Each returns the new text of the line, and false when the fix
// is not certain. No model writes a suggestion block.
var (
	rfcWord  = regexp.MustCompile(`^(must|should|may)$`)
	linkDest = regexp.MustCompile(`\]\(([^)\s]+)`)
)

func fixRFC2119(f api.Finding, line string, src []byte) (string, bool) {
	word := f.Anchor.Quote
	if !rfcWord.MatchString(word) {
		return "", false
	}
	start := f.Anchor.Start - (bytes.LastIndexByte(src[:f.Anchor.Start], '\n') + 1)
	if start < 0 || start+len(word) > len(line) || line[start:start+len(word)] != word {
		return "", false
	}
	return line[:start] + strings.ToUpper(word) + line[start+len(word):], true
}

func fixBrokenLink(b Bundle, f api.Finding, line string) (string, bool) {
	// The finding points at the link; its destination is on the line. Exactly one relative
	// destination on the line must be broken, and exactly one bundle file must have its name.
	dir := path.Dir(f.Anchor.File)
	var broken []string
	for _, m := range linkDest.FindAllStringSubmatch(line, -1) {
		dest := strings.SplitN(m[1], "#", 2)[0]
		if dest == "" || strings.Contains(dest, "://") || strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "mailto:") {
			continue
		}
		if _, ok := b.Files[path.Clean(path.Join(dir, dest))]; !ok {
			broken = append(broken, m[1])
		}
	}
	if len(broken) != 1 {
		return "", false
	}
	dest := broken[0]
	base := path.Base(strings.SplitN(dest, "#", 2)[0])
	var matches []string
	for p := range b.Files {
		if path.Base(p) == base {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	target := relPath(dir, matches[0])
	if _, frag, ok := strings.Cut(dest, "#"); ok {
		target += "#" + frag
	}
	if target == dest {
		return "", false
	}
	return strings.Replace(line, "]("+dest+")", "]("+target+")", 1), true
}

// relPath is the path of to relative to the folder from, both inside the bundle.
func relPath(from, to string) string {
	if from == "." || from == "" {
		return to
	}
	fp, tp := strings.Split(from, "/"), strings.Split(to, "/")
	i := 0
	for i < len(fp) && i < len(tp)-1 && fp[i] == tp[i] {
		i++
	}
	return strings.Repeat("../", len(fp)-i) + strings.Join(tp[i:], "/")
}

func suggestion(line string) string { return "\n\n```suggestion\n" + line + "\n```" }

// candidates builds the inline comments of a bundle: MUST findings and findings with a
// deterministic fix, on changed lines of the files of the bundle, MUST first. The rest go to
// the summary.
func candidates(b Bundle, changed map[string]map[int]bool) (inline []candidate, rest []candidate) {
	rank := map[api.FindingLevel]int{api.FindingLevelMUST: 0, api.FindingLevelSHOULD: 1, api.FindingLevelINFO: 2}
	fs := append([]api.Finding(nil), b.Findings...)
	sort.SliceStable(fs, func(i, j int) bool { return rank[fs[i].Level] < rank[fs[j].Level] })
	for _, f := range fs {
		if f.Waived || (f.Anchor.Detached != nil && *f.Anchor.Detached) {
			continue
		}
		src := b.Files[f.Anchor.File]
		repoPath := path.Join(b.Dir, f.Anchor.File)
		line := lineOf(src, f.Anchor.Start)
		text, ok := lineText(src, line)
		var fix string
		var fixOK bool
		if ok {
			switch f.CheckSlug {
			case "lint.rfc2119-case":
				fix, fixOK = fixRFC2119(f, text, src)
			case "lint.broken-link":
				fix, fixOK = fixBrokenLink(b, f, text)
			}
		}
		if f.Level != api.FindingLevelMUST && !fixOK {
			continue // SHOULD and INFO findings stay in the report
		}
		k := key(b.Slug, f.CheckSlug, f.Message, f.Anchor.Quote)
		body := fmt.Sprintf("**%s** `%s`: %s", f.Level, f.CheckSlug, f.Message)
		if f.Fix != nil {
			body += "\n\nFix: " + *f.Fix
		}
		if fixOK {
			body += suggestion(fix)
		}
		body += "\n\n" + keyMarker + k + " -->"
		c := candidate{key: k, path: repoPath, line: line, level: f.Level, body: body,
			summary: fmt.Sprintf("- **%s** `%s` %s:%d: %s", f.Level, f.CheckSlug, repoPath, line, f.Message)}
		if changed[repoPath][line] {
			inline = append(inline, c)
		} else if f.Level == api.FindingLevelMUST {
			rest = append(rest, c)
		}
	}
	// REQ-136: suggested trace IDs for unnumbered items, when the profile's ID check failed.
	idsFailed := false
	for _, f := range fs {
		if !f.Waived && (strings.HasSuffix(f.CheckSlug, ".req.ids") || strings.HasSuffix(f.CheckSlug, ".decisions.ids")) {
			idsFailed = true
		}
	}
	if main := b.Files[b.MainDoc]; idsFailed && main != nil {
		for _, s := range review.SuggestIDs(b.MainDoc, main, b.Prefixes) {
			line := lineOf(main, s.Insert)
			text, ok := lineText(main, line)
			start := s.Insert - (bytes.LastIndexByte(main[:s.Insert], '\n') + 1)
			repoPath := path.Join(b.Dir, b.MainDoc)
			if !ok || start > len(text) || !changed[repoPath][line] {
				continue
			}
			k := key(b.Slug, "trace.id", s.ID, text)
			body := fmt.Sprintf("Give this item the trace ID `%s`, so that other docs and tests can point at it (REQ-052).%s\n\n%s%s -->",
				s.ID, suggestion(text[:start]+"**"+s.ID+":** "+text[start:]), keyMarker, k)
			inline = append(inline, candidate{key: k, path: repoPath, line: line, level: api.FindingLevelSHOULD, body: body})
		}
	}
	sort.SliceStable(inline, func(i, j int) bool { return rank[inline[i].level] < rank[inline[j].level] })
	return inline, rest
}

// keyIn returns the finding key in a comment body.
func keyIn(body string) string {
	i := strings.Index(body, keyMarker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(keyMarker):]
	if j := strings.Index(rest, " "); j >= 0 {
		return rest[:j]
	}
	return ""
}

var verdictText = map[string]string{"build_ready": "Build Ready", "not_build_ready": "Not Build Ready", "stale": "Stale"}

// Run posts the review of the bundles on the pull request.
func Run(ctx context.Context, o Options, bundles []Bundle, files []github.PRFile) Result {
	var res Result
	if o.InlineLimit <= 0 {
		o.InlineLimit = DefaultInlineLimit
	}
	changed := map[string]map[int]bool{}
	for _, f := range files {
		changed[f.Filename] = ChangedLines(f.Patch)
	}

	threads, threadsErr := o.GitHub.ReviewThreads(ctx, o.Repo, o.PR)
	if threadsErr != nil {
		res.Warnings = append(res.Warnings, "Speccy could not read the review threads, so it posts no inline comments: "+threadsErr.Error())
	}
	open := map[string]bool{}
	for _, t := range threads {
		if k := keyIn(t.Body); k != "" && !t.Resolved {
			open[k] = true
		}
	}

	var post []github.ReviewComment
	current := map[string]bool{}
	summaries := map[string][]string{}
	for _, b := range bundles {
		inline, rest := candidates(b, changed)
		for i, c := range inline {
			current[c.key] = true
			if i >= o.InlineLimit {
				if c.summary != "" {
					rest = append(rest, c)
				}
				continue
			}
			if open[c.key] || threadsErr != nil {
				continue // REQ-135: never a duplicate of an open comment
			}
			post = append(post, github.ReviewComment{Path: c.path, Line: c.line, Side: "RIGHT", Body: c.body})
		}
		for _, c := range rest {
			summaries[b.Slug] = append(summaries[b.Slug], c.summary)
		}
	}
	if len(post) > 0 {
		if err := o.GitHub.CreateReview(ctx, o.Repo, o.PR, o.HeadSHA, "Speccy found these on the lines this pull request changed.", post); err != nil {
			res.Warnings = append(res.Warnings, "Speccy could not post inline comments: "+err.Error())
		} else {
			res.Posted = len(post)
		}
	}
	// The reply commands of this pull request: one commit, then the threads they answered.
	var act applied
	if o.Sidecar != nil && threadsErr == nil {
		var warn []string
		act, warn = apply(ctx, o, bundles, threads)
		res.Warnings = append(res.Warnings, warn...)
		res.Decided = len(act.threads)
		for _, id := range act.threads {
			if err := o.GitHub.ResolveThread(ctx, id); err != nil {
				res.Warnings = append(res.Warnings, "Speccy could not resolve a comment: "+err.Error())
			}
		}
	}
	decided := map[string]bool{}
	for _, id := range act.threads {
		decided[id] = true
	}
	if len(o.Relaxed) > 0 && o.Config != nil {
		var warn []string
		act.enforced, warn = enforce(ctx, o)
		res.Warnings = append(res.Warnings, warn...)
		res.Enforced = len(act.enforced)
	}

	// REQ-135: resolve Speccy's own threads whose findings are gone.
	for _, t := range threads {
		if k := keyIn(t.Body); k != "" && !t.Resolved && !current[k] && !decided[t.ID] {
			if err := o.GitHub.ResolveThread(ctx, t.ID); err != nil {
				res.Warnings = append(res.Warnings, "Speccy could not resolve a comment: "+err.Error())
				continue
			}
			res.Resolved++
		}
	}

	// DEC-009: a waiver that this pull request adds counts, and the comment says so in words.
	pending := map[string]int{}
	if o.Sidecar != nil {
		for _, b := range bundles {
			ws, err := unmerged(ctx, o, docPath(b))
			if err != nil {
				res.Warnings = append(res.Warnings, "Speccy could not read the waivers of "+b.Slug+" on "+o.BaseRef+": "+err.Error())
				continue
			}
			pending[b.Slug] = dependsOn(b, ws)
		}
	}
	res.Summary = summary(o, bundles, summaries, pending, act)
	// A pull request that changes no bundle gets no new comment; an old one says so.
	if err := upsertSummary(ctx, o, res.Summary, len(bundles) > 0); err != nil {
		res.Warnings = append(res.Warnings, "Speccy could not post the summary comment: "+err.Error())
	}
	for _, b := range bundles {
		ready := b.Error == "" && b.Verdict == "build_ready"
		conclusion := "success"
		switch {
		case !ready && o.Blocking:
			conclusion = "failure"
			res.Failed = true
		case !ready:
			conclusion = "neutral" // advisory: the verdict shows, and the job passes (REQ-125)
		}
		title := verdictText[b.Verdict]
		if b.Error != "" {
			title = "The review failed"
		} else if n := pending[b.Slug]; n > 0 {
			title += fmt.Sprintf(" with %d waiver%s that this pull request has not merged", n, plural(n))
		}
		if err := o.GitHub.CreateCheckRun(ctx, o.Repo, github.CheckRun{Name: "speccy: " + b.Slug, HeadSHA: o.HeadSHA, Conclusion: conclusion,
			Title: title, Summary: bundleSummary(b), DetailsURL: b.Report}); err != nil {
			res.Warnings = append(res.Warnings, "Speccy could not set the check for "+b.Slug+": "+err.Error())
		}
	}
	return res
}

func bundleSummary(b Bundle) string {
	if b.Error != "" {
		return b.Error
	}
	s := fmt.Sprintf("Score %d. %d MUST, %d SHOULD.", b.Score, b.Must, b.Should)
	if b.Waivers > 0 {
		s += fmt.Sprintf(" %d waiver%s.", b.Waivers, plural(b.Waivers))
	}
	if b.Kind == "lint" {
		s += " Lint checks only."
	}
	return s
}

// isAre agrees the verb with the count, because the line is read by a person.
func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// short is the first 7 characters of a commit SHA.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// summary is the one summary comment (SDD §12.4).
func summary(o Options, bundles []Bundle, rest map[string][]string, pending map[string]int, act applied) string {
	var b strings.Builder
	b.WriteString(summaryMarker + "\n## Speccy\n\n")
	if len(bundles) == 0 {
		b.WriteString("This pull request changes no bundle.\n")
		return b.String()
	}
	b.WriteString("| Bundle | Verdict | Score | MUST | SHOULD |\n|---|---|---|---|---|\n")
	relaxed := 0
	for _, x := range bundles {
		v := verdictText[x.Verdict]
		if x.Error != "" {
			v = "Review failed"
		}
		if x.Waivers > 0 {
			v += fmt.Sprintf(" (%d waiver%s)", x.Waivers, plural(x.Waivers))
		}
		name := "`" + x.Slug + "`"
		if x.Report != "" {
			name = "[`" + x.Slug + "`](" + x.Report + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d |\n", name, v, x.Score, x.Must, x.Should)
		relaxed = max(relaxed, x.Relaxed)
	}
	if relaxed > 0 {
		fmt.Fprintf(&b, "\nAdoption mode: %d check%s relaxed.\n", relaxed, plural(relaxed))
	}
	for _, x := range bundles {
		if n := pending[x.Slug]; n > 0 {
			fmt.Fprintf(&b, "\n`%s`: %s. %d waiver%s in this pull request %s not merged yet. Without them: Not Build Ready.\n",
				x.Slug, verdictText[x.Verdict], n, plural(n), isAre(n))
		}
	}
	if len(act.enforced) > 0 {
		b.WriteString("\n**Adoption mode**\n\n")
		for _, e := range act.enforced {
			fmt.Fprintf(&b, "- @%s turned `%s` back on. It counts from the next review.\n", e.By, e.Rest)
		}
	}
	if len(o.Ready) > 0 {
		b.WriteString("\nThese relaxed checks now pass on every mapped doc. Reply to turn one back on:\n\n```text\n")
		for _, slug := range o.Ready {
			fmt.Fprintf(&b, "/speccy enforce %s\n", slug)
		}
		b.WriteString("```\n")
	}
	if len(act.decisions) > 0 {
		b.WriteString("\n**Decisions**\n\n")
		for _, d := range act.decisions {
			b.WriteString(d.Line + "\n")
		}
		if act.committed != "" {
			fmt.Fprintf(&b, "\nCommitted as `%s`. A decision counts when this pull request merges.\n", short(act.committed))
		}
		b.WriteString(pasteBlock(act.paste))
	}
	for _, x := range bundles {
		if x.Error != "" {
			fmt.Fprintf(&b, "\n**%s**: %s\n", x.Slug, x.Error)
		}
		if lines := rest[x.Slug]; len(lines) > 0 {
			fmt.Fprintf(&b, "\n<details><summary><code>%s</code>: %d finding%s not on a changed line</summary>\n\n%s\n\n</details>\n",
				x.Slug, len(lines), plural(len(lines)), strings.Join(lines, "\n"))
		}
	}
	if o.RunURL != "" {
		fmt.Fprintf(&b, "\nThe full HTML reports are in the artifacts of [this run](%s).\n", o.RunURL)
	} else if slices.ContainsFunc(bundles, func(x Bundle) bool { return x.Report != "" }) {
		b.WriteString("\nEach bundle name links to its full report on the Speccy server.\n")
	}
	if !o.Blocking {
		b.WriteString("\nAdvisory mode: the verdict does not fail the check.\n")
	}
	return b.String()
}

func upsertSummary(ctx context.Context, o Options, body string, create bool) error {
	return upsertMarked(ctx, o, summaryMarker, body, create)
}

// upsertMarked keeps one comment per marker, so the review summary and the verification
// summary each have their own.
func upsertMarked(ctx context.Context, o Options, marker, body string, create bool) error {
	comments, err := o.GitHub.IssueComments(ctx, o.Repo, o.PR)
	if err != nil {
		return err
	}
	for _, c := range comments {
		if strings.HasPrefix(c.Body, marker) {
			return o.GitHub.UpdateIssueComment(ctx, o.Repo, c.ID, body)
		}
	}
	if !create {
		return nil
	}
	return o.GitHub.CreateIssueComment(ctx, o.Repo, o.PR, body)
}
