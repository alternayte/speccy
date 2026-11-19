package prcheck_test

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alternayte/speccy/internal/features/prcheck"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const spec = "---\ntype: sdd\ntitle: Pay design\n---\n# Pay design\n\n## Context\n\nThe limit is TBD.\n"

// fakeRepo serves pull request 7 of acme/specs: the head commit c0ffee0 with its files, the
// base branch main with its own .speccy.yaml, and it records the check runs.
type fakeRepo struct {
	mu     sync.Mutex
	head   map[string]string
	base   map[string]string
	checks []map[string]any
}

func (f *fakeRepo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	blob := func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	send := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	p := strings.TrimPrefix(r.URL.Path, "/repos/acme/specs")
	switch {
	case r.URL.Path == "/graphql":
		send(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"reviewThreads": map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}}}}}})
	case p == "/pulls/7":
		send(map[string]any{"head": map[string]any{"ref": "feature", "sha": "c0ffee0", "repo": map[string]string{"full_name": "acme/specs"}},
			"base": map[string]string{"ref": "main"}})
	case p == "/pulls/7/files":
		send([]map[string]string{{"filename": "docs/SPEC.md", "status": "modified", "patch": "@@ -1,10 +1,10 @@\n+x"}})
	case strings.HasPrefix(p, "/commits/"):
		send(map[string]any{"sha": "c0ffee0", "commit": map[string]any{"tree": map[string]string{"sha": "7ree"}}})
	case p == "/git/trees/7ree":
		var tree []map[string]any
		for path, content := range f.head {
			tree = append(tree, map[string]any{"path": path, "type": "blob", "mode": "100644", "sha": blob(content), "size": len(content)})
		}
		tree = append(tree, map[string]any{"path": "docs", "type": "tree", "mode": "040000", "sha": blob("docs")})
		send(map[string]any{"tree": tree})
	case strings.HasPrefix(p, "/git/blobs/"):
		for _, content := range f.head {
			if blob(content) == strings.TrimPrefix(p, "/git/blobs/") {
				send(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
				return
			}
		}
		http.NotFound(w, r)
	case strings.HasPrefix(p, "/contents/"):
		files := f.head
		if r.URL.Query().Get("ref") == "main" {
			files = f.base
		}
		content, ok := files[strings.TrimPrefix(p, "/contents/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			send(map[string]string{"message": "Not Found"})
			return
		}
		send(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
	case p == "/check-runs":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.checks = append(f.checks, in)
		send(map[string]any{})
	case p == "/issues/7/comments" && r.Method == "GET":
		send([]any{})
	case p == "/issues/7/comments":
		send(map[string]any{"id": 1})
	default:
		send(map[string]any{})
	}
}

func run(t *testing.T, f *fakeRepo) {
	t.Helper()
	ctx := context.Background()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	db := storetest.Engines()[0].Open(t)
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.LoadLocal("")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := profile.Record(ctx, db, ws, loaded, "local")
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := queue.Open(ctx, db, queue.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client := func(context.Context, string) (*github.Client, error) {
		return &github.Client{API: srv.URL, Token: "t"}, nil
	}
	profiles := func() map[string]profile.Versioned { return versions }
	reviews := &review.Service{DB: db, Workspace: ws, Profiles: profiles, GitHub: client, Queue: jobs,
		Gateway: &model.Gateway{DB: db, Workspace: ws}}
	s := &prcheck.Service{Reviews: reviews, API: &review.API{DB: db, Workspace: ws, Service: reviews}, Queue: jobs,
		GitHub: client, Profiles: profiles}
	s.Register()
	if err := s.Enqueue(ctx, prcheck.Event{API: srv.URL, Repo: "acme/specs", Pull: 7, SHA: "c0ffee0"}); err != nil {
		t.Fatal(err)
	}
	if ran, err := jobs.RunNext(ctx); !ran || err != nil {
		t.Fatalf("run the job: ran %v, err %v", ran, err)
	}
}

// The gate keys come from the base branch: a pull request that sets advisory still fails a
// Not Build Ready check while main says blocking.
func TestAPullRequestCannotRelaxItsOwnGate(t *testing.T) {
	f := &fakeRepo{
		head: map[string]string{".speccy.yaml": "enforcement: advisory\n", "docs/SPEC.md": spec},
		base: map[string]string{".speccy.yaml": "enforcement: blocking\n"},
	}
	run(t, f)
	if len(f.checks) != 1 {
		t.Fatalf("%d check runs, want 1: %v", len(f.checks), f.checks)
	}
	if f.checks[0]["conclusion"] != "failure" || f.checks[0]["head_sha"] != "c0ffee0" {
		t.Errorf("check run %v, want a failure on c0ffee0", f.checks[0])
	}
}

// mode: action on the base branch leaves the repo to the Action: the App posts nothing.
func TestModeActionSkipsTheApp(t *testing.T) {
	f := &fakeRepo{
		head: map[string]string{".speccy.yaml": "mode: action\n", "docs/SPEC.md": spec},
		base: map[string]string{".speccy.yaml": "mode: action\n"},
	}
	run(t, f)
	if len(f.checks) != 0 {
		t.Errorf("%d check runs, want none", len(f.checks))
	}
}

// The other way round: a pull request cannot make its own gate blocking either. Main says
// advisory, so the check is neutral.
func TestTheBaseBranchSetsEnforcement(t *testing.T) {
	f := &fakeRepo{
		head: map[string]string{".speccy.yaml": "enforcement: blocking\n", "docs/SPEC.md": spec},
		base: map[string]string{".speccy.yaml": "enforcement: advisory\n"},
	}
	run(t, f)
	if len(f.checks) != 1 || f.checks[0]["conclusion"] != "neutral" {
		t.Errorf("check runs %v, want one neutral check", f.checks)
	}
}
