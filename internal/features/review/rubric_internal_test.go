package review

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
)

// #138: the run note names a check with no section only when this doc has its heading, and
// never a check with an explicit "scope: doc". A second note names a check whose section this
// doc does not have.
func TestSectionNotes(t *testing.T) {
	var p profile.Versioned
	p.TemplateText = []byte("# T\n\n## Migration\n\n## Security\n")
	p.Profile.Key = "sdd"
	p.Profile.Checks = []profile.Check{
		{Slug: "x.rollout", Stage: "rubric", Question: "Does the Migration section give the order?"},
		{Slug: "x.access", Stage: "rubric", Question: "Does the Security section name each role?"},
		{Slug: "x.whole", Stage: "rubric", Scope: "doc", Question: "Does the Security section agree with the plan?"},
		{Slug: "x.obs", Stage: "rubric", Section: "Observability"},
	}
	main := []byte("# Doc\n\n## Security\n\nRoles.\n")
	notes := sectionNotes(p, input{main: main, doc: section.Parse(main)})
	if len(notes) != 2 {
		t.Fatalf("notes = %q, want 2", notes)
	}
	if !strings.Contains(notes[0], `"x.access"`) || strings.Contains(notes[0], "x.rollout") || strings.Contains(notes[0], "x.whole") {
		t.Errorf("hint note = %q, want x.access only", notes[0])
	}
	if !strings.Contains(notes[1], `x.obs ("Observability")`) {
		t.Errorf("missing-section note = %q, want x.obs", notes[1])
	}
}

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

// #119: one shortfall stays for each group. It is the first one, with the quote of another
// where it has none, so a finding of the last review with no quote leaves line 1. A number
// that is not in the list, or in a second group, changes nothing.
func TestSame_OneShortfallForEachGroup(t *testing.T) {
	falls := []shortfall{
		{Reason: "The audit-trail choice has no DEC trace ID."},
		{Reason: "The transport choice has no DEC trace ID.", Quote: "Transport."},
		{Reason: "The audit-trail choice is stated without a DEC trace ID.", Quote: "Audit trail.", Question: "Which DEC?"},
		{Reason: "The cache choice has no DEC trace ID.", Quote: "The cache is local."},
	}
	got := merged(falls, [][]int{{3, 1}, {2, 3, 9}, {0}})
	want := []shortfall{
		{Reason: "The audit-trail choice has no DEC trace ID.", Quote: "Audit trail.", Question: "Which DEC?"},
		falls[1], falls[3],
	}
	if !slices.Equal(got, want) {
		t.Errorf("shortfalls %+v, want %+v", got, want)
	}
}

// The model gives the keys of an answer in the order of the schema, which is the order of the
// alphabet. Each shortfall of the last review needs its "analysis" in front of its "state":
// with none, a real model gave the state first and called a standing shortfall fixed.
func TestRubric_AnalysisBeforeTheStateOfAPriorShortfall(t *testing.T) {
	var schema struct {
		Properties struct {
			Results struct {
				Items struct {
					Properties struct {
						Prior struct {
							Items struct {
								Required   []string                   `json:"required"`
								Properties map[string]json.RawMessage `json:"properties"`
							} `json:"items"`
						} `json:"prior"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"results"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(rubricSchema([]string{"a"}), &schema); err != nil {
		t.Fatal(err)
	}
	item := schema.Properties.Results.Items.Properties.Prior.Items
	if _, ok := item.Properties["analysis"]; !ok || !slices.Contains(item.Required, "analysis") {
		t.Errorf("a prior shortfall requires %v of %d properties, want a required analysis in front of the state", item.Required, len(item.Properties))
	}
}
