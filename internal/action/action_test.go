package action

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// fakePR is the part of the GitHub API that the Action uses, for one pull request.
type fakePR struct {
	mu        sync.Mutex
	threads   []thread
	issue     []github.IssueComment
	checks    []map[string]any
	reviews   int
	resolved  []string
	committed []string
	moved     int
}

type thread struct {
	id       string
	body     string
	path     string
	line     int
	resolved bool
	replies  []github.Reply
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
		// GitHub gives at most 100 threads, and 100 comments of a thread, in one page.
		page := func(n int) (from, to int, info map[string]any) {
			if a, ok := in.Variables["after"].(string); ok && a != "" {
				from, _ = strconv.Atoi(a)
			}
			to = min(from+100, n)
			return from, to, map[string]any{"hasNextPage": to < n, "endCursor": strconv.Itoa(to)}
		}
		comments := func(t thread, from, to int) []map[string]any {
			all := []map[string]any{{"body": t.body}}
			for _, r := range t.replies {
				all = append(all, map[string]any{"body": r.Body, "author": map[string]string{"login": r.Author}})
			}
			return all[min(from, len(all)):min(to, len(all))]
		}
		if strings.Contains(in.Query, "node(id:") {
			for _, t := range f.threads {
				if t.id == in.Variables["id"] {
					from, to, info := page(len(t.replies) + 1)
					send(map[string]any{"data": map[string]any{"node": map[string]any{
						"comments": map[string]any{"pageInfo": info, "nodes": comments(t, from, to)}}}})
				}
			}
			return
		}
		from, to, info := page(len(f.threads))
		var nodes []map[string]any
		for _, t := range f.threads[from:to] {
			n := len(t.replies) + 1
			nodes = append(nodes, map[string]any{"id": t.id, "isResolved": t.resolved,
				"comments": map[string]any{"nodes": comments(t, 0, 100),
					"pageInfo": map[string]any{"hasNextPage": n > 100, "endCursor": "100"}}})
		}
		send(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"reviewThreads": map[string]any{"nodes": nodes, "pageInfo": info}}}}})
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
	case p == "/git/ref/heads/topic" && r.Method == "GET":
		send(map[string]any{"object": map[string]string{"sha": "base-sha"}})
	case p == "/git/commits/base-sha" && r.Method == "GET":
		send(map[string]any{"tree": map[string]string{"sha": "base-tree"}})
	case p == "/git/blobs":
		send(map[string]string{"sha": "blob-sha"})
	case p == "/git/trees":
		var in struct {
			Tree []struct {
				Path string `json:"path"`
			} `json:"tree"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, e := range in.Tree {
			f.committed = append(f.committed, e.Path)
		}
		send(map[string]string{"sha": "tree-sha"})
	case p == "/git/commits":
		send(map[string]string{"sha": "0123456789"})
	case strings.HasPrefix(p, "/git/refs/heads/") && r.Method == "PATCH":
		f.moved++
		send(map[string]any{})
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

// sidecars is the checkout's sidecars, by doc path, for the Options.Sidecar of a test.
type sidecars map[string]string

func (s sidecars) read(doc string) (source.Decisions, error) {
	return source.ParseDecisions([]byte(s[doc]))
}

// A reply of /speccy waive on a Speccy comment commits the waiver to the pull request's branch
// and resolves that thread (DEC-009).
func TestAction_ReplyCommandCommitsTheWaiver(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	side := sidecars{}
	o.Sidecar, o.HeadRef, o.BaseRef = side.read, "topic", "main"
	src := doc()
	fs := []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD")}
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Must: 1,
		Findings: fs, Files: map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "docs/refunds/PRD.md", Patch: patchFor(8)}}
	if res := Run(ctx, o, []Bundle{b}, files); res.Posted != 1 {
		t.Fatalf("posted %d, want 1: %v", res.Posted, res.Warnings)
	}

	// The approver replies on Speccy's comment.
	f.threads[0].replies = []github.Reply{{Author: "kim", Body: "/speccy waive The owner lands in the next doc."}}
	res := Run(ctx, o, []Bundle{b}, files)
	if res.Decided != 1 {
		t.Fatalf("decided %d, want 1: %v", res.Decided, res.Warnings)
	}
	if want := ".speccy/decisions/docs/refunds/PRD.md.yaml"; !slices.Contains(f.committed, want) {
		t.Errorf("committed %v, want %s", f.committed, want)
	}
	if f.moved != 1 || !f.threads[0].resolved {
		t.Errorf("moved the branch %d times, thread resolved %v", f.moved, f.threads[0].resolved)
	}
	if body := f.issue[0].Body; !strings.Contains(body, "@kim asked for a waiver of `lint.placeholder`") {
		t.Errorf("the summary does not name the decision:\n%s", body)
	}
}

// A reply command applies once. The thread it answered is resolved, so the next push neither
// commits the same decision again nor repeats a refusal.
func TestAction_ReplyCommandAppliesOnce(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	side := sidecars{}
	o.Sidecar, o.HeadRef, o.BaseRef = side.read, "topic", "main"
	src := doc()
	fs := []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD"),
		finding(src, 9, "lint.placeholder", api.FindingLevelMUST, "TBD")}
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Must: 2,
		Findings: fs, Files: map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "docs/refunds/PRD.md", Patch: patchFor(8, 9)}}
	if res := Run(ctx, o, []Bundle{b}, files); res.Posted != 2 {
		t.Fatalf("posted %d, want 2: %v", res.Posted, res.Warnings)
	}
	f.threads[0].replies = []github.Reply{{Author: "kim", Body: "/speccy waive The owner lands in the next doc."}}
	f.threads[1].replies = []github.Reply{{Author: "kim", Body: "/speccy ack The owner lands in the next doc."}}
	// The author fixes line 9, so the ack answers a finding that is gone.
	b.Findings = fs[:1]
	Run(ctx, o, []Bundle{b}, files)
	if f.moved != 1 || !f.threads[0].resolved || !f.threads[1].resolved {
		t.Fatalf("moved the branch %d times, threads resolved %v %v", f.moved, f.threads[0].resolved, f.threads[1].resolved)
	}

	// The next push.
	res := Run(ctx, o, []Bundle{b}, files)
	if res.Decided != 0 || f.moved != 1 {
		t.Errorf("the next push applied the command again: decided %d, moved the branch %d times", res.Decided, f.moved)
	}
	if body := f.issue[0].Body; strings.Contains(body, "**Decisions**") {
		t.Errorf("the next push repeats the decisions:\n%s", body)
	}
}

// A fork's pull request gets the sidecar to paste, and no commit.
func TestAction_ForkPastesTheSidecar(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	side := sidecars{}
	o.Sidecar, o.HeadRef, o.BaseRef, o.Fork = side.read, "topic", "main", true
	src := doc()
	b := Bundle{Slug: "refunds", Dir: "refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Must: 1,
		Findings: []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD")},
		Files:    map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "refunds/PRD.md", Patch: patchFor(8)}}
	Run(ctx, o, []Bundle{b}, files)
	f.threads[0].replies = []github.Reply{{Author: "kim", Body: "/speccy waive The owner lands in the next doc."}}
	res := Run(ctx, o, []Bundle{b}, files)
	if res.Decided != 0 || len(f.committed) != 0 || f.moved != 0 {
		t.Fatalf("a fork was committed to: decided %d, committed %v", res.Decided, f.committed)
	}
	body := f.issue[0].Body
	if !strings.Contains(body, "comes from a fork") || !strings.Contains(body, "check: lint.placeholder") {
		t.Errorf("the summary has no text to paste:\n%s", body)
	}
}

// A commit that fails on a pull request from the same repo says why in the summary, not that
// the pull request comes from a fork.
func TestAction_FailedCommitSaysWhy(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	side := sidecars{}
	// The branch is gone, so the commit fails.
	o.Sidecar, o.HeadRef, o.BaseRef = side.read, "deleted", "main"
	src := doc()
	b := Bundle{Slug: "refunds", Dir: "refunds", MainDoc: "PRD.md", Verdict: "not_build_ready", Must: 1,
		Findings: []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD")},
		Files:    map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "refunds/PRD.md", Patch: patchFor(8)}}
	Run(ctx, o, []Bundle{b}, files)
	f.threads[0].replies = []github.Reply{{Author: "kim", Body: "/speccy waive The owner lands in the next doc."}}
	res := Run(ctx, o, []Bundle{b}, files)
	if res.Decided != 0 || f.moved != 0 {
		t.Fatalf("decided %d, moved the branch %d times", res.Decided, f.moved)
	}
	body := f.issue[0].Body
	if strings.Contains(body, "comes from a fork") {
		t.Errorf("the summary blames a fork:\n%s", body)
	}
	if !strings.Contains(body, "Speccy could not commit to this pull request's branch") || !strings.Contains(body, "GitHub found nothing") ||
		!strings.Contains(body, "check: lint.placeholder") {
		t.Errorf("the summary does not say why the commit failed, or has no text to paste:\n%s", body)
	}
}

// A waiver that the pull request adds counts, and the comment says the verdict depends on it.
func TestAction_UnmergedWaiverNamedInTheSummary(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	src := doc()
	fs := []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD")}
	fs[0].Waived = true
	fs[0].Anchor.HeadingPath = []string{"Refunds"}
	hash, _ := section.HashAt(section.Parse(src), src, []string{"Refunds"})
	side := sidecars{"docs/refunds/PRD.md": "waivers:\n  - check: lint.placeholder\n    section: [Refunds]\n    reason: The owner lands in the next doc.\n    section_hash: " + hash + "\n"}
	o.Sidecar, o.HeadRef, o.BaseRef = side.read, "topic", "main"
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md", Verdict: "build_ready", Waivers: 1,
		Findings: fs, Files: map[string][]byte{"PRD.md": src}}
	Run(ctx, o, []Bundle{b}, []github.PRFile{{Filename: "docs/refunds/PRD.md", Patch: patchFor(8)}})
	body := f.issue[0].Body
	if !strings.Contains(body, "1 waiver in this pull request is not merged yet. Without them: Not Build Ready.") {
		t.Errorf("the summary does not name the waiver the verdict depends on:\n%s", body)
	}
	if len(f.checks) == 0 || !strings.Contains(fmt.Sprint(f.checks[0]["output"]), "has not merged") {
		t.Errorf("the check run does not say the verdict depends on an unmerged waiver: %v", f.checks)
	}
}

// The summary comment offers a relaxed check that now passes, and a reply in the conversation
// commits its removal from .speccy.yaml (REQ-133).
func TestAction_EnforceRamp(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	side := sidecars{}
	o.Sidecar, o.HeadRef, o.BaseRef = side.read, "topic", "main"
	o.Relaxed = []string{"lint.placeholder", "links.has-upstream"}
	o.Ready = []string{"links.has-upstream"}
	o.Config = []byte("map:\n  - glob: \"docs/*.md\"\n    profile: prd\nadoption:\n  relaxed:\n    - lint.placeholder\n    - links.has-upstream\n")
	src := doc()
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md", Verdict: "not_build_ready",
		Findings: []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD")},
		Files:    map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "docs/refunds/PRD.md", Patch: patchFor(8)}}
	Run(ctx, o, []Bundle{b}, files)
	if body := f.issue[0].Body; !strings.Contains(body, "/speccy enforce links.has-upstream") {
		t.Fatalf("the summary does not offer the check that now passes:\n%s", body)
	}

	// A maintainer replies in the conversation.
	f.issue = append(f.issue, github.IssueComment{ID: 9, Body: "/speccy enforce links.has-upstream",
		User: &struct {
			Login string `json:"login"`
		}{Login: "kim"}})
	res := Run(ctx, o, []Bundle{b}, files)
	if res.Enforced != 1 {
		t.Fatalf("enforced %d, want 1: %v", res.Enforced, res.Warnings)
	}
	if !slices.Contains(f.committed, ".speccy.yaml") {
		t.Errorf("committed %v, want .speccy.yaml", f.committed)
	}
	if body := f.issue[0].Body; !strings.Contains(body, "@kim turned `links.has-upstream` back on") {
		t.Errorf("the summary does not name the change:\n%s", body)
	}
}

// Two findings with the same check, message and quote, such as two lower-case "must", each get
// a comment of their own. When one is fixed, its comment is resolved and the other finding
// keeps one open comment on its line.
func TestAction_TwinFindingsHaveTheirOwnComments(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	src := []byte("# Refunds\n\n## Requirements\n\n- **REQ-001:** An agent must refund in one step.\n- **REQ-002:** A refund must show in the history.\n")
	twin := func(nth int) api.Finding {
		at := strings.Index(string(src), "must")
		if nth == 2 {
			at = strings.LastIndex(string(src), "must")
		}
		return api.Finding{CheckSlug: "lint.rfc2119-case", Level: api.FindingLevelSHOULD, Message: "lower case",
			Anchor: api.Anchor{File: "PRD.md", Quote: "must", Start: at, End: at + 4}}
	}
	b := Bundle{Slug: "refunds", Dir: "refunds", MainDoc: "PRD.md", Verdict: "not_build_ready",
		Files: map[string][]byte{"PRD.md": src}, Findings: []api.Finding{twin(1), twin(2)}}
	files := []github.PRFile{{Filename: "refunds/PRD.md", Patch: patchFor(5, 6)}}
	if res := Run(ctx, o, []Bundle{b}, files); res.Posted != 2 || keyIn(f.threads[0].body) == keyIn(f.threads[1].body) {
		t.Fatalf("posted %d comments with the keys %q and %q, want 2 with different keys", res.Posted, keyIn(f.threads[0].body), keyIn(f.threads[1].body))
	}
	if res := Run(ctx, o, []Bundle{b}, files); res.Posted != 0 || res.Resolved != 0 {
		t.Fatalf("a second push with the same findings: posted %d, resolved %d", res.Posted, res.Resolved)
	}

	// The author fixes line 5.
	b.Files = map[string][]byte{"PRD.md": []byte(strings.Replace(string(src), "must", "MUST", 1))}
	b.Findings = []api.Finding{twin(2)}
	Run(ctx, o, []Bundle{b}, files)
	var open []int
	for _, th := range f.threads {
		if !th.resolved {
			open = append(open, th.line)
		}
	}
	if !slices.Equal(open, []int{6}) {
		t.Errorf("open comments on lines %v, want one on line 6", open)
	}
}

// A pull request with more than 100 review threads: Speccy sees them all, so it resolves the
// ones whose findings are gone and reads a reply command past the 100th comment of a thread.
func TestAction_SeesEveryThreadAndReply(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	for i := range 130 {
		f.threads = append(f.threads, thread{id: fmt.Sprintf("T%d", i+1), body: fmt.Sprintf("old %s%016x -->", keyMarker, i)})
	}
	for range 120 {
		f.threads[0].replies = append(f.threads[0].replies, github.Reply{Author: "ana", Body: "A note."})
	}
	f.threads[0].replies = append(f.threads[0].replies, github.Reply{Author: "ana", Body: "/speccy waive The provider sets this limit."})
	threads, err := o.GitHub.ReviewThreads(ctx, o.Repo, o.PR)
	if err != nil {
		t.Fatal(err)
	}
	if cmds := Commands(threads); len(threads) != 130 || len(cmds) != 1 || cmds[0].Kind != "waive" {
		t.Fatalf("%d threads and the commands %+v, want 130 threads and the waive command", len(threads), cmds)
	}
	if res := Run(ctx, o, nil, nil); res.Resolved != 130 {
		t.Errorf("resolved %d threads whose findings are gone, want 130", res.Resolved)
	}
}

// The verify gate posts a breach once. A second run on the same pull request adds no second
// comment, and a breach that is gone has its comment resolved.
func TestVerify_BreachCommentOnce(t *testing.T) {
	ctx := context.Background()
	f, o := newPR(t)
	holds, line := true, 12
	b := VerifyBundle{Slug: "refunds", Run: api.Verification{Outcomes: []api.VerificationOutcome{{
		TraceId: "REQ-001", Outcome: api.Breached,
		Targets: []api.VerificationTarget{{Kind: api.Code, Path: "pay/refund.go", Line: &line, Holds: &holds, Quote: "func Refund("}},
	}}}}
	files := []github.PRFile{{Filename: "pay/refund.go", Patch: patchFor(12)}}
	if res := RunVerify(ctx, o, []VerifyBundle{b}, files); res.Posted != 1 {
		t.Fatalf("posted %d, want 1 (warnings %v)", res.Posted, res.Warnings)
	}
	if res := RunVerify(ctx, o, []VerifyBundle{b}, files); res.Posted != 0 || len(f.threads) != 1 {
		t.Fatalf("second run: posted %d, %d threads, want 0 and 1", res.Posted, len(f.threads))
	}
	// The review run of the same pull request leaves the verify comment alone.
	if res := Run(ctx, o, nil, nil); res.Resolved != 0 || f.threads[0].resolved {
		t.Fatalf("the review run resolved the verify comment")
	}
	b.Run.Outcomes = nil
	if res := RunVerify(ctx, o, []VerifyBundle{b}, files); res.Resolved != 1 || !f.threads[0].resolved {
		t.Errorf("after the fix: resolved %d, thread resolved %v", res.Resolved, f.threads[0].resolved)
	}
}

// A batch adds to the reviewer's pending review: a finding that already has a comment there
// gets no second one, the reviewer's own comments and text stay, and Speccy's part of the body
// replaces the part it wrote before.
func TestMerge_KeepsTheReviewersDraft(t *testing.T) {
	old := Pending(Options{Repo: "acme/specs", PR: 1, HeadSHA: "aaaaaaa"}, nil, nil)
	existing := &github.PendingReview{
		Body: "My own note.\n\n" + old.Body,
		Comments: []github.PendingComment{
			{Body: "Why a queue here?"},
			{Body: "Earlier finding\n\n" + keyMarker + "k1 -->"},
		},
	}
	next := PendingReview{Body: bodyStart + "\nSpeccy reviewed 1 spec doc at bbbbbbb.\n" + bodyEnd, Comments: []github.ReviewComment{
		{Path: "SPEC.md", Line: 3, Body: "Same finding\n\n" + keyMarker + "k1 -->"},
		{Path: "SPEC.md", Line: 9, Body: "New finding\n\n" + keyMarker + "k2 -->"},
	}}
	add, _, body, _ := Merge(existing, next, nil)
	if len(add) != 1 || add[0].Line != 9 {
		t.Errorf("added %+v, want only the comment of the new finding", add)
	}
	if !strings.HasPrefix(body, "My own note.") {
		t.Errorf("the reviewer's text is gone: %q", body)
	}
	if strings.Count(body, bodyStart) != 1 || !strings.Contains(body, "bbbbbbb") || strings.Contains(body, "aaaaaaa") {
		t.Errorf("Speccy's part was not replaced: %q", body)
	}
}

// A comment leads with what the author must do: the question of an answer finding, or the
// fix of a reword finding. The level and the check go last, with a link to the Check catalog.
func TestCommentBody_LeadsWithTheAsk(t *testing.T) {
	q, fix := "What happens to a payment when the third retry fails?", "Write must in capitals."
	answer := commentBody(api.Finding{CheckSlug: "divergence.gap", Level: api.FindingLevelMUST, Message: "No reader found an answer.",
		Question: &q, FixKind: api.Answer}, "", false, false)
	if !strings.HasPrefix(answer, q) {
		t.Errorf("the answer finding does not lead with its question: %q", answer)
	}
	if !strings.HasSuffix(answer, "<sub>MUST · [`divergence.gap`](https://speccy-docs.pages.dev/reference/checks/#divergence.gap)</sub>") {
		t.Errorf("the last line is not the level and the linked check: %q", answer)
	}
	reword := commentBody(api.Finding{CheckSlug: "lint.rfc2119-case", Level: api.FindingLevelSHOULD, Message: "Lower-case must.",
		Fix: &fix, FixKind: api.Reword}, "The service MUST retry.", true, false)
	if !strings.HasPrefix(reword, fix+"\n\n```suggestion\nThe service MUST retry.\n```") {
		t.Errorf("the reword finding does not lead with its fix: %q", reword)
	}
	custom := commentBody(api.Finding{CheckSlug: "acme.owner", Level: api.FindingLevelMUST, Message: "No owner."}, "", false, false)
	if !strings.HasSuffix(custom, "<sub>MUST · `acme.owner`</sub>") {
		t.Errorf("a check with no catalog entry has a link: %q", custom)
	}
}

// A batch that ran every stage removes Speccy's comments whose finding is gone. It keeps the
// reviewer's questions and comments, the comments of a bundle whose review failed, and every
// comment after a review that left a stage out.
func TestMerge_RemovesCommentsOfGoneFindings(t *testing.T) {
	b := Bundle{Slug: "docs/pay", Dir: "docs", MainDoc: "pay.md", Kind: "full", Verdict: "not_build_ready",
		Findings: []api.Finding{{CheckSlug: "sdd.non-goals", Level: api.FindingLevelMUST, Message: "No non-goals."}},
		Files:    map[string][]byte{"pay.md": []byte("# Pay\n")}}
	live := findingKeys(b)[0]
	existing := &github.PendingReview{Comments: []github.PendingComment{
		{ID: "live", Path: "docs/pay.md", Body: "x\n\n" + keyMarker + live + " -->"},
		{ID: "gone", Path: "docs/pay.md", Body: "y\n\n" + keyMarker + "0000000000000000 -->"},
		{ID: "other", Path: "specs/other.md", Body: "z\n\n" + keyMarker + "1111111111111111 -->"},
		{ID: "ask", Path: "docs/pay.md", Body: AskComment("Who owns retries?", "abc", false)},
		{ID: "mine", Path: "docs/pay.md", Body: "My own note."},
	}}
	stale := func(o Options, bundles ...Bundle) []string {
		_, _, _, out := Merge(existing, Pending(o, bundles, nil), nil)
		var ids []string
		for _, c := range out {
			ids = append(ids, c.ID)
		}
		return ids
	}
	if got := stale(Options{Prune: true}, b); !slices.Equal(got, []string{"gone"}) {
		t.Errorf("removed %v, want only the comment of the gone finding", got)
	}
	if got := stale(Options{}, b); len(got) != 0 {
		t.Errorf("a review that left a stage out removed %v", got)
	}
	failed := b
	failed.Error, failed.Findings = "The review failed.", nil
	if got := stale(Options{Prune: true}, failed); len(got) != 0 {
		t.Errorf("a failed review removed %v", got)
	}
}

// #140: a pending review with no attribution names no tool, and a later round still finds its
// comments and its part of the body through the marks that the local state keeps.
func TestPending_PlainMergesByMarks(t *testing.T) {
	src := doc()
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md", Kind: "full", Verdict: "not_build_ready",
		Findings: []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelMUST, "TBD")}, Files: map[string][]byte{"PRD.md": src}}
	files := []github.PRFile{{Filename: "docs/refunds/PRD.md", Patch: patchFor(8)}}
	o := Options{Repo: "acme/specs", PR: 7, HeadSHA: "aaaaaaa", Unattributed: true, Prune: true}
	first := Pending(o, []Bundle{b}, files)
	text := first.Body
	for _, c := range first.Comments {
		text += c.Body
	}
	if len(first.Comments) != 1 || strings.Contains(strings.ToLower(text), "speccy") || strings.Contains(text, "<!--") {
		t.Fatalf("a plain review names the tool or holds a marker:\n%s", text)
	}
	posted := &github.PendingReview{ID: "R", Body: "My note.\n\n" + first.Body, Comments: []github.PendingComment{
		{ID: "C1", Path: first.Comments[0].Path, Line: first.Comments[0].Line, Body: first.Comments[0].Body},
		{ID: "C2", Path: "docs/refunds/PRD.md", Line: 9, Body: "Why a queue here?"},
	}}
	marks := Posted(posted, first)
	o.HeadSHA = "bbbbbbb"
	add, _, body, stale := Merge(posted, Pending(o, []Bundle{b}, files), marks)
	if len(add) != 0 || len(stale) != 0 {
		t.Errorf("added %d and removed %d comments; want none, the finding has its comment", len(add), len(stale))
	}
	if !strings.HasPrefix(body, "My note.") || !strings.Contains(body, "bbbbbbb") || strings.Contains(body, "aaaaaaa") {
		t.Errorf("the earlier part of the body was not replaced: %q", body)
	}
	if BySpeccy(posted.Comments[1], marks) || !BySpeccy(posted.Comments[0], marks) {
		t.Error("the marks do not tell the reviewer's comment from the finding's")
	}
}

// #131: with should in the levels, a SHOULD finding on a changed line goes inline although it
// has no suggestion. By default it stays in the report.
func TestCandidates_ShouldLevel(t *testing.T) {
	src := doc()
	b := Bundle{Slug: "docs/refunds", Dir: "docs/refunds", MainDoc: "PRD.md",
		Findings: []api.Finding{finding(src, 8, "lint.placeholder", api.FindingLevelSHOULD, "TBD")}, Files: map[string][]byte{"PRD.md": src}}
	changed := map[string]map[int]bool{"docs/refunds/PRD.md": {8: true}}
	if inline, _ := candidates(b, changed, nil, false); len(inline) != 0 {
		t.Errorf("by default %d SHOULD comments went inline; want none", len(inline))
	}
	if inline, _ := candidates(b, changed, []string{"must", "should"}, false); len(inline) != 1 {
		t.Errorf("with should, %d SHOULD comments went inline; want 1", len(inline))
	}
}
