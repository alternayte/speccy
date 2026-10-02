package profile

import (
	"strings"
	"testing"
)

// #108: a rubric check that is about one section and names none gets a hint: a check with the
// slug of a built-in check that names a section, and a check whose text names one heading of
// the template. A check about the whole doc gets none.
func TestSectionHints(t *testing.T) {
	l := Loaded{TemplateText: []byte("# Title\n\n## Decisions <!-- required -->\n\n## Migration\n\n## Security\n")}
	l.Profile.Checks = []Check{
		{Slug: "sdd.security", Stage: "rubric", Question: "Is access stated?", PassWhen: "Each caller has a role."},
		{Slug: "x.rollout", Stage: "rubric", Question: "Does the Migration section say how the change rolls out?", PassWhen: "Each step has an order."},
		{Slug: "x.tone", Stage: "rubric", Question: "Is the doc written for an implementer?", PassWhen: "The doc states decisions, not options."},
		{Slug: "x.both", Stage: "rubric", Question: "Do Security and Migration agree?", PassWhen: "No conflict."},
		{Slug: "x.named", Stage: "rubric", Section: "Security", Question: "Does the Security section name each role?", PassWhen: "Each role has a name."},
		{Slug: "lint.like", Stage: "coherence", Question: "Does the Migration section link the plan?", PassWhen: "It links it."},
	}
	var got []string
	for _, h := range SectionHints(l) {
		got = append(got, h.Slug+"="+h.Section)
	}
	if want := "sdd.security=Security x.rollout=Migration"; strings.Join(got, " ") != want {
		t.Errorf("hints = %v, want %s", got, want)
	}
}
