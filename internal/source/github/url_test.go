package github

import "testing"

// REQ-128: the four forms of a source URL, and what each one names.
func TestParseURL(t *testing.T) {
	cases := []struct {
		in     string
		repo   string
		branch string
		path   string
		file   bool
		api    string
	}{
		{in: "acme/specs", repo: "acme/specs", path: ".", api: DefaultAPI},
		{in: "https://github.com/acme/specs", repo: "acme/specs", path: ".", api: DefaultAPI},
		{in: "https://github.com/acme/specs.git", repo: "acme/specs", path: ".", api: DefaultAPI},
		{in: "github.com/acme/specs/tree/main/docs", repo: "acme/specs", branch: "main", path: "docs", api: DefaultAPI},
		{in: "https://github.com/acme/specs/blob/main/docs/prd-payments.md", repo: "acme/specs", branch: "main",
			path: "docs/prd-payments.md", file: true, api: DefaultAPI},
		{in: "https://github.example.com/acme/specs/tree/next/docs", repo: "acme/specs", branch: "next", path: "docs",
			api: "https://github.example.com/api/v3"},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got.Repo != c.repo || got.Branch != c.branch || got.Path != c.path || got.File != c.file || got.APIURL() != c.api {
			t.Errorf("%s gave %+v with API %s", c.in, got, got.APIURL())
		}
	}
	for _, bad := range []string{"", "acme", "https://github.com/acme", "https://example.com", "https://github.com/acme/specs/pull/7"} {
		if ref, err := ParseURL(bad); err == nil {
			t.Errorf("%q parsed as %+v", bad, ref)
		}
	}
}

// A verification run takes the address a person copies: a repo, a branch, a file, a commit
// or a pull request. The file names no scope.
func TestParseBuildURL(t *testing.T) {
	cases := []struct {
		in     string
		repo   string
		branch string
		sha    string
		pull   int
	}{
		{in: "https://github.com/acme/pay", repo: "acme/pay"},
		{in: "https://github.com/acme/pay/tree/next/internal", repo: "acme/pay", branch: "next"},
		{in: "https://github.com/acme/pay/blob/main/internal/pay/retry.go", repo: "acme/pay", branch: "main"},
		{in: "https://github.com/acme/pay/commit/1a2b3c4d5e", repo: "acme/pay", sha: "1a2b3c4d5e"},
		{in: "https://github.com/acme/pay/pull/12", repo: "acme/pay", pull: 12},
		{in: "https://github.com/acme/pay/pull/12/files", repo: "acme/pay", pull: 12},
	}
	for _, c := range cases {
		got, err := ParseBuildURL(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got.Repo != c.repo || got.Branch != c.branch || got.SHA != c.sha || got.Pull != c.pull {
			t.Errorf("%s gave %+v", c.in, got)
		}
	}
	for _, bad := range []string{"", "https://github.com/acme/pay/commit/zz", "https://github.com/acme/pay/pull/x", "https://github.com/acme/pay/issues/3"} {
		if b, err := ParseBuildURL(bad); err == nil {
			t.Errorf("%q parsed as %+v", bad, b)
		}
	}
}
