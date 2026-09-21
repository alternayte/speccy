package source

import "testing"

func TestParseExternalTarget(t *testing.T) {
	patterns := map[string]string{"jira": "https://company.atlassian.net/browse/{key}"}
	cases := []struct {
		target        string
		wantURL       string
		wantHost      string
		wantRepo      string
		wantPath      string
		wantCommit    string
		wantNotExtern bool
		wantErr       bool
	}{
		{target: "payments-prd", wantNotExtern: true},
		{target: "../prd/payments.md", wantNotExtern: true},
		{target: "github:alternayte/speccy#internal/features/waiver",
			wantURL:  "https://github.com/alternayte/speccy/tree/HEAD/internal/features/waiver",
			wantHost: "github.com", wantRepo: "alternayte/speccy", wantPath: "internal/features/waiver"},
		{target: "github:alternayte/speccy", wantURL: "https://github.com/alternayte/speccy",
			wantHost: "github.com", wantRepo: "alternayte/speccy"},
		{target: "github:alternayte/speccy@41b2fff", wantURL: "https://github.com/alternayte/speccy/commit/41b2fff",
			wantHost: "github.com", wantRepo: "alternayte/speccy", wantCommit: "41b2fff"},
		{target: "jira:PAY-412", wantURL: "https://company.atlassian.net/browse/PAY-412", wantHost: "company.atlassian.net"},
		{target: "https://company.atlassian.net/wiki/spaces/ENG/pages/4210",
			wantURL: "https://company.atlassian.net/wiki/spaces/ENG/pages/4210", wantHost: "company.atlassian.net"},
		{target: "linear:ENG-12", wantErr: true},
		{target: "github:speccy", wantErr: true},
		{target: "ftp://files.example.com/x", wantErr: true},
	}
	for _, c := range cases {
		if got := IsExternalTarget(c.target); got == c.wantNotExtern {
			t.Errorf("IsExternalTarget(%q) = %v", c.target, got)
		}
		if c.wantNotExtern {
			continue
		}
		got, err := ParseExternalTarget(c.target, patterns)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseExternalTarget(%q) = %+v, want an error", c.target, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseExternalTarget(%q): %v", c.target, err)
			continue
		}
		if got.URL != c.wantURL || got.Host != c.wantHost || got.Repo != c.wantRepo || got.Path != c.wantPath || got.Commit != c.wantCommit {
			t.Errorf("ParseExternalTarget(%q) = %+v", c.target, got)
		}
	}
}

func TestLinkPatternProblem(t *testing.T) {
	if p := LinkPatternProblem("jira", "https://company.atlassian.net/browse/{key}"); p != "" {
		t.Errorf("a good pattern reports %q", p)
	}
	for _, c := range [][2]string{
		{"jira", "https://company.atlassian.net/browse/"},
		{"github", "https://example.com/{key}"},
		{"Jira", "https://example.com/{key}"},
		{"jira", "not a url {key}"},
	} {
		if LinkPatternProblem(c[0], c[1]) == "" {
			t.Errorf("%q %q reports no problem", c[0], c[1])
		}
	}
}
