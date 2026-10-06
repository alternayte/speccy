package profile

import (
	"fmt"
	"regexp"
)

// UpstreamHint is a rubric check whose question or pass condition names a doc type of
// links.upstream, such as "PRD", and that does not read the upstream docs (#143). The check
// sees the doc alone, so a check that compares the doc with its PRD fails on every doc.
type UpstreamHint struct {
	Slug string
	// Type is the doc type as the check names it.
	Type string
}

// String is the warning a person reads.
func (h UpstreamHint) String() string {
	return fmt.Sprintf("%s names the %s, but it does not read the upstream docs, so it cannot see the %s. Add \"reads: [upstream]\" when the check compares the doc with its %s.",
		h.Slug, h.Type, h.Type, h.Type)
}

// UpstreamHints returns the rubric checks of the profile that name a doc type of
// links.upstream and have no reads: [upstream]. A type matches by its key, such as "prd", or
// by the name of the built-in profile with that key, in any case.
func UpstreamHints(l Loaded) []UpstreamHint {
	up := l.Profile.Links.Upstream
	if up == nil || len(up.Types) == 0 {
		return nil
	}
	names := map[string]string{}
	if builtins, err := Builtins(); err == nil {
		for _, b := range builtins {
			names[b.Profile.Key] = b.Profile.Name
		}
	}
	var words []*regexp.Regexp
	for _, t := range up.Types {
		words = append(words, regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(t)+`\b`))
		if n := names[t]; n != "" {
			words = append(words, regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(n)+`\b`))
		}
	}
	var out []UpstreamHint
	for _, c := range l.Profile.Checks {
		if c.Stage != "rubric" || c.Upstream() {
			continue
		}
		text := c.Question + " " + c.PassWhen
		for _, w := range words {
			if m := w.FindString(text); m != "" {
				out = append(out, UpstreamHint{Slug: c.Slug, Type: m})
				break
			}
		}
	}
	return out
}
