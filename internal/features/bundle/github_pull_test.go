package bundle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/findingkey"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// fakePull serves one pull request over fakeGitHub: its state, its files, its head commit and
// its review threads.
type fakePull struct {
	*fakeGitHub
	state   string
	files   []string
	threads []map[string]any
}

func (f *fakePull) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	p := strings.TrimPrefix(r.URL.Path, "/repos/acme/specs")
	f.mu.Lock()
	head := f.refs["feature/pay"]
	f.mu.Unlock()
	switch {
	case p == "/pulls/42":
		state, merged := f.state, false
		if state == "merged" {
			state, merged = "closed", true
		}
		send(map[string]any{"title": "Payments PRD and SDD", "html_url": "https://github.com/acme/specs/pull/42", "state": state,
			"merged": merged, "user": map[string]any{"login": "pat", "id": 77},
			"head": map[string]any{"ref": "feature/pay", "sha": head, "repo": map[string]string{"full_name": "acme/specs"}},
			"base": map[string]any{"ref": "main"}})
	case strings.HasPrefix(p, "/pulls/42/files"):
		var out []map[string]string
		for _, name := range f.files {
			out = append(out, map[string]string{"filename": name, "status": "added", "patch": "@@ -0,0 +1,20 @@\n+a"})
		}
		send(out)
	case strings.HasPrefix(p, "/commits/"):
		send(map[string]any{"sha": head, "commit": map[string]any{"tree": map[string]string{"sha": sha("tree" + head)}}})
	case strings.HasPrefix(p, "/contents/") && r.URL.Query().Get("ref") == head:
		name, _ := url.PathUnescape(strings.TrimPrefix(p, "/contents/"))
		f.mu.Lock()
		content, ok := f.commits[head][name]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		send(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(content)), "encoding": "base64"})
	case r.URL.Path == "/graphql":
		send(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"reviewThreads": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": f.threads}}}}})
	default:
		f.fakeGitHub.ServeHTTP(w, r)
	}
}

