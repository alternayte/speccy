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
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
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
	case strings.HasPrefix(p, "/contents/") && r.Method == "GET":
		name, _ := url.PathUnescape(strings.TrimPrefix(p, "/contents/"))
		files := f.commits[f.refs[r.URL.Query().Get("ref")]]
		content, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		send(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(content)), "encoding": "base64"})
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
			s.GitHub = func(context.Context, string) (*github.Client, error) {
				return &github.Client{API: srv.URL, Token: "t"}, nil
			}
			q := s.DB.Queries()
			src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: "acme/specs", Branch: "main", Path: "docs",
				CreatedBy: "user-1", CreatedAt: time.Now().UTC()}
			if err := q.InsertGithubSource(ctx, src); err != nil {
				t.Fatal(err)
			}
			if err := s.SyncSource(ctx, src.ID, false); err != nil {
				t.Fatal(err)
			}
			b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			if err != nil {
				t.Fatalf("the bundle under docs was not read: %v", err)
			}
			if _, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "other/x"}); err == nil {
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
			b, _ = q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
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
			b, _ = q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
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
			b, _ = q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
			if state, _ = GitHubStateOf(ctx, q, b); !state.Draft || !state.Ahead {
				t.Fatalf("GitHub moved under a draft: %+v", state)
			}
			if _, err := s.DiscardDraft(ctx, b.ID, "user-1"); err != nil {
				t.Fatal(err)
			}
			b, _ = q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"})
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

// as is ctx with a signed-in member.
func as(ctx context.Context, user string) context.Context {
	return kernel.WithActor(ctx, kernel.Actor{UserID: user, Role: kernel.RoleMember})
}

// sourceAPI is the bundle API over s, with the built-in profiles.
func sourceAPI(t *testing.T, s *Service) *API {
	t.Helper()
	loaded, err := profile.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	ps := map[string]profile.Versioned{}
	for _, l := range loaded {
		ps[l.Profile.Key] = profile.Versioned{Loaded: l, Version: 1}
	}
	return &API{Service: s, Profiles: func() map[string]profile.Versioned { return ps }}
}

// REQ-128: a source URL that names one doc makes a source for the doc's folder. The profile the
// person picked becomes the doc's adopted type, and the response names the doc to open. A second
// URL in the same folder makes no source: it says "Already added".
func TestGitHubSource_OneDoc(t *testing.T) {
	ctx := as(context.Background(), "user-1")
	s := services()[0].open(t)
	gh := &fakeGitHub{refs: map[string]string{}, commits: map[string]map[string]string{}, blobs: map[string]string{}, trees: map[string]map[string]string{}}
	gh.refs["main"] = gh.commit(map[string]string{
		"docs/prd-payments.md":               "# Payments\n\n## Requirements\n\n- The system must refund a card payment.\n",
		"docs/prd-payments.assets/limits.md": "Limits.\n",
		"docs/sdd-payments.md":               mainDoc,
		"docs/pay/SPEC.md":                   mainDoc,
	})
	srv := httptest.NewServer(gh)
	defer srv.Close()
	s.GitHub = func(context.Context, string) (*github.Client, error) {
		return &github.Client{API: srv.URL, Token: "t"}, nil
	}
	a := sourceAPI(t, s)
	prd := "prd"
	res, err := a.AddGithubSource(ctx, api.AddGithubSourceRequestObject{Body: &api.AddGithubSourceJSONRequestBody{
		Url: srv.URL + "/acme/specs/blob/main/docs/prd-payments.md", Profile: &prd}})
	if err != nil {
		t.Fatal(err)
	}
	added := res.(api.AddGithubSource200JSONResponse)
	if added.AlreadyAdded || added.Source.Path != "docs" || added.Source.Error != "" {
		t.Fatalf("source = %+v, want a new source for docs with no error", added)
	}
	q := s.DB.Queries()
	d, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/prd-payments"})
	if err != nil {
		t.Fatalf("the doc did not become a spec doc: %v", err)
	}
	if d.ProfileKey != "prd" || added.DocId == nil || *added.DocId != d.ID || added.BundleId == nil || *added.BundleId != d.BundleID {
		t.Errorf("spec doc %+v, response %+v; want the prd doc to open", d, added)
	}
	// The folder is one bundle with both spec docs; its subfolder with its own doc is another.
	docs, _ := q.ListSpecDocsOfBundle(ctx, d.BundleID)
	if len(docs) != 2 {
		t.Errorf("the docs bundle holds %d spec docs, want 2", len(docs))
	}
	if _, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/pay"}); err != nil {
		t.Errorf("docs/pay is not a bundle of the source: %v", err)
	}

	res, err = a.AddGithubSource(ctx, api.AddGithubSourceRequestObject{Body: &api.AddGithubSourceJSONRequestBody{
		Url: srv.URL + "/acme/specs/blob/main/docs/sdd-payments.md"}})
	if err != nil {
		t.Fatal(err)
	}
	again := res.(api.AddGithubSource200JSONResponse)
	if !again.AlreadyAdded || again.Source.Id != added.Source.Id || again.DocId == nil {
		t.Errorf("second URL = %+v, want Already added with the SDD to open", again)
	}
	srcs, _ := q.ListGithubSources(ctx, s.Workspace)
	if len(srcs) != 1 {
		t.Errorf("%d sources, want 1", len(srcs))
	}
}

