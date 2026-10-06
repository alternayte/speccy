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
	// Section is the heading the check is about: the section of the built-in check with the
	// same slug, or the one heading of the template that the check's text names.
	Section string
	// Docs is the number of docs of the profile that the caller read, and Found the number of
	// them with a Section heading. With no docs read, Speccy cannot say if the docs have it.
	Docs, Found int
}

// String is the warning a person reads. It suggests the heading only when a doc of the
// profile has it, because a section key with a heading that no doc has does nothing (#138).
func (h SectionHint) String() string {
	head := h.Slug + " has no section, so it runs again on the whole doc after each edit."
	switch {
	case h.Docs == 0:
		return fmt.Sprintf("%s Add \"section: %s\" when the check is about that section only, and the docs of the profile have that heading.", head, h.Section)
	case h.Found == 0:
		return fmt.Sprintf("%s No doc of the profile has the heading %q. Add \"section:\" with a heading that the docs use, or add \"scope: doc\" to keep the whole doc.", head, h.Section)
	}
	return fmt.Sprintf("%s %d of %d docs of the profile have the heading %q. Add \"section: %s\" when the check is about that section only.", head, h.Found, h.Docs, h.Section, h.Section)
}

// SectionHints returns the rubric checks of the profile that name no section and are about
// one: a check with the slug of a built-in check that names a section, or a check whose
// question or pass condition names exactly one heading of the profile's template. A check
// that names no heading, or more than one, is a whole-doc check on purpose, and gets no hint.
// A check with an explicit "scope: doc" says that it reads the whole doc, and gets no hint.
// docs are the docs of the profile: each hint counts the docs with its heading.
func SectionHints(l Loaded, docs []section.Doc) []SectionHint {
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
		if c.Stage != "rubric" || c.Section != "" || c.Scope != "" {
			continue
		}
		sec, ok := builtinSection[c.Slug]
		if !ok {
			// The check names a heading when its text has the title as the template writes it,
			// such as "Migration", or the title in any case with the word "section" after it. A
			// plain word of a sentence, such as "decisions", names no heading.
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
			if len(named) != 1 {
				continue
			}
			sec = named[0]
		}
		out = append(out, SectionHint{Slug: c.Slug, Section: sec, Docs: len(docs), Found: withHeading(sec, docs)})
	}
	return out
}

// MissingSection is a rubric check whose section names a heading that no doc of the profile
// has. In those docs the check reads the whole doc, and nothing else says so (#138).
type MissingSection struct {
	Slug    string
	Section string
}

// String is the warning a person reads.
func (m MissingSection) String() string {
	return fmt.Sprintf("%s names the section %q, and no doc of the profile has that heading, so the check reads the whole doc. Use a heading that the docs have.", m.Slug, m.Section)
}

// MissingSections returns the rubric checks whose section no doc of docs has. With no docs it
// returns nothing: Speccy cannot tell.
func MissingSections(p Profile, docs []section.Doc) []MissingSection {
	if len(docs) == 0 {
		return nil
	}
	var out []MissingSection
	for _, c := range p.Checks {
		if c.Stage == "rubric" && c.Section != "" && withHeading(c.Section, docs) == 0 {
			out = append(out, MissingSection{Slug: c.Slug, Section: c.Section})
		}
	}
	return out
}

// withHeading counts the docs with a heading that a check with section sec would read.
func withHeading(sec string, docs []section.Doc) int {
	n := 0
	for _, d := range docs {
		if (Check{Section: sec}).Named(d) != nil {
			n++
		}
	}
	return n
}
