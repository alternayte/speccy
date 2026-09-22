package sourcepolicy

import (
	"testing"
	"time"
)

func day(n int) *time.Time {
	t := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
	return &t
}

var now = time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

func TestClassOfMostSpecificWins(t *testing.T) {
	p := Policy{Classes: []ClassRule{
		{Pattern: "Security/**", Class: "security"},
		{Pattern: "Security/Threat model", Class: "threat"},
		{Pattern: "*/Limits", Class: "limits"},
	}}
	for _, c := range []struct{ path, want string }{
		{"Security/Threat model", "threat"},
		{"Security/Secrets", "security"},
		{"Operations/Limits", "limits"},
		{"Glossary", Unclassified},
	} {
		if got := p.ClassOf(c.path); got != c.want {
			t.Errorf("ClassOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestValidateRejectsEqualSpecificity(t *testing.T) {
	p := Policy{Classes: []ClassRule{
		{Pattern: "Security/*", Class: "security"},
		{Pattern: "*/Limits", Class: "limits"},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("two equally specific patterns that both match one section must fail to load")
	}
	ok := Policy{Classes: []ClassRule{
		{Pattern: "Security/*", Class: "security"},
		{Pattern: "Security/Limits", Class: "limits"},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a more specific pattern is not a conflict: %v", err)
	}
}

func TestCheckHostForbidBeatsAllow(t *testing.T) {
	p := Policy{Allow: []string{"*.example.com"}, Forbid: []string{"blog.example.com"}}
	if s, _ := p.CheckHost("docs.example.com"); s != HostOK {
		t.Error("an allowed subdomain must pass")
	}
	if s, _ := p.CheckHost("blog.example.com"); s != HostForbidden {
		t.Error("a forbidden host must be forbidden, even inside an allowed wildcard")
	}
	if s, _ := p.CheckHost("example.com"); s != HostNotAllowed {
		t.Error("*.example.com must not match the bare host")
	}
	if s, _ := p.CheckHost("elsewhere.test"); s != HostNotAllowed {
		t.Error("an allow list excludes every host outside it")
	}
}

func TestEvaluateFreshnessNeedsARetrievalDate(t *testing.T) {
	p := Policy{
		Domains:   []DomainRule{{Pattern: "docs.example.com", Tier: TierPrimary}},
		Freshness: map[Tier]int{TierPrimary: 30},
	}
	fresh := p.Evaluate(Source{URL: "https://docs.example.com/a", RetrievedAt: day(1)}, "", true, now)
	if fresh.Dropped {
		t.Errorf("a source read yesterday is fresh: %s", fresh.Reason)
	}
	old := p.Evaluate(Source{URL: "https://docs.example.com/a", RetrievedAt: day(90)}, "", true, now)
	if !old.Dropped {
		t.Error("a source older than the freshness period must drop")
	}
	undated := p.Evaluate(Source{URL: "https://docs.example.com/a"}, "", false, now)
	if !undated.Dropped {
		t.Error("with no retrieval date the freshness rule must drop the source, not pass it")
	}
}

func TestEvaluateRequirePrimary(t *testing.T) {
	p := Policy{
		Domains:        []DomainRule{{Pattern: "docs.example.com", Tier: TierPrimary}},
		RequirePrimary: []string{"security"},
	}
	secondary := p.Evaluate(Source{URL: "https://blog.test/a"}, "security", true, now)
	if !secondary.Dropped {
		t.Error("a security claim must drop a secondary source")
	}
	primary := p.Evaluate(Source{URL: "https://docs.example.com/a"}, "security", true, now)
	if primary.Dropped {
		t.Errorf("a primary source satisfies require_primary: %s", primary.Reason)
	}
	other := p.Evaluate(Source{URL: "https://blog.test/a"}, "pricing", true, now)
	if other.Dropped {
		t.Error("require_primary applies only to the classes it names")
	}
}

func TestTierOfPrefersTheHostWrittenOut(t *testing.T) {
	p := Policy{Domains: []DomainRule{
		{Pattern: "*.example.com", Tier: TierSecondary},
		{Pattern: "docs.example.com", Tier: TierPrimary},
	}}
	if got := p.TierOf("docs.example.com"); got != TierPrimary {
		t.Errorf("TierOf = %q, want primary", got)
	}
	if got := p.TierOf("blog.example.com"); got != TierSecondary {
		t.Errorf("TierOf = %q, want secondary", got)
	}
	if got := p.TierOf("unknown.test"); got != TierSecondary {
		t.Errorf("an unlisted host is secondary, got %q", got)
	}
}
