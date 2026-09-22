// Package sourcepolicy decides which grounding sources a profile accepts, and which claim
// class a section carries. Every rule here is deterministic and free of I/O: a model never
// assigns a class, and a policy never depends on a model obeying it.
package sourcepolicy

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Tier is the trust tier of a domain.
type Tier string

const (
	TierPrimary   Tier = "primary"
	TierSecondary Tier = "secondary"
)

// Unclassified is the class of a claim whose section matches no pattern.
const Unclassified = "unclassified"

// Unclassified policy values.
const (
	UnclassifiedAllow   = "allow"
	UnclassifiedWarn    = "warn"
	UnclassifiedRequire = "require-classification"
)

// DomainRule gives one host pattern a tier. The pattern is a host, optionally with a
// leading "*." that matches one or more leading labels.
type DomainRule struct {
	Pattern string `yaml:"pattern" json:"pattern"`
	Tier    Tier   `yaml:"tier" json:"tier"`
}

// ClassRule gives one heading path pattern a claim class. The pattern is a heading path
// with " / " between headings, and "*" as a segment that matches one heading.
type ClassRule struct {
	Pattern string `yaml:"pattern" json:"pattern"`
	Class   string `yaml:"class" json:"class"`
}

// Policy is the profile's grounding source policy.
type Policy struct {
	// Allow, when it holds a pattern, is the only set of hosts a source may come from.
	Allow []string `yaml:"allow" json:"allow"`
	// Forbid names hosts Speccy never accepts and never requests.
	Forbid []string `yaml:"forbid" json:"forbid"`
	// Domains gives a tier to a host pattern. A host that matches none is secondary.
	Domains []DomainRule `yaml:"domains" json:"domains"`
	// Freshness is the period a source of that tier stays usable, in days. Zero is no limit.
	Freshness map[Tier]int `yaml:"freshness" json:"freshness"`
	// RequirePrimary names the claim classes whose sources must all be primary.
	RequirePrimary []string `yaml:"require_primary" json:"require_primary"`
	// Classes maps a heading path to a claim class.
	Classes []ClassRule `yaml:"classes" json:"classes"`
	// Unclassified says what happens to a claim that matches no class rule.
	Unclassified string `yaml:"unclassified" json:"unclassified"`
}

// Active reports whether the policy constrains anything. An empty policy leaves the
// grounding stage exactly as it was.
func (p Policy) Active() bool {
	return len(p.Allow) > 0 || len(p.Forbid) > 0 || len(p.Domains) > 0 ||
		len(p.Freshness) > 0 || len(p.RequirePrimary) > 0 || len(p.Classes) > 0
}

// Validate reports the faults that stop a profile from loading. Two class patterns of equal
// specificity on one section would be resolved by order, and a rule nobody can read off the
// file is worse than no rule.
func (p Policy) Validate() error {
	switch p.Unclassified {
	case "", UnclassifiedAllow, UnclassifiedWarn, UnclassifiedRequire:
	default:
		return fmt.Errorf("grounding.sources.unclassified is %q; use allow, warn, or require-classification", p.Unclassified)
	}
	for _, d := range p.Domains {
		if d.Tier != TierPrimary && d.Tier != TierSecondary {
			return fmt.Errorf("grounding.sources.domains: %q has tier %q; use primary or secondary", d.Pattern, d.Tier)
		}
		if err := checkHostPattern(d.Pattern); err != nil {
			return fmt.Errorf("grounding.sources.domains: %w", err)
		}
	}
	for _, h := range p.Allow {
		if err := checkHostPattern(h); err != nil {
			return fmt.Errorf("grounding.sources.allow: %w", err)
		}
	}
	for _, h := range p.Forbid {
		if err := checkHostPattern(h); err != nil {
			return fmt.Errorf("grounding.sources.forbid: %w", err)
		}
	}
	for t := range p.Freshness {
		if t != TierPrimary && t != TierSecondary {
			return fmt.Errorf("grounding.sources.freshness: %q is not a tier; use primary or secondary", t)
		}
	}
	seen := map[string]string{}
	for _, c := range p.Classes {
		if c.Class == "" {
			return fmt.Errorf("grounding.sources.classes: %q names no class", c.Pattern)
		}
		if c.Class == Unclassified {
			return fmt.Errorf("grounding.sources.classes: %q may not name the class %q", c.Pattern, Unclassified)
		}
		norm := normalisePattern(c.Pattern)
		if prev, ok := seen[norm]; ok {
			return fmt.Errorf("grounding.sources.classes: %q and %q match the same sections with the same specificity", prev, c.Pattern)
		}
		seen[norm] = c.Pattern
	}
	// Two different patterns of equal specificity that both match one section are also a
	// fault, and only a section shows it. Compare the patterns against each other.
	for i := range p.Classes {
		for j := i + 1; j < len(p.Classes); j++ {
			a, b := p.Classes[i], p.Classes[j]
			if a.Class == b.Class {
				continue
			}
			if specificity(a.Pattern) != specificity(b.Pattern) {
				continue
			}
			if overlap(a.Pattern, b.Pattern) {
				return fmt.Errorf("grounding.sources.classes: %q and %q are equally specific and both match a section", a.Pattern, b.Pattern)
			}
		}
	}
	return nil
}