// TestGitHubSource_AdoptedType pins that a type accepted in Speccy makes a bundle with no
// commit to the repo, and that the repo's own answer replaces it later (REQ-133).
func TestGitHubSource_AdoptedType(t *testing.T) {
	ctx := context.Background()
	s := services()[0].open(t)
	gh := &fakeGitHub{refs: map[string]string{}, commits: map[string]map[string]string{}, blobs: map[string]string{}, trees: map[string]map[string]string{}}
	gh.refs["main"] = gh.commit(map[string]string{
		"docs/prd-payments.md": "# Payments\n\n## Requirements\n\n- The system must refund a card payment.\n",
		"README.md":            "# The repo\n",
	})
	srv := httptest.NewServer(gh)
	defer srv.Close()
	s.GitHub = func(context.Context, string) (*github.Client, error) {
		return &github.Client{API: srv.URL, Token: "t"}, nil
	}
	q := s.DB.Queries()
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: "acme/specs", Branch: "main",
		Path: "docs", CreatedBy: "user-1", CreatedAt: time.Now().UTC()}
	if err := q.InsertGithubSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	// The scan finds no bundle: the doc names no type and the repo maps nothing.
	if err := s.SyncSource(ctx, src.ID, false); err == nil {
		t.Fatal("a source with no typed doc synced without saying so")
	}
	row, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: src.ID})
	if err != nil {
		t.Fatal(err)
	}
	var skipped []string
	_ = json.Unmarshal(row.Skipped, &skipped)
	if len(skipped) != 1 || skipped[0] != "docs/prd-payments.md" {
		t.Fatalf("skipped = %v, want the one doc under the source", skipped)
	}
	head := gh.refs["main"]

	// Accepting the type makes the bundle, and the branch does not move.
	if err := q.SetAdoptedType(ctx, pgdb.SetAdoptedTypeParams{SourceID: src.ID, Path: "docs/prd-payments.md", Profile: "prd"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSource(ctx, src.ID, true); err != nil {
		t.Fatal(err)
	}
	b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/prd-payments"})
	if err != nil {
		t.Fatalf("the accepted doc did not become a bundle: %v", err)
	}
	if b.ProfileKey != "prd" {
		t.Errorf("bundle profile = %q, want prd", b.ProfileKey)
	}
	if gh.refs["main"] != head {
		t.Error("the source branch moved: Speccy wrote to the repo")
	}
	// The row stands: the type Speccy holds must not read as the repo's own answer.
	if rows, err := q.ListAdoptedTypes(ctx, src.ID); err != nil || len(rows) != 1 {
		t.Fatalf("adopted types = %+v, %v; want the one the person accepted", rows, err)
	}

	// The repo names the type itself. It wins, the row goes, and the bundle keeps its ID.
	gh.refs["main"] = gh.commit(map[string]string{
		"docs/prd-payments.md": "---\ntype: prd\n---\n\n# Payments\n\n## Requirements\n\n- The system must refund a card payment.\n",
		"README.md":            "# The repo\n",
	})
	if err := s.SyncSource(ctx, src.ID, true); err != nil {
		t.Fatal(err)
	}
	again, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/prd-payments"})
	if err != nil || again.ID != b.ID {
		t.Fatalf("the bundle changed when the repo named the type: %v", err)
	}
	rows, err := q.ListAdoptedTypes(ctx, src.ID)
	if err != nil || len(rows) != 0 {
		t.Errorf("adopted types = %+v, want none once the repo names the type", rows)
	}
}

// TestMappingsFor pins that a folder glob is written only when every doc the scan passed over
// in that folder was accepted. A glob must not take in a doc the person left alone.
func TestMappingsFor(t *testing.T) {
	adopted := []pgdb.AdoptedType{{Path: "notes/audit-trail.md", Profile: "sdd"}}
	// meeting.md was passed over and not accepted, so the mapping names the one doc.
	got := mappingsFor(adopted, []string{"notes/audit-trail.md", "notes/meeting.md"})
	if len(got) != 1 || got[0].Glob != "notes/audit-trail.md" || got[0].Profile != "sdd" {
		t.Fatalf("mappings = %+v, want the one doc", got)
	}
	// With every doc of the folder accepted, one glob covers it.
	adopted = append(adopted, pgdb.AdoptedType{Path: "notes/meeting.md", Profile: "sdd"})
	got = mappingsFor(adopted, []string{"notes/audit-trail.md", "notes/meeting.md"})
	if len(got) != 1 || got[0].Glob != "notes/*.md" {
		t.Fatalf("mappings = %+v, want one folder glob", got)
	}
	// Two types in one folder: one mapping each.
	adopted[1].Profile = "prd"
	got = mappingsFor(adopted, []string{"notes/audit-trail.md", "notes/meeting.md"})
	if len(got) != 2 {
		t.Fatalf("mappings = %+v, want one per doc", got)
	}
}

// TestDismissedDoc pins that a file marked as not a spec leaves the skipped list, and that
// accepting a type for it clears the mark (REQ-133).
func TestDismissedDoc(t *testing.T) {
	ctx := context.Background()
	s := services()[0].open(t)
	q := s.DB.Queries()
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: "acme/specs", Branch: "main",
		Path: "docs", CreatedBy: "user-1", CreatedAt: time.Now().UTC()}
	if err := q.InsertGithubSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertDismissedDoc(ctx, pgdb.InsertDismissedDocParams{WorkspaceID: s.Workspace,
		SourceID: uuid.NullUUID{UUID: src.ID, Valid: true}, Path: "docs/meeting.md", DismissedBy: "user-1",
		CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// A second mark of the same file is not an error, and makes no second row.
	if err := q.InsertDismissedDoc(ctx, pgdb.InsertDismissedDocParams{WorkspaceID: s.Workspace,
		SourceID: uuid.NullUUID{UUID: src.ID, Valid: true}, Path: "docs/meeting.md", DismissedBy: "user-1",
		CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListDismissedDocs(ctx, s.Workspace)
	if err != nil || len(rows) != 1 {
		t.Fatalf("dismissed docs = %+v, %v; want one", rows, err)
	}
	// A file of the served folder carries no source, and lives beside it.
	if err := q.InsertDismissedDoc(ctx, pgdb.InsertDismissedDocParams{WorkspaceID: s.Workspace,
		Path: "notes.md", DismissedBy: "user-1", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if rows, err = q.ListDismissedDocs(ctx, s.Workspace); err != nil || len(rows) != 2 {
		t.Fatalf("dismissed docs = %+v, %v; want the source file and the local file", rows, err)
	}
	// Taking the mark off the local file leaves the source's mark alone.
	if err := q.DeleteDismissedDoc(ctx, pgdb.DeleteDismissedDocParams{WorkspaceID: s.Workspace, Path: "notes.md"}); err != nil {
		t.Fatal(err)
	}
	if rows, err = q.ListDismissedDocs(ctx, s.Workspace); err != nil || len(rows) != 1 || rows[0].Path != "docs/meeting.md" {
		t.Fatalf("dismissed docs = %+v, %v; want the source file only", rows, err)
	}
}

// #67: a source that is deleted and added again brings its bundles back, with their history,
// and a source for a parent folder takes over the one it covers.
func TestGitHubSource_AddedAgainTakesOver(t *testing.T) {
	ctx := as(context.Background(), "user-1")
	s := services()[0].open(t)
	gh := &fakeGitHub{refs: map[string]string{}, commits: map[string]map[string]string{}, blobs: map[string]string{}, trees: map[string]map[string]string{}}
	gh.refs["main"] = gh.commit(map[string]string{"docs/pay/SPEC.md": mainDoc})
	srv := httptest.NewServer(gh)
	defer srv.Close()
	s.GitHub = func(context.Context, string) (*github.Client, error) {
		return &github.Client{API: srv.URL, Token: "t"}, nil
	}
	a := sourceAPI(t, s)
	q := s.DB.Queries()
	add := func(u string) api.AddedSource {
		t.Helper()
		res, err := a.AddGithubSource(ctx, api.AddGithubSourceRequestObject{Body: &api.AddGithubSourceJSONRequestBody{Url: srv.URL + u}})
		if err != nil {
			t.Fatal(err)
		}
		out := api.AddedSource(res.(api.AddGithubSource200JSONResponse))
		if out.Source.Error != "" {
			t.Fatalf("add %s: %s", u, out.Source.Error)
		}
		return out
	}
	first := add("/acme/specs/tree/main/docs/pay")
	d := head(t, s, "docs/pay")
	if _, err := a.DeleteGithubSource(ctx, api.DeleteGithubSourceRequestObject{SourceId: first.Source.Id}); err != nil {
		t.Fatal(err)
	}
	again := add("/acme/specs/tree/main/docs/pay")
	back := head(t, s, "docs/pay")
	if back.ID != d.ID || back.ArchivedAt.Valid || again.BundleId == nil || *again.BundleId != d.BundleID {
		t.Errorf("after the source came back: doc %+v, response %+v; want the same live doc", back, again)
	}

	parent := add("/acme/specs/tree/main/docs")
	if parent.AlreadyAdded {
		t.Fatal("a parent folder is not already added")
	}
	srcs, _ := q.ListGithubSources(ctx, s.Workspace)
	if len(srcs) != 1 || srcs[0].ID != parent.Source.Id {
		t.Errorf("sources = %+v, want only the parent", srcs)
	}
	if moved := head(t, s, "docs/pay"); moved.ID != d.ID || moved.ArchivedAt.Valid {
		t.Errorf("the parent did not take over docs/pay: %+v", moved)
	}
}

// #68: a sync with no GitHub credential keeps its error on the source, so the app shows it. It
// used to reach the server log only.
func TestGitHubSource_NoCredentialKeepsTheError(t *testing.T) {
	ctx := context.Background()
	s := services()[0].open(t)
	s.GitHub = func(context.Context, string) (*github.Client, error) {
		return nil, kernel.Invalid("gh_logged_out", "The gh login does not cover github.com.")
	}
	q := s.DB.Queries()
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: "acme/specs", Branch: "main",
		Path: "docs", CreatedBy: "user-1", CreatedAt: time.Now().UTC()}
	if err := q.InsertGithubSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSource(ctx, src.ID, false); err == nil {
		t.Fatal("a sync with no credential succeeded")
	}
	row, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: src.ID})
	if err != nil || !strings.Contains(row.Error, "gh login") {
		t.Errorf("source error = %q, %v; want the credential error", row.Error, err)
	}
}
