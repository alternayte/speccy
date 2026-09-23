package github

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
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
	host, parts, err := split(raw)
	if err != nil {
		return Ref{}, err
	}
	bad := func() (Ref, error) {
		return Ref{}, fmt.Errorf("%q is not a GitHub address. Speccy takes owner/name, a repo URL, or the URL of a folder or a doc in it", raw)
	}
	ref := Ref{Host: host, Repo: parts[0] + "/" + parts[1], Path: "."}
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

// split reads the host and the path segments of a GitHub address. The first two segments are
// the owner and the repo name.
func split(raw string) (host string, parts []string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil, fmt.Errorf("the address is empty")
	}
	bad := fmt.Errorf("%q is not a GitHub address. Speccy takes owner/name, a repo URL, or the URL of a folder or a doc in it", raw)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "git@"), ".git")
	rest := s
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", nil, bad
		}
		host, rest = u.Host, strings.Trim(u.Path, "/")
	case strings.Count(s, "/") >= 2 && strings.Contains(strings.SplitN(s, "/", 2)[0], "."):
		// host/owner/name, with no scheme.
		host, rest, _ = strings.Cut(s, "/")
		rest = strings.Trim(rest, "/")
	}
	parts = strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" || !RepoPattern.MatchString(parts[0]+"/"+parts[1]) {
		return "", nil, bad
	}
	return host, parts, nil
}

// Build is the code a verification run reads, as a pasted URL names it: a repo, a branch, a
// commit, or a pull request.
type Build struct {
	Ref
	// SHA is the commit a commit URL names.
	SHA string
	// Pull is the number of the pull request a pull request URL names.
	Pull int
}

// shaPattern is a full or abbreviated commit SHA.
var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// ParseBuildURL reads the address of a build: anything ParseURL takes, plus
// .../commit/<sha> and .../pull/<n>. The folder or the file in the address names no scope,
// because the gate reads the whole tree.
func ParseBuildURL(raw string) (Build, error) {
	host, parts, err := split(raw)
	if err != nil {
		return Build{}, fmt.Errorf("%q is not a GitHub address. Speccy takes a repo URL, or the URL of a branch, a folder, a file, a commit or a pull request", raw)
	}
	b := Build{Ref: Ref{Host: host, Repo: parts[0] + "/" + parts[1], Path: "."}}
	if len(parts) >= 4 {
		switch parts[2] {
		case "commit":
			if !shaPattern.MatchString(parts[3]) {
				return Build{}, fmt.Errorf("%q is not a commit SHA", parts[3])
			}
			b.SHA = parts[3]
			return b, nil
		case "pull":
			n, err := strconv.Atoi(parts[3])
			if err != nil || n <= 0 {
				return Build{}, fmt.Errorf("%q is not a pull request number", parts[3])
			}
			b.Pull = n
			return b, nil
		}
	}
	ref, err := ParseURL(raw)
	if err != nil {
		return Build{}, fmt.Errorf("%q is not a GitHub address. Speccy takes a repo URL, or the URL of a branch, a folder, a file, a commit or a pull request", raw)
	}
	b.Ref = ref
	return b, nil
}