func checkHostPattern(pat string) error {
	if pat == "" {
		return fmt.Errorf("an empty host pattern")
	}
	if strings.ContainsAny(pat, "/: ") {
		return fmt.Errorf("%q is not a host; write a host such as example.com or *.example.com", pat)
	}
	if i := strings.Index(pat, "*"); i > 0 || (i == 0 && !strings.HasPrefix(pat, "*.")) {
		return fmt.Errorf("%q puts * where it does not belong; a wildcard is only a leading \"*.\"", pat)
	}
	return nil
}

// ClassOf returns the claim class of a section, by its heading path. The most specific
// matching pattern wins. A section that matches nothing is Unclassified.
func (p Policy) ClassOf(headingPath string) string {
	best, bestSpec := Unclassified, -1
	for _, c := range p.Classes {
		if !matchPath(c.Pattern, headingPath) {
			continue
		}
		if s := specificity(c.Pattern); s > bestSpec {
			best, bestSpec = c.Class, s
		}
	}
	return best
}

// NeedsPrimary reports whether a class's sources must all be primary.
func (p Policy) NeedsPrimary(class string) bool {
	for _, c := range p.RequirePrimary {
		if c == class {
			return true
		}
	}
	return false
}

// TierOf returns the tier of a host. A host that matches no rule is secondary, because a
// source nobody vouched for is not a primary one.
func (p Policy) TierOf(host string) Tier {
	best, bestSpec := TierSecondary, -1
	for _, d := range p.Domains {
		if !matchHost(d.Pattern, host) {
			continue
		}
		if s := hostSpecificity(d.Pattern); s > bestSpec {
			best, bestSpec = d.Tier, s
		}
	}
	return best
}

// HostState is why a host may or may not be requested.
type HostState int

const (
	// HostOK means the policy allows a request to the host.
	HostOK HostState = iota
	// HostForbidden means the policy names the host in forbid; Speccy never requests it.
	HostForbidden
	// HostNotAllowed means an allow list exists and the host is not in it.
	HostNotAllowed
)

// CheckHost is the test Speccy runs before every request, including every redirect hop.
func (p Policy) CheckHost(host string) (HostState, string) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, f := range p.Forbid {
		if matchHost(f, host) {
			return HostForbidden, fmt.Sprintf("The profile forbids the domain %s.", host)
		}
	}
	if len(p.Allow) == 0 {
		return HostOK, ""
	}
	for _, a := range p.Allow {
		if matchHost(a, host) {
			return HostOK, ""
		}
	}
	return HostNotAllowed, fmt.Sprintf("The profile does not allow the domain %s.", host)
}

