package profile

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
)

// SectionHint is a rubric check that asks about one section and names none (#108). A check
// with no section runs again on the whole doc after each edit, so an edit to one section can
// change its answer, and a review of the doc does not converge.
type SectionHint struct {
	Slug string
	// Section is the heading the check is about.
	Section string
}

// String is the warning a person reads.
func (h SectionHint) String() string {
	return fmt.Sprintf("%s has no section, so it runs again on the whole doc after each edit. Add \"section: %s\" when the check is about that section only.",
		h.Slug, h.Section)
}

// SectionHints returns the rubric checks of the profile that name no section and are about
// one: a check with the slug of a built-in check that names a section, or a check whose
// question or pass condition names exactly one heading of the profile's template. A check
// that names no heading, or more than one, is a whole-doc check on purpose, and gets no hint.
func SectionHints(l Loaded) []SectionHint {
	builtinSection := map[string]string{}
	if builtins, err := Builtins(); err == nil {
		for _, b := range builtins {
			for _, c := range b.Profile.Checks {
				if c.Section != "" {
					builtinSection[c.Slug] = c.Section
				}
			}
		}
	}
	var headings []string
	for _, s := range section.Parse(StripMarks(l.TemplateText)).Sections {
		if s.Level > 1 {
			headings = append(headings, s.Title)
		}
	}
	var out []SectionHint
	for _, c := range l.Profile.Checks {
		if c.Stage != "rubric" || c.Section != "" || c.Scope == "section" {
			continue
		}
		if sec, ok := builtinSection[c.Slug]; ok {
			out = append(out, SectionHint{Slug: c.Slug, Section: sec})
			continue
		}
		// The check names a heading when its text has the title as the template writes it, such
		// as "Migration", or the title in any case with the word "section" after it. A plain
		// word of a sentence, such as "decisions", names no heading.
		text := c.Question + " " + c.PassWhen
		var named []string
		for _, h := range headings {
			title := strings.TrimSpace(h)
			if lint.NormTitle(title) == "" {
				continue
			}
			q := regexp.QuoteMeta(title)
			if regexp.MustCompile(`\b`+q+`\b`).MatchString(text) || regexp.MustCompile(`(?i)\b`+q+`\s+section\b`).MatchString(text) {
				named = append(named, h)
			}
		}
		if len(named) == 1 {
			out = append(out, SectionHint{Slug: c.Slug, Section: named[0]})
		}
	}
	return out
}
