package action

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source/github"
)

// fakePR is the part of the GitHub API that the Action uses, for one pull request.
type fakePR struct {
	mu       sync.Mutex
	threads  []thread
	issue    []github.IssueComment
	checks   []map[string]any
	reviews  int
	resolved []string
}

type thread struct {
	id       string
	body     string
	path     string
	line     int
	resolved bool
}

func (f *fakePR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	p := strings.TrimPrefix(r.URL.Path, "/repos/acme/specs")
	switch {
	case r.URL.Path == "/graphql":
		var in struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if strings.Contains(in.Query, "resolveReviewThread") {
			id := in.Variables["id"].(string)
			for i := range f.threads {
				if f.threads[i].id == id {
					f.threads[i].resolved = true
				}
			}
			f.resolved = append(f.resolved, id)
			send(map[string]any{"data": map[string]any{}})
			return
		}
		var nodes []map[string]any
		for _, t := range f.threads {
			nodes = append(nodes, map[string]any{"id": t.id, "isResolved": t.resolved,
				"comments": map[string]any{"nodes": []map[string]string{{"body": t.body}}}})
		}
		send(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"reviewThreads": map[string]any{"nodes": nodes}}}}})
	case p == "/pulls/7/reviews":
		var in struct {
			Comments []github.ReviewComment `json:"comments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, c := range in.Comments {
			f.threads = append(f.threads, thread{id: fmt.Sprintf("T%d", len(f.threads)+1), body: c.Body, path: c.Path, line: c.Line})
		}
		f.reviews++
		send(map[string]any{"id": f.reviews})
	case p == "/issues/7/comments" && r.Method == "GET":
		send(f.issue)
	case p == "/issues/7/comments" && r.Method == "POST":
		var in github.IssueComment
		_ = json.NewDecoder(r.Body).Decode(&in)
		in.ID = int64(len(f.issue) + 1)
		f.issue = append(f.issue, in)
		send(in)
	case strings.HasPrefix(p, "/issues/comments/") && r.Method == "PATCH":
		var in github.IssueComment
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.issue[0].Body = in.Body
		send(f.issue[0])
	case p == "/check-runs":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.checks = append(f.checks, in)
		send(in)
	default:
		w.WriteHeader(404)
		send(map[string]string{"message": "not found: " + r.URL.Path})
	}
}

func newPR(t *testing.T) (*fakePR, Options) {
	t.Helper()
	f := &fakePR{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, Options{GitHub: &github.Client{API: srv.URL, Token: "t"}, Repo: "acme/specs", PR: 7, HeadSHA: "abc"}
}

// doc has a finding on each of lines 6 to 25.
func doc() []byte {
	var b strings.Builder
	b.WriteString("---\ntype: prd\n---\n# Refunds\n\n")
	for i := 6; i <= 25; i++ {
		fmt.Fprintf(&b, "Line %d has a TBD here.\n", i)
	}
	return []byte(b.String())
}

func finding(src []byte, line int, slug string, level api.FindingLevel, quote string) api.Finding {
	lines := strings.SplitAfter(string(src), "\n")
	start := 0
	for i := 0; i < line-1; i++ {
		start += len(lines[i])
	}
	start += strings.Index(lines[line-1], quote)
	fix := "Replace the placeholder."
	return api.Finding{CheckSlug: slug, Level: level, Message: fmt.Sprintf("Placeholder on line %d.", line), Fix: &fix,
		Anchor: api.Anchor{File: "PRD.md", Quote: quote, Start: start, End: start + len(quote)}}
}

func patchFor(lines ...int) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "@@ -%d,1 +%d,1 @@\n-old\n+new\n", l, l)
	}
	return b.String()
}

// T-096: inline comments go only on changed lines, keep to the limit, are not posted twice,
// and are resolved when their finding is gone.
func TestAction_InlineComments(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	o.InlineLimit = 3
	src := doc()
	var fs []api.Finding
	for l := 6; l <= 25; l++ {
		fs = append(fs, finding(src, l, "lint.placeholder", api.FindingLevelMUST, "TBD"))
	}
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Must: 20,
		Findings: fs, Files: map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "docs/refunds/PRD.md", Patch: patchFor(8, 9, 10, 11, 12)}}

	res := Run(ctx, o, []Bundle{b}, files)
	if res.Posted != 3 {
		t.Fatalf("posted %d inline comments, want the limit of 3; warnings %v", res.Posted, res.Warnings)
	}
	for _, th := range f.threads {
		if th.path != "docs/refunds/PRD.md" || th.line < 8 || th.line > 12 {
			t.Errorf("a comment on an unchanged line: %s:%d", th.path, th.line)
		}
	}
	body := f.issue[0].Body
	if !strings.Contains(body, "not on a changed line") || !strings.Contains(body, "docs/refunds/PRD.md:6") || !strings.Contains(body, "docs/refunds/PRD.md:11") {
		t.Errorf("the summary does not list the findings off the diff or over the limit:\n%s", body)
	}

	// A new push with the same findings posts nothing new, and updates the one summary.
	res = Run(ctx, o, []Bundle{b}, files)
	if res.Posted != 0 || len(f.threads) != 3 || len(f.issue) != 1 {
		t.Fatalf("second push: posted %d, %d threads, %d summary comments", res.Posted, len(f.threads), len(f.issue))
	}

	// The author fixes line 8: its comment is resolved, and the next finding takes the slot.
	b.Findings = fs[3:]
	res = Run(ctx, o, []Bundle{b}, files)
	if res.Resolved != 1 || !f.threads[0].resolved || res.Posted != 1 {
		t.Fatalf("after the fix: resolved %d (first thread resolved %v), posted %d", res.Resolved, f.threads[0].resolved, res.Posted)
	}
}

// T-097: suggestion blocks only for the deterministic fixes of REQ-136, never for other
// findings.
func TestAction_SuggestionsDeterministicOnly(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	src := []byte("---\ntype: prd\n---\n# Refunds\n\n## Requirements\n\n- **REQ-001:** An agent must refund in one step.\n- Refunds show in the order history.\n\nSee the [limits](limits.md).\n\nThe owner is TBD.\n")
	must := strings.Index(string(src), "must")
	link := strings.Index(string(src), "[limits](limits.md)")
	tbd := strings.Index(string(src), "TBD")
	fix := "Model-written fix."
	b := Bundle{Slug: "refunds", Dir: "refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Prefixes: []string{"REQ"},
		Files: map[string][]byte{"PRD.md": src, "assets/limits.md": []byte("Limits.")},
		Findings: []api.Finding{
			{CheckSlug: "lint.rfc2119-case", Level: api.FindingLevelSHOULD, Message: "lower case",
				Anchor: api.Anchor{File: "PRD.md", Quote: "must", Start: must, End: must + 4}},
			{CheckSlug: "lint.broken-link", Level: api.FindingLevelMUST, Message: "broken",
				Anchor: api.Anchor{File: "PRD.md", Quote: "[limits](limits.md)", Start: link, End: link + 18}},
			{CheckSlug: "lint.placeholder", Level: api.FindingLevelMUST, Message: "placeholder", Fix: &fix,
				Anchor: api.Anchor{File: "PRD.md", Quote: "TBD", Start: tbd, End: tbd + 3}},
			{CheckSlug: "prd.req.ids", Level: api.FindingLevelMUST, Message: "An item has no ID.", Fix: &fix,
				Anchor: api.Anchor{File: "PRD.md", Quote: "", Start: 0, End: 0}},
		}}
	files := []github.PRFile{{Filename: "refunds/PRD.md", Patch: patchFor(8, 9, 11, 13)}}
	if res := Run(ctx, o, []Bundle{b}, files); res.Posted != 4 {
		t.Fatalf("posted %d, want 4 (warnings %v)", res.Posted, res.Warnings)
	}
	want := map[int]string{
		8:  "- **REQ-001:** An agent MUST refund in one step.",
		9:  "- **REQ-002:** Refunds show in the order history.",
		11: "See the [limits](assets/limits.md).",
	}
	for _, th := range f.threads {
		s, hasSuggestion := strings.CutPrefix(th.body[strings.Index(th.body, "```")+1:], "``suggestion\n")
		switch th.line {
		case 13:
			if strings.Contains(th.body, "```suggestion") {
				t.Errorf("a finding with no deterministic fix has a suggestion block:\n%s", th.body)
			}
		default:
			if !hasSuggestion || !strings.HasPrefix(s, want[th.line]+"\n```") {
				t.Errorf("line %d: want the suggestion %q, got:\n%s", th.line, want[th.line], th.body)
			}
		}
	}
}

