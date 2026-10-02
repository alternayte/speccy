package review

import (
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
)

// #116: a cached answer of a check that names a section, with a shortfall on the text of
// another section, is one that an older Speccy took from a whole-doc review. It is not read
// from the cache. A quote in the section, a quote in no section and no quote are the model's own.
func TestRubric_AnswerOutsideItsSection(t *testing.T) {
	main := []byte("# Doc\n\n## Solution\n\nThe service sends a reminder.\n\n## Details\n\nA worker sends a reminder each day.\n")
	in := input{main: main, doc: section.Parse(main)}
	var sol *section.Section
	for i := range in.doc.Sections {
		if in.doc.Sections[i].Title == "Solution" {
			sol = &in.doc.Sections[i]
		}
	}
	answer := func(quotes ...string) rubricAnswer {
		a := rubricAnswer{Result: "fail"}
		for _, q := range quotes {
			a.Shortfalls = append(a.Shortfalls, shortfall{Reason: "r", Quote: q})
		}
		return a
	}
	named := scopeUnit{sec: sol, named: true}
	if !named.outside(in, answer("The service sends a reminder.", "A worker sends a reminder each day.")) {
		t.Error("a shortfall on the Details section counts as one of the Solution section")
	}
	// "sends a reminder" is in both sections: it is in the section, so it is the model's own.
	if named.outside(in, answer("The service sends a reminder.", "sends a reminder", "not in the doc", "")) {
		t.Error("an answer with quotes in the section, in no section, and with no quote counts as outside")
	}
	if (scopeUnit{}).outside(in, answer("A worker sends a reminder each day.")) {
		t.Error("a whole-doc answer counts as outside")
	}
}