// docs/specs/pr-source-and-github-review.md: a pull request URL makes a source with the docs a
// person picked. Each one is a bundle of its own, kept apart from the base branch by its slug,
// with the pull request's author as the author and the person who pasted it as a reviewer. A
// reply to Speccy's comment lands on its doc. On merge, a doc another source holds on the base
// branch becomes read-only and points there; the other doc follows the base branch.
func TestPullSource(t *testing.T) {
	ctx := as(context.Background(), "user-admin")
	s := services()[0].open(t)
	gh := &fakeGitHub{refs: map[string]string{}, commits: map[string]map[string]string{}, blobs: map[string]string{}, trees: map[string]map[string]string{}}
	prd := "---\ntype: prd\n---\n" + mainDoc
	gh.refs["main"] = gh.commit(map[string]string{"docs/prd.md": prd})
	headFiles := map[string]string{
		"docs/prd.md":   prd + "\nMore.\n",
		"docs/sdd.md":   "---\ntype: sdd\n---\n# Payments SDD\n\n## Design\n\nThe service stores refunds.\n",
		"docs/notes.md": "# Notes\n",
	}
	gh.refs["feature/pay"] = gh.commit(headFiles)
	fake := &fakePull{fakeGitHub: gh, state: "open", files: []string{"docs/prd.md", "docs/sdd.md", "docs/notes.md"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s.GitHub = func(context.Context, string) (*github.Client, error) {
		return &github.Client{API: srv.URL, Token: "t"}, nil
	}
	s.UserOfGitHub = func(_ context.Context, id string) (string, bool, error) { return "user-pat", id == "77", nil }
	a := sourceAPI(t, s)
	q := s.DB.Queries()

	// The base branch already has a source, which holds the PRD.
	if _, err := a.AddGithubSource(ctx, api.AddGithubSourceRequestObject{Body: &api.AddGithubSourceJSONRequestBody{Url: srv.URL + "/acme/specs/tree/main/docs"}}); err != nil {
		t.Fatal(err)
	}
	mainPRD, ok := docAt(t, s, false, "prd.md")
	if !ok {
		t.Fatal("the base branch source did not make the PRD")
	}

	picks := []api.PullDocChoice{{Path: "docs/prd.md", Profile: "prd"}, {Path: "docs/sdd.md", Profile: "sdd"}, {Path: "docs/notes.md"}}
	res, err := a.AddGithubSource(ctx, api.AddGithubSourceRequestObject{Body: &api.AddGithubSourceJSONRequestBody{
		Url: srv.URL + "/acme/specs/pull/42", Docs: &picks}})
	if err != nil {
		t.Fatal(err)
	}
	added := res.(api.AddGithubSource200JSONResponse)
	if added.Source.Error != "" || added.Source.Pull == nil || added.Source.Pull.Author != "pat" || added.Source.Docs == nil || len(*added.Source.Docs) != 2 {
		t.Fatalf("source = %+v, want pull request #42 by pat with two docs and no error", added.Source)
	}
	prPRD, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/prd-pr-42"})
	if err != nil {
		t.Fatalf("no PRD bundle of the pull request: %v", err)
	}
	prSDD, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/sdd-pr-42"})
	if err != nil || prSDD.ProfileKey != "sdd" {
		t.Fatalf("SDD bundle %+v, %v; want the sdd profile", prSDD, err)
	}
	if _, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: "docs/notes-pr-42"}); err == nil {
		t.Error("notes.md became a spec doc; it was marked not a spec")
	}
	if prPRD.BundleID == mainPRD.BundleID {
		t.Error("the pull request's PRD went into the base branch's bundle")
	}
	if ok, _ := q.IsBundleAuthor(ctx, pgdb.IsBundleAuthorParams{BundleID: prPRD.BundleID, UserID: "user-pat"}); !ok {
		t.Error("the author of the pull request is not the author of its bundle")
	}
	if ok, _ := q.IsBundleAuthor(ctx, pgdb.IsBundleAuthorParams{BundleID: prPRD.BundleID, UserID: "user-admin"}); ok {
		t.Error("the person who pasted the URL became an author")
	}
	if ok, _ := q.IsBundleMember(ctx, pgdb.IsBundleMemberParams{BundleID: prPRD.BundleID, UserID: "user-admin"}); !ok {
		t.Error("the person who pasted the URL is not a reviewer")
	}

	// A reply to a comment with Speccy's marker shows on its doc, by the comment's key.
	key := findingkey.Key(findingkey.DocSlug(prPRD.Slug), "lint.x", "message", "quote")
	fake.threads = []map[string]any{{"id": "T1", "isResolved": false, "path": "docs/prd.md", "line": 3, "comments": map[string]any{
		"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []map[string]any{
			{"body": "Answer this.\n\n" + findingkey.Mark(key), "databaseId": 1, "author": map[string]string{"login": "speccy[bot]"}},
			{"body": "Not needed: the gateway retries.", "databaseId": 2, "url": "https://github.com/c/2", "createdAt": "2026-11-25T10:00:00Z",
				"author": map[string]string{"login": "pat"}},
		}}}}
	src := added.Source.Id
	if err := s.SyncSource(ctx, src, true); err != nil {
		t.Fatal(err)
	}
	replies, err := q.ListGithubRepliesOfDoc(ctx, prPRD.ID)
	if err != nil || len(replies) != 1 || replies[0].FindingKey != key || replies[0].Author != "pat" {
		t.Fatalf("replies = %+v, %v; want pat's reply under the key", replies, err)
	}

	// The merge: main holds the head's files. The PRD is held on main already, so the pull
	// request's PRD becomes read-only and points at it. The SDD now follows main.
	gh.mu.Lock()
	gh.refs["main"] = gh.commit(headFiles)
	gh.mu.Unlock()
	fake.state = "merged"
	if err := s.SyncSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	prPRD, _ = q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: prPRD.ID})
	gs, _ := GitHubStateOf(ctx, q, prPRD)
	if gs.ReadOnly != ReadOnlyMerged || gs.MovedTo != mainPRD.ID {
		t.Errorf("PRD state = %+v, want read-only after the merge, moved to the base branch's PRD", gs)
	}
	if share.ReadOnly(prPRD) == "" {
		t.Error("share does not see the PRD as read-only")
	}
	if ok, _ := share.CanEdit(ctx, q, kernel.Actor{UserID: "user-admin", Role: kernel.RoleAdmin}, prPRD); ok {
		t.Error("an admin can edit the read-only PRD")
	}
	prSDD, _ = q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: prSDD.ID})
	if gs, _ := GitHubStateOf(ctx, q, prSDD); gs.ReadOnly != "" || gs.Branch != "main" {
		t.Errorf("SDD state = %+v, want live on main", gs)
	}
	// The base branch source sees the SDD on main, and makes no second bundle for it.
	srcs, _ := q.ListGithubSources(ctx, s.Workspace)
	for _, o := range srcs {
		if o.Pull == 0 {
			if err := s.SyncSource(ctx, o.ID, true); err != nil {
				t.Fatalf("the base branch source failed: %v", err)
			}
		}
	}
	if _, ok := docAt(t, s, false, "sdd.md"); ok {
		t.Error("the base branch source made a second bundle for the merged SDD")
	}
}

// docAt is the live spec doc at path of the branch source, or of the pull request source.
func docAt(t *testing.T, s *Service, pull bool, docPath string) (pgdb.SpecDoc, bool) {
	t.Helper()
	ctx := context.Background()
	q := s.DB.Queries()
	srcs, err := q.ListGithubSources(ctx, s.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range srcs {
		if (src.Pull > 0) != pull {
			continue
		}
		docs, err := s.sourceSpecDocs(ctx, q, src.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range docs {
			if d.DocPath == docPath {
				return d, true
			}
		}
	}
	return pgdb.SpecDoc{}, false
}
