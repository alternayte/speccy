package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alternayte/speccy/internal/source/github"
)

// docs/specs/url-connectors.md: the header goes to the base URL's host only. A redirect to
// another host does not take it.
func TestWebConnector_HeaderStaysOnItsHost(t *testing.T) {
	var other string
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other = r.Header.Get("X-Api-Key")
		_, _ = w.Write([]byte("# Elsewhere\n"))
	}))
	defer otherSrv.Close()
	var seen string
	baseSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/away" {
			http.Redirect(w, r, otherSrv.URL+"/page", http.StatusFound)
			return
		}
		seen = r.Header.Get("X-Api-Key")
		_, _ = w.Write([]byte("# Index\n"))
	}))
	defer baseSrv.Close()
	c := &webConn{urlConn: urlConn{name: "docs", index: baseSrv.URL + "/docs/llms.txt", base: baseSrv.URL + "/docs/"},
		header: "X-Api-Key", value: "secret", client: http.DefaultClient}
	ctx := context.Background()
	if _, err := c.Fetch(ctx, c.index); err != nil || seen != "secret" {
		t.Fatalf("the base host got header %q, %v; want the secret", seen, err)
	}
	if _, err := c.Fetch(ctx, otherSrv.URL+"/page"); err != nil || other != "" {
		t.Errorf("another host got header %q, %v; want none", other, err)
	}
	if _, err := c.Fetch(ctx, baseSrv.URL+"/docs/away"); err != nil || other != "" {
		t.Errorf("a redirect to another host took header %q, %v; want none", other, err)
	}
}

// A GitHub connector maps a link under the site's base URL, and a relative link of the index,
// to files of its branch. A path with no extension tries its markdown copy.
func TestGitHubConnector_RepoPaths(t *testing.T) {
	ref, err := github.ParseURL("https://github.com/acme/platform/blob/gh-pages/docs/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	c := &githubConn{urlConn: urlConn{index: "https://github.com/acme/platform/blob/gh-pages/docs/llms.txt",
		base: "https://acme.github.io/platform/"}, ref: ref}
	for target, want := range map[string]string{
		"https://github.com/acme/platform/blob/gh-pages/docs/llms.txt":       "docs/llms.txt",
		"https://github.com/acme/platform/blob/gh-pages/docs/api/refunds.md": "docs/api/refunds.md",
		"https://acme.github.io/platform/docs/api/refunds.md":                "docs/api/refunds.md",
		"https://acme.github.io/platform/guide/":                             "guide",
	} {
		if got, ok := c.repoPath(target); !ok || got != want {
			t.Errorf("repoPath(%s) = %q, %v; want %q", target, got, ok, want)
		}
	}
	if _, ok := c.repoPath("https://elsewhere.example/x.md"); ok {
		t.Error("a link on another host maps to a file of the repo")
	}
	if !c.Owns("https://acme.github.io/platform/x.md") || !c.Owns("https://github.com/acme/platform/blob/gh-pages/docs/x.md") || c.Owns("https://github.com/acme/other/blob/main/x.md") {
		t.Error("Owns takes the site and the index folder, and nothing else")
	}
	if got := pageFiles("guide"); len(got) != 3 || got[1] != "guide.md" || got[2] != "guide/index.md" {
		t.Errorf("pageFiles(guide) = %v", got)
	}
}
