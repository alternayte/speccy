package source

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ExternalKind is the link kind that names a code target: the target implements this doc.
const ExternalKind = "implemented-by"

// GitHubScheme is the scheme of a code target.
const GitHubScheme = "github"

// URLScheme is the scheme of a target written as a bare URL.
const URLScheme = "url"

// schemePrefix matches "<scheme>:<ref>" in a frontmatter target. A bundle slug and a relative
// path carry no colon, so they are not external.
var schemePrefix = regexp.MustCompile(`^([a-z][a-z0-9+.-]*):(.+)$`)

// repoRef matches "owner/repo", with an optional "@ref" and an optional "#path". The ref is a
// commit SHA or a branch name.
var repoRef = regexp.MustCompile(`^([^/@#\s]+/[^/@#\s]+)(?:@([^@#\s]+))?(?:#(.+))?$`)

// commitSHA is a full or abbreviated commit SHA. A ref that is not one is a branch name.
var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// ExternalTarget is a parsed link target outside Speccy: an issue, a page, a repo path, or a
// commit.
type ExternalTarget struct {
	// Scheme is github, url, or the scheme of a link pattern.
	Scheme string
	// Ref is the part after the scheme, as the author wrote it.
	Ref string
	// URL is where a person opens the target.
	URL string
	// Host is the host of URL. The MCP connection that reads the target matches on it.
	Host string
	// Repo, Path, Commit and Branch are set for a github target only. An empty Path is the
	// whole repo. The ref of the target is a commit, or a branch (#97), or neither.
	Repo   string
	Path   string
	Commit string
	Branch string
}

// GitHubDoc reads a target as a file in a GitHub repo: "github:owner/repo@ref#path", or the
// URL of a file, ".../owner/repo/blob/<ref>/<path>". host is the host of the repo. refPath is
// the ref and the path as one text, "<ref>/<path>", because a branch name can hold a slash:
// the caller matches it against the branches it knows. pinned is true when the ref is a
// commit SHA, so the file never changes.
func (t ExternalTarget) GitHubDoc() (host, repo, refPath string, pinned, ok bool) {
	switch t.Scheme {
	case GitHubScheme:
		if t.Path == "" || (t.Commit == "" && t.Branch == "") {
			return "", "", "", false, false
		}
		return t.Host, t.Repo, t.Commit + t.Branch + "/" + t.Path, t.Commit != "", true
	case URLScheme:
		u, err := url.Parse(t.URL)
		if err != nil {
			return "", "", "", false, false
		}
		p, err := url.PathUnescape(strings.Trim(u.Path, "/"))
		if err != nil {
			return "", "", "", false, false
		}
		parts := strings.SplitN(p, "/", 5)
		if len(parts) < 5 || parts[2] != "blob" {
			return "", "", "", false, false
		}
		return u.Host, parts[0] + "/" + parts[1], parts[3] + "/" + parts[4], commitSHA.MatchString(parts[3]), true
	}
	return "", "", "", false, false
}

// IsExternalTarget reports whether a frontmatter target names something outside Speccy.
func IsExternalTarget(target string) bool {
	return schemePrefix.MatchString(strings.TrimSpace(target))
}

// ParseExternalTarget parses an external target. patterns are the link patterns of
// .speccy.yaml, by scheme. The error says what the author must change.
func ParseExternalTarget(target string, patterns map[string]string) (ExternalTarget, error) {
	target = strings.TrimSpace(target)
	m := schemePrefix.FindStringSubmatch(target)
	if m == nil {
		return ExternalTarget{}, fmt.Errorf("%q is not an external target: it needs a scheme, such as github: or jira:, or a full URL", target)
	}
	scheme, ref := m[1], m[2]
	if strings.HasPrefix(ref, "//") {
		return parseURLTarget(target)
	}
	if scheme == GitHubScheme {
		return parseGitHubTarget(ref)
	}
	pattern, ok := patterns[scheme]
	if !ok {
		return ExternalTarget{}, fmt.Errorf("the scheme %q has no pattern: add link_patterns.%s to %s", scheme, scheme, RepoConfigFile)
	}
	raw := strings.ReplaceAll(pattern, "{key}", url.PathEscape(ref))
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ExternalTarget{}, fmt.Errorf("the pattern of %q makes %q, which is not a URL", scheme, raw)
	}
	return ExternalTarget{Scheme: scheme, Ref: ref, URL: raw, Host: u.Host}, nil
}

// parseURLTarget parses a bare URL.
func parseURLTarget(target string) (ExternalTarget, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ExternalTarget{}, fmt.Errorf("%q is not an http or https URL", target)
	}
	return ExternalTarget{Scheme: URLScheme, Ref: target, URL: target, Host: u.Host}, nil
}

// parseGitHubTarget parses "owner/repo", "owner/repo#path", or "owner/repo@ref#path", where
// the ref is a commit SHA or a branch.
func parseGitHubTarget(ref string) (ExternalTarget, error) {
	m := repoRef.FindStringSubmatch(ref)
	if m == nil {
		return ExternalTarget{}, fmt.Errorf("the github target %q is not owner/repo, owner/repo#path, or owner/repo@ref#path", ref)
	}
	t := ExternalTarget{Scheme: GitHubScheme, Ref: ref, Host: "github.com", Repo: m[1], Path: strings.Trim(m[3], "/")}
	if commitSHA.MatchString(m[2]) {
		t.Commit = m[2]
	} else {
		t.Branch = m[2]
	}
	t.URL = "https://github.com/" + t.Repo
	switch at := t.Commit + t.Branch; {
	case at != "" && t.Path != "":
		t.URL += "/tree/" + at + "/" + t.Path
	case t.Branch != "":
		t.URL += "/tree/" + t.Branch
	case t.Commit != "":
		t.URL += "/commit/" + t.Commit
	case t.Path != "":
		t.URL += "/tree/HEAD/" + t.Path
	}
	return t, nil
}

// LinkPatternProblem returns the reason a link pattern is unusable, or an empty string.
func LinkPatternProblem(scheme, pattern string) string {
	if !schemeName.MatchString(scheme) {
		return fmt.Sprintf("the scheme %q is not lower-case letters, digits, and - . +", scheme)
	}
	if scheme == GitHubScheme || scheme == URLScheme {
		return fmt.Sprintf("the scheme %q is built in, so a pattern cannot replace it", scheme)
	}
	if !strings.Contains(pattern, "{key}") {
		return fmt.Sprintf("the pattern %q has no {key}", pattern)
	}
	u, err := url.Parse(strings.ReplaceAll(pattern, "{key}", "KEY"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Sprintf("the pattern %q is not an http or https URL", pattern)
	}
	return ""
}

var schemeName = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
