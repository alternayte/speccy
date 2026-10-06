package profile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
)

// #108: a rubric check that is about one section and names none gets a hint: a check with the
// slug of a built-in check that names a section, and a check whose text names one heading of
// the template. A check about the whole doc gets none, and so does a check with "section:
// none" (#138). "scope: doc", which the built-in profiles write on each check, does not stop
// a hint. Each hint counts the docs of the profile with its heading (#138).
func TestSectionHints(t *testing.T) {
	l := Loaded{TemplateText: []byte("# Title\n\n## Decisions <!-- required -->\n\n## Migration\n\n## Security\n")}
	l.Profile.Checks = []Check{
		{Slug: "sdd.security", Stage: "rubric", Question: "Is access stated?", PassWhen: "Each caller has a role."},
		{Slug: "x.rollout", Stage: "rubric", Question: "Does the Migration section say how the change rolls out?", PassWhen: "Each step has an order."},
		{Slug: "x.tone", Stage: "rubric", Question: "Is the doc written for an implementer?", PassWhen: "The doc states decisions, not options."},
		{Slug: "x.both", Stage: "rubric", Question: "Do Security and Migration agree?", PassWhen: "No conflict."},
		{Slug: "x.named", Stage: "rubric", Section: "Security", Question: "Does the Security section name each role?", PassWhen: "Each role has a name."},
		{Slug: "x.whole", Stage: "rubric", Section: "none", Question: "Does the Migration section match the plan?", PassWhen: "It does."},
		{Slug: "x.scoped", Stage: "rubric", Scope: "doc", Question: "Does the Security section name each role?", PassWhen: "It does."},
		{Slug: "lint.like", Stage: "coherence", Question: "Does the Migration section link the plan?", PassWhen: "It links it."},
	}
	docs := []section.Doc{
		section.Parse([]byte("# A\n\n## 2. Security\n\nRoles.\n")),
		section.Parse([]byte("# B\n\n## Access\n\nRoles.\n")),
	}
	var got []string
	for _, h := range SectionHints(l, docs) {
		got = append(got, fmt.Sprintf("%s=%s:%d/%d", h.Slug, h.Section, h.Found, h.Docs))
	}
	if want := "sdd.security=Security:1/2 x.rollout=Migration:0/2 x.scoped=Security:1/2"; strings.Join(got, " ") != want {
		t.Errorf("hints = %v, want %s", got, want)
	}
}

// #138: a check whose section no doc of the profile has reads the whole doc, so validate warns.
func TestMissingSections(t *testing.T) {
	p := Profile{Checks: []Check{
		{Slug: "x.security", Stage: "rubric", Section: "Security"},
		{Slug: "x.migration", Stage: "rubric", Section: "Migration"},
		{Slug: "x.whole", Stage: "rubric", Section: "none"},
	}}
	docs := []section.Doc{section.Parse([]byte("# A\n\n## Security\n\nRoles.\n"))}
	got := MissingSections(p, docs)
	if len(got) != 1 || got[0].Slug != "x.migration" {
		t.Errorf("missing = %+v, want x.migration", got)
	}
	if MissingSections(p, nil) != nil {
		t.Error("with no docs, Speccy cannot tell, so it warns about nothing")
	}
}