// Source is one source of a claim, with what the resolver learned about it.
type Source struct {
	// URL is the address the search returned.
	URL string `json:"url"`
	// FinalURL is the address that answered, after the redirects. It is empty when the
	// resolver did not run.
	FinalURL string `json:"final_url,omitempty"`
	// Chain is every URL Speccy requested, in order, including FinalURL.
	Chain []string `json:"chain,omitempty"`
	// Status is the HTTP status of the final response.
	Status int `json:"status,omitempty"`
	// RetrievedAt is when Speccy resolved the source. It is zero when the resolver did not
	// run, and a source with no date fails every freshness period.
	RetrievedAt *time.Time `json:"retrieved_at,omitempty"`
	// Modified is the Last-Modified header, when the server sent one.
	Modified *time.Time `json:"modified,omitempty"`
	// Tier is the tier the policy gave the final host.
	Tier Tier `json:"tier,omitempty"`
	// Dropped says the policy refused the source, and Reason says why.
	Dropped bool   `json:"dropped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Host returns the host of the address that answered, or of the address the search returned.
func (s Source) Host() string {
	raw := s.FinalURL
	if raw == "" {
		raw = s.URL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
}

// Evaluate applies the policy to one resolved source. It returns the source with its tier,
// and with Dropped and Reason set when the policy refuses it. resolverOn says whether the
// admin left the metadata resolver on: with it off, a rule that needs redirect or freshness
// metadata drops the source rather than passing it.
func (p Policy) Evaluate(s Source, class string, resolverOn bool, now time.Time) Source {
	host := s.Host()
	if host == "" {
		s.Dropped, s.Reason = true, "The source is not a URL."
		return s
	}
	if state, why := p.CheckHost(host); state != HostOK {
		s.Dropped, s.Reason = true, why
		return s
	}
	s.Tier = p.TierOf(host)
	if p.NeedsPrimary(class) && s.Tier != TierPrimary {
		s.Dropped = true
		s.Reason = fmt.Sprintf("A %s claim needs a primary source, and %s is secondary.", class, host)
		return s
	}
	days := p.Freshness[s.Tier]
	if days <= 0 {
		return s
	}
	at := s.RetrievedAt
	if at == nil || at.IsZero() {
		s.Dropped = true
		if resolverOn {
			s.Reason = "The source carries no retrieval date, so Speccy cannot apply the freshness period."
		} else {
			s.Reason = "The source resolver is off, so the source carries no retrieval date and the freshness period cannot apply."
		}
		return s
	}
	if now.Sub(*at) > time.Duration(days)*24*time.Hour {
		s.Dropped = true
		s.Reason = fmt.Sprintf("The source is older than the %d-day freshness period for a %s source.", days, s.Tier)
	}
	return s
}

// Kept returns the sources the policy accepted, and the reasons it gave for the rest.
func Kept(sources []Source) (kept []Source, reasons []string) {
	for _, s := range sources {
		if s.Dropped {
			reasons = append(reasons, s.Reason)
			continue
		}
		kept = append(kept, s)
	}
	sort.Strings(reasons)
	return kept, dedupe(reasons)
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i > 0 && s == in[i-1] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// matchHost matches a host against a pattern. "*.example.com" matches one or more leading
// labels, and never the bare "example.com".
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+suffix)
	}
	return pattern == host
}

func hostSpecificity(pattern string) int {
	n := len(strings.Split(strings.TrimPrefix(pattern, "*."), "."))
	if strings.HasPrefix(pattern, "*.") {
		return n // a wildcard is always less specific than the same host written out
	}
	return n + 1
}

// matchPath matches a heading path against a pattern. A "*" segment matches one heading, and
// a trailing "**" matches the rest of the path.
func matchPath(pattern, path string) bool {
	return matchSegments(splitPath(pattern), splitPath(path))
}

func matchSegments(pat, seg []string) bool {
	for i, p := range pat {
		if p == "**" {
			return true
		}
		if i >= len(seg) {
			return false
		}
		if p != "*" && !strings.EqualFold(p, seg[i]) {
			return false
		}
	}
	return len(pat) == len(seg)
}

func splitPath(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "/") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// specificity counts the literal segments of a pattern. A pattern with more literal headings
// wins over one with more wildcards.
func specificity(pattern string) int {
	n := 0
	for _, p := range splitPath(pattern) {
		if p == "*" || p == "**" {
			continue
		}
		n += 2
	}
	return n + len(splitPath(pattern))
}

func normalisePattern(pattern string) string {
	return strings.ToLower(strings.Join(splitPath(pattern), "/"))
}

// overlap reports whether two patterns can match one heading path.
func overlap(a, b string) bool {
	pa, pb := splitPath(a), splitPath(b)
	if len(pa) != len(pb) {
		// A "**" tail can still cover the other pattern.
		if !hasTail(pa) && !hasTail(pb) {
			return false
		}
	}
	n := min(len(pa), len(pb))
	for i := range n {
		if pa[i] == "**" || pb[i] == "**" {
			return true
		}
		if pa[i] == "*" || pb[i] == "*" {
			continue
		}
		if !strings.EqualFold(pa[i], pb[i]) {
			return false
		}
	}
	return true
}

func hasTail(p []string) bool {
	return len(p) > 0 && p[len(p)-1] == "**"
}
