package review_test

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// openPull is one open pull request of the fake repo: its head commit, when it last changed,
// and the files it adds at that commit.
type openPull struct {
	sha     string
	updated time.Time
	files   map[string]string
}

// fakeRepo serves the GitHub API of a repo with one reviewed pull request (#9, whose head tree
// is tree) and other open pull requests. A test changes it between reviews.
type fakeRepo struct {
	mu    sync.Mutex
	tree  map[string]string
	pulls map[int]*openPull
	// lists counts the reads of the open pull request list.
	lists int
}

func (f *fakeRepo) serve(t *testing.T) *httptest.Server {
	t.Helper()
	blob := func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/repos/acme/specs")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case p == "/pulls/9":
			_, _ = w.Write([]byte(`{"head":{"ref":"sdd","sha":"5dd0000","repo":{"full_name":"acme/specs"}},"base":{"ref":"main"}}`))
		case p == "/pulls/9/files":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"filename": "docs/sdd/pay.md", "status": "added", "patch": "@@ -0,0 +1 @@\n+x"}})
		case p == "/pulls" && r.URL.Query().Get("state") == "open":
			f.lists++
			out := []map[string]any{{"number": 9, "draft": false, "html_url": "https://github.com/acme/specs/pull/9",
				"updated_at": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "head": map[string]string{"sha": "5dd0000"}}}
			for n, pl := range f.pulls {
				out = append(out, map[string]any{"number": n, "draft": true, "html_url": "https://github.com/acme/specs/pull/" + strconv.Itoa(n),
					"updated_at": pl.updated, "head": map[string]string{"sha": pl.sha}})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/pulls/") && strings.HasSuffix(p, "/files"):
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(p, "/pulls/"), "/files"))
			out := []map[string]string{}
			if pl, ok := f.pulls[n]; ok {
				for name := range pl.files {
					out = append(out, map[string]string{"filename": name, "status": "added"})
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/contents/"):
			name, ref := strings.TrimPrefix(p, "/contents/"), r.URL.Query().Get("ref")
			for _, pl := range f.pulls {
				if content, ok := pl.files[name]; ok && pl.sha == ref {
					_ = json.NewEncoder(w).Encode(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
					return
				}
			}
			http.NotFound(w, r)
		case strings.HasPrefix(p, "/commits/"):
			_, _ = w.Write([]byte(`{"sha":"5dd0000","commit":{"tree":{"sha":"7ree"}}}`))
		case p == "/git/trees/7ree":
			var tree []map[string]any
			dirs := map[string]bool{}
			for path, content := range f.tree {
				tree = append(tree, map[string]any{"path": path, "type": "blob", "mode": "100644", "sha": blob(content), "size": len(content)})
				for d := path; strings.Contains(d, "/"); {
					d = d[:strings.LastIndex(d, "/")]
					if !dirs[d] {
						dirs[d] = true
						tree = append(tree, map[string]any{"path": d, "type": "tree", "mode": "040000", "sha": blob(d)})
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": tree})
		case strings.HasPrefix(p, "/git/blobs/"):
			for _, content := range f.tree {
				if blob(content) == strings.TrimPrefix(p, "/git/blobs/") {
					_ = json.NewEncoder(w).Encode(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const (
	pullRule = "link_rules:\n  - \"docs/sdd/{name}.md implements docs/prd/{name}.md\"\n"
	pullSDD  = "---\ntype: sdd\ntitle: Pay design\n---\n# Pay design\n\n## Context\n\nOne payment per order.\n"
)

func pullPRD(reqs ...string) string {
	var b strings.Builder
	b.WriteString("---\ntype: prd\ntitle: Pay\n---\n# Pay\n\n## Requirements\n\n")
	for _, r := range reqs {
		b.WriteString("- **" + r + ":** A customer pays once.\n")
	}
	return b.String()
}

// prdLink returns the implements link of the review of the one doc of res.
func prdLink(t *testing.T, res review.URLReview) api.BundleLink {
	t.Helper()
	if len(res.Docs) != 1 || res.Docs[0].Err != nil {
		t.Fatalf("review = %+v", res)
	}
	for _, l := range res.Docs[0].Result.Links {
		if l.Kind == "implements" && l.TargetRef == "docs/prd/pay.md" {
			return l
		}
	}
	t.Fatalf("links %+v: want the implements link to docs/prd/pay.md", res.Docs[0].Result.Links)
	return api.BundleLink{}
}

func coverageOf(res review.URLReview) string {
	var out []string
	for _, f := range res.Docs[0].Result.Findings {
		if f.CheckSlug == review.CoverageSlug || f.CheckSlug == review.HasUpstreamSlug {
			out = append(out, f.CheckSlug+": "+f.Message)
		}
	}
	return strings.Join(out, "\n")
}

// #142: a link rule target that only an open pull request holds is read at that pull
// request's head commit. The newest of several wins and a note names the others; the next
// review reads a new head; a doc in the tree wins over a pull request.
func TestReviewURL_UpstreamFromOpenPull(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC) }
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			// Each engine gets its own repo, because the test changes it.
			repo := &fakeRepo{
				tree: map[string]string{".speccy.yaml": pullRule, "docs/sdd/pay.md": pullSDD},
				pulls: map[int]*openPull{
					// #4 has the higher update time, so it wins over #6 although its number is lower.
					4: {sha: "aaaa111", updated: day(5), files: map[string]string{"docs/prd/pay.md": pullPRD("REQ-001")}},
					6: {sha: "bbbb222", updated: day(2), files: map[string]string{"docs/prd/pay.md": pullPRD("REQ-001", "REQ-007")}},
					8: {sha: "cccc333", updated: day(9), files: map[string]string{"docs/other.md": "# Other\n"}},
				},
			}
			gh := repo.serve(t)
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{"local/SPEC.md": "---\ntype: sdd\n---\n# Local\n"})
			en.reviews.GitHub = func(context.Context, string) (*github.Client, error) {
				return &github.Client{API: gh.URL, Token: "t"}, nil
			}
			review1 := func() review.URLReview {
				t.Helper()
				res, err := en.reviews.ReviewURL(ctx, "https://github.com/acme/specs/pull/9", review.Stages{})
				if err != nil {
					t.Fatal(err)
				}
				return res
			}

			// The newest pull request that adds the PRD gives the link, and the coverage check
			// reads that PRD. A note names the other pull request.
			repo.mu.Lock()
			repo.lists = 0
			repo.mu.Unlock()
			res := review1()
			l := prdLink(t, res)
			if l.Pull == nil || l.Pull.Number != 4 || l.Pull.Sha != "aaaa111" || l.Bundle != nil {
				t.Fatalf("link %+v pull %+v: want pull request #4 at aaaa111", l, l.Pull)
			}
			cov := coverageOf(res)
			if strings.Contains(cov, review.HasUpstreamSlug) || !strings.Contains(cov, "REQ-001") || strings.Contains(cov, "REQ-007") {
				t.Errorf("findings:\n%s\nwant coverage of REQ-001 from #4, and no has-upstream finding", cov)
			}
			notes := strings.Join(res.Docs[0].Result.Notes, "\n")
			if !strings.Contains(notes, "pull request #4 at aaaa111") || !strings.Contains(notes, "#6") || strings.Contains(notes, "#8") {
				t.Errorf("notes:\n%s\nwant the pull request and its commit, and #6 as the other match", notes)
			}
			if repo.lists != 1 {
				t.Errorf("the review listed the open pull requests %d times; want 1", repo.lists)
			}

			// A new commit on #6 makes it the newest: the next review reads it.
			repo.mu.Lock()
			repo.pulls[6].sha, repo.pulls[6].updated = "dddd444", day(7)
			repo.mu.Unlock()
			res = review1()
			if l := prdLink(t, res); l.Pull == nil || l.Pull.Number != 6 || l.Pull.Sha != "dddd444" {
				t.Fatalf("after a push to #6: link pull %+v; want #6 at dddd444", l.Pull)
			}
			if cov := coverageOf(res); !strings.Contains(cov, "REQ-007") {
				t.Errorf("findings after the push:\n%s\nwant coverage of REQ-007 from the new head", cov)
			}

			// The PRD merges: the doc in the tree wins, and the link names no pull request.
			repo.mu.Lock()
			repo.tree["docs/prd/pay.md"] = pullPRD("REQ-001")
			repo.mu.Unlock()
			res = review1()
			if l := prdLink(t, res); l.Pull != nil {
				t.Fatalf("with the PRD in the tree: link pull %+v; want none", l.Pull)
			}
			if notes := strings.Join(res.Docs[0].Result.Notes, "\n"); strings.Contains(notes, "pull request #") {
				t.Errorf("notes with the PRD in the tree:\n%s\nwant no pull request", notes)
			}
			repo.mu.Lock()
			delete(repo.tree, "docs/prd/pay.md")
			repo.mu.Unlock()
		})
	}
}

// #142: in the CI Action, a lint run of a doc of the checkout reads a link rule target from an
// open pull request, stores the link with the pull request, and a later pass keeps it.
func TestLint_FolderRepoUpstreamFromOpenPull(t *testing.T) {
	repo := &fakeRepo{
		tree: map[string]string{},
		pulls: map[int]*openPull{
			4: {sha: "aaaa111", updated: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), files: map[string]string{"docs/prd/pay.md": pullPRD("REQ-001")}},
		},
	}
	gh := repo.serve(t)
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			en := newEnv(t, e, map[string]string{".speccy.yaml": pullRule, "docs/sdd/pay.md": pullSDD})
			en.reviews.FolderRepo = &review.FolderRepo{Client: &github.Client{API: gh.URL, Token: "t"}, Repo: "acme/specs", Dir: en.dir, Pull: 9}
			q := en.bundles.DB.Queries()
			docs, err := q.ListSpecDocs(ctx, pgdb.ListSpecDocsParams{WorkspaceID: en.bundles.Workspace, PageSize: 100})
			if err != nil || len(docs) != 1 {
				t.Fatalf("%d spec docs, err %v; want the SDD", len(docs), err)
			}
			b := docs[0]
			if _, err := en.reviews.Lint(ctx, b, b.CurrentVersionID.UUID); err != nil {
				t.Fatal(err)
			}
			run, fs, _ := en.latest(t, b.Slug)
			var notes []string
			_ = json.Unmarshal(run.Notes, &notes)
			if !strings.Contains(strings.Join(notes, "\n"), "pull request #4 at aaaa111") {
				t.Errorf("run notes %q: want the pull request and its commit", notes)
			}
			for _, f := range fs {
				if f.CheckSlug == review.HasUpstreamSlug {
					t.Errorf("has-upstream finding %q; the PRD of pull request #4 is the upstream doc", f.Message)
				}
			}
			links, err := q.ListLinksFrom(ctx, b.ID)
			if err != nil || len(links) != 1 || links[0].PullNumber != 4 || links[0].PullSha != "aaaa111" || links[0].TargetSpecDocID.Valid {
				t.Fatalf("stored links %+v, err %v; want one link with pull request #4 and no saved target", links, err)
			}
			// A pass that reads no pull request keeps the run and the stored link.
			if err := en.reviews.EnsureLinted(ctx); err != nil {
				t.Fatal(err)
			}
			if again, _, _ := en.latest(t, b.Slug); again.ID != run.ID {
				t.Errorf("the pass linted again; the link from the pull request must stand")
			}
		})
	}
}
