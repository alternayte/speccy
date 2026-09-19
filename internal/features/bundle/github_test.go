package bundle

import (
	"context"
	"crypto/sha1" // #nosec G505 -- git object IDs, not security
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// fakeGitHub is the part of the GitHub REST API that the source and publish use. Commits hold
// whole file sets; trees and blobs are derived from them.
type fakeGitHub struct {
	mu      sync.Mutex
	refs    map[string]string            // branch → commit
	commits map[string]map[string]string // commit → path → content
	blobs   map[string]string            // sha → content
	trees   map[string]map[string]string // tree sha → path → content
	pulls   []map[string]any
}

func sha(s string) string {
	h := sha1.Sum([]byte(s)) // #nosec G401
	return hex.EncodeToString(h[:])
}

func (f *fakeGitHub) commit(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\x00" + files[k] + "\x00")
		f.blobs[sha(files[k])] = files[k]
	}
	id := sha("commit" + b.String() + fmt.Sprint(len(f.commits)))
	f.commits[id] = files
	f.trees[sha("tree"+id)] = files
	return id
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/repos/acme/specs")
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.URL.Path == "/user":
		send(map[string]string{"login": "ada"})
	case p == "" && r.Method == "GET":
		send(map[string]string{"default_branch": "main"})
	case strings.HasPrefix(p, "/git/ref/heads/"):
		c, ok := f.refs[strings.TrimPrefix(p, "/git/ref/heads/")]
		if !ok {
			w.WriteHeader(404)
			send(map[string]string{"message": "Not Found"})
			return
		}
		send(map[string]any{"object": map[string]string{"sha": c}})
	case strings.HasPrefix(p, "/git/commits/") && r.Method == "GET":
		send(map[string]any{"tree": map[string]string{"sha": sha("tree" + strings.TrimPrefix(p, "/git/commits/"))}})
	case strings.HasPrefix(p, "/git/trees/") && r.Method == "GET":
		var tree []map[string]any
		for path, content := range f.trees[strings.TrimPrefix(p, "/git/trees/")] {
			tree = append(tree, map[string]any{"path": path, "type": "blob", "mode": "100644", "sha": sha(content), "size": len(content)})
		}
		send(map[string]any{"tree": tree})
	case strings.HasPrefix(p, "/git/blobs/") && r.Method == "GET":
		send(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(f.blobs[strings.TrimPrefix(p, "/git/blobs/")]))})
	case p == "/git/blobs" && r.Method == "POST":
		var in struct{ Content string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		raw, _ := base64.StdEncoding.DecodeString(in.Content)
		f.blobs[sha(string(raw))] = string(raw)
		send(map[string]string{"sha": sha(string(raw))})
	case p == "/git/trees" && r.Method == "POST":
		var in struct {
			BaseTree string `json:"base_tree"`
			Tree     []struct {
				Path string  `json:"path"`
				SHA  *string `json:"sha"`
			} `json:"tree"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		next := map[string]string{}
		for k, v := range f.trees[in.BaseTree] {
			next[k] = v
		}
		for _, e := range in.Tree {
			if e.SHA == nil {
				delete(next, e.Path)
			} else {
				next[e.Path] = f.blobs[*e.SHA]
			}
		}
		id := sha(fmt.Sprint("newtree", len(f.trees)))
		f.trees[id] = next
		send(map[string]string{"sha": id})
	case p == "/git/commits" && r.Method == "POST":
		var in struct {
			Tree string `json:"tree"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		id := sha(fmt.Sprint("newcommit", len(f.commits)))
		f.commits[id] = f.trees[in.Tree]
		f.trees[sha("tree"+id)] = f.trees[in.Tree]
		send(map[string]string{"sha": id})
	case p == "/git/refs" && r.Method == "POST":
		var in struct{ Ref, SHA string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.refs[strings.TrimPrefix(in.Ref, "refs/heads/")] = in.SHA
		w.WriteHeader(201)
		send(map[string]string{})
	case p == "/pulls" && r.Method == "POST":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.pulls = append(f.pulls, in)
		send(map[string]any{"number": len(f.pulls), "html_url": fmt.Sprintf("https://github.com/acme/specs/pull/%d", len(f.pulls))})
	default:
		w.WriteHeader(404)
		send(map[string]string{"message": "Not Found: " + r.Method + " " + r.URL.Path})
	}
}

// REQ-123: a GitHub source reads bundles from a repo, branch, and path; edits are drafts
// until Publish, which opens a pull request and leaves the branch as it was.
func TestGitHubSource_DraftAndPublish(t *testing.T) {
	for _, svc := range services() {
		t.Run(svc.name, func(t *testing.T) {
			ctx := context.Background()
			s := svc.open(t)
			gh := &fakeGitHub{refs: map[string]string{}, commits: map[string]map[string]string{}, blobs: map[string]string{}, trees: map[string]map[string]string{}}
			gh.refs["main"] = gh.commit(map[string]string{
				"docs/pay/SPEC.md":          mainDoc,
				"docs/pay/assets/limits.md": "Limits.\n",
				"src/main.go":               "package main\n",
				"other/x/SPEC.md":           mainDoc,
			})
			srv := httptest.NewServer(gh)
			defer srv.Close()
			s.GitHub = func(context.Context) (*github.Client, error) { return &github.Client{API: srv.URL, Token: "t"}, nil }
			q := s.DB.Queries()
			src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: "acme/specs", Branch: "main", Path: "docs",
				CreatedBy: "user-1", CreatedAt: time.Now().UTC()}
			if err := q.InsertGithubSource(ctx, src); err != nil {
				t.Fatal(err)
			}
			if err := s.SyncSource(ctx, src.ID, false); err != nil {
				t.Fatal(err)
			}
			b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			if err != nil {
				t.Fatalf("the bundle under docs was not read: %v", err)
			}
			if _, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "other/x"}); err == nil {
				t.Error("a bundle outside the source path was read")
			}
			files, _ := version.Files(ctx, q, b.CurrentVersionID.UUID)
			if len(files) != 2 || b.SourceKind != KindGitHub {
				t.Fatalf("files %v, source %s", files, b.SourceKind)
			}
			state, _ := GitHubStateOf(ctx, q, b)
			if state.Draft {
				t.Fatal("a bundle fresh from GitHub is a draft")
			}

			// An edit is a draft; a sync of an unchanged branch keeps it.
			edited := strings.Replace(mainDoc, "Retries.", "Retries, three times.", 1)
			v, _, err := s.Change(ctx, b.ID, b.CurrentVersionID.UUID, source.Op{Kind: source.OpWrite, Path: "SPEC.md", Content: []byte(edited)}, "user-1", "Edit")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SyncSource(ctx, src.ID, true); err != nil {
				t.Fatal(err)
			}
			b, _ = q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			if state, _ = GitHubStateOf(ctx, q, b); !state.Draft || b.CurrentVersionID.UUID != v.ID {
				t.Fatalf("the draft was lost: %+v", state)
			}

			base := gh.refs["main"]
			pr, err := s.Publish(ctx, b.ID, "ada@example.com", "Say how many retries")
			if err != nil {
				t.Fatal(err)
			}
			if pr.Number != 1 || gh.refs["main"] != base {
				t.Fatalf("pr %+v; main moved: %v", pr, gh.refs["main"] != base)
			}
			head := gh.pulls[0]["head"].(string)
			if got := gh.commits[gh.refs[head]]["docs/pay/SPEC.md"]; got != edited || gh.pulls[0]["base"] != "main" {
				t.Fatalf("the branch %s has %q; pull %+v", head, got, gh.pulls[0])
			}

			// The pull request merges: the next sync ends the draft, with no new version.
			gh.mu.Lock()
			gh.refs["main"] = gh.refs[head]
			gh.mu.Unlock()
			if err := s.SyncSource(ctx, src.ID, false); err != nil {
				t.Fatal(err)
			}
			b, _ = q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			if state, _ = GitHubStateOf(ctx, q, b); state.Draft || state.PR != "" || b.CurrentVersionID.UUID != v.ID {
				t.Fatalf("after the merge: %+v, version changed %v", state, b.CurrentVersionID.UUID != v.ID)
			}

			// A change on GitHub while a draft exists marks it ahead; discard takes GitHub's text.
			if _, _, err := s.Change(ctx, b.ID, v.ID, source.Op{Kind: source.OpWrite, Path: "SPEC.md", Content: []byte(edited + "\nMore.\n")}, "user-1", "Edit"); err != nil {
				t.Fatal(err)
			}
			theirs := strings.Replace(edited, "three", "five", 1)
			gh.mu.Lock()
			gh.refs["main"] = gh.commit(map[string]string{"docs/pay/SPEC.md": theirs, "docs/pay/assets/limits.md": "Limits.\n"})
			gh.mu.Unlock()
			if err := s.SyncSource(ctx, src.ID, false); err != nil {
				t.Fatal(err)
			}
			b, _ = q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			if state, _ = GitHubStateOf(ctx, q, b); !state.Draft || !state.Ahead {
				t.Fatalf("GitHub moved under a draft: %+v", state)
			}
			if _, err := s.DiscardDraft(ctx, b.ID, "user-1"); err != nil {
				t.Fatal(err)
			}
			b, _ = q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			files, _ = version.Files(ctx, q, b.CurrentVersionID.UUID)
			state, _ = GitHubStateOf(ctx, q, b)
			var spec string
			for _, f := range files {
				if f.Path == "SPEC.md" {
					spec = string(f.Content)
				}
			}
			if state.Draft || state.Ahead || spec != theirs {
				t.Fatalf("after discard: %+v, SPEC.md %q", state, spec)
			}
		})
	}
}