// T-091: in advisory mode a verdict never fails the job; its check is neutral. Blocking mode
// fails it.
func TestAction_AdvisoryNeverFails(t *testing.T) {
	ctx := context.Background()
	b := Bundle{Slug: "refunds", Dir: "refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Must: 2, Files: map[string][]byte{}}
	ready := Bundle{Slug: "payments", Dir: "payments", MainDoc: "PRD.md", Verdict: "build_ready", Files: map[string][]byte{}}
	for _, c := range []struct {
		blocking   bool
		failed     bool
		conclusion string
	}{{false, false, "neutral"}, {true, true, "failure"}} {
		f, o := newPR(t)
		o.Blocking = c.blocking
		res := Run(ctx, o, []Bundle{b, ready}, nil)
		if res.Failed != c.failed {
			t.Errorf("blocking %v: failed %v, want %v", c.blocking, res.Failed, c.failed)
		}
		if len(f.checks) != 2 || f.checks[0]["conclusion"] != c.conclusion || f.checks[1]["conclusion"] != "success" {
			t.Errorf("blocking %v: checks %v", c.blocking, f.checks)
		}
		if !c.blocking && !strings.Contains(f.issue[0].Body, "Advisory mode") {
			t.Errorf("the summary does not say advisory mode:\n%s", f.issue[0].Body)
		}
	}
}

// ChangedLines reads the new-side lines of a diff.
func TestChangedLines(t *testing.T) {
	got := ChangedLines("@@ -1,3 +1,4 @@\n a\n-b\n+B\n+C\n c\n@@ -10 +11,2 @@\n x\n+y\n")
	for _, l := range []int{2, 3, 12} {
		if !got[l] {
			t.Errorf("line %d is not changed: %v", l, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("changed lines %v", got)
	}
}
