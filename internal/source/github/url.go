package github

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// Ref is what a source URL names (REQ-128).
type Ref struct {
	Host   string // the GitHub host, such as github.com
	Repo   string // owner/name
	Branch string // the branch, or "" for the repo's default branch
	Path   string // the folder, the doc, or "." for the whole repo
	File   bool   // Path names one doc, not a folder
}

// APIURL is the API of the ref's host. github.com has its own API host; GitHub Enterprise
// Server serves the API under /api/v3 of the same host.
func (r Ref) APIURL() string {
	if r.Host == "" || r.Host == "github.com" || r.Host == "www.github.com" {
		return DefaultAPI
	}
	return "https://" + r.Host + "/api/v3"
}

// ParseURL reads a source URL. It takes owner/name, github.com/owner/name,
// .../tree/<branch>/<path>, and .../blob/<branch>/<path>/doc.md, with or without a scheme,
// and the .git suffix of a clone URL (REQ-128).
func ParseURL(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Ref{}, fmt.Errorf("the address is empty")
	}
	bad := func() (Ref, error) {
		return Ref{}, fmt.Errorf("%q is not a GitHub address. Speccy takes owner/name, a repo URL, or the URL of a folder or a doc in it", raw)
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "git@"), ".git")
	host := ""
	rest := s
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return bad()
		}
		host, rest = u.Host, strings.Trim(u.Path, "/")
	case strings.Count(s, "/") >= 2 && strings.Contains(strings.SplitN(s, "/", 2)[0], "."):
		// host/owner/name, with no scheme.
		host, rest, _ = strings.Cut(s, "/")
		rest = strings.Trim(rest, "/")
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return bad()
	}
	ref := Ref{Host: host, Repo: parts[0] + "/" + parts[1], Path: "."}
	if !RepoPattern.MatchString(ref.Repo) {
		return bad()
	}
	if len(parts) == 2 {
		return ref, nil
	}
	kind := parts[2]
	if kind != "tree" && kind != "blob" {
		return bad()
	}
	if len(parts) < 4 {
		return bad()
	}
	ref.Branch = parts[3]
	if len(parts) > 4 {
		ref.Path = path.Join(parts[4:]...)
	}
	// A blob URL names one doc. A tree URL with a file name is one too, because GitHub uses
	// tree for both when it redirects.
	ref.File = kind == "blob" || (ref.Path != "." && path.Ext(ref.Path) != "")
	if ref.File && ref.Path == "." {
		return bad()
	}
	return ref, nil
}
