package review

import (
	"fmt"
	"testing"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// A waiver of a doc-scope check covers the whole doc: the model may point its finding at a
// different section on each run, and the waiver still holds. A section-scope check's waiver
// covers its section only.
func TestWaiver_DocScopeCoversWholeDoc(t *testing.T) {
	body := "# Doc\n\n## Data\n\nTables.\n\n## Interfaces\n\nThe API.\n"
	wholeHash, _ := section.HashAt(section.Parse([]byte(body)), []byte(body), []string{})
	dataHash, _ := section.HashAt(section.Parse([]byte(body)), []byte(body), []string{"Doc", "Data"})
	sidecar := fmt.Sprintf("waivers:\n  - check: sdd.data.model\n    section: []\n    reason: The migrations hold the types.\n    section_hash: %s\n  - check: sdd.section-check\n    section: [Doc, Data]\n    reason: Not in this section.\n    section_hash: %s\n", wholeHash, dataHash)
	main := []byte("---\ntype: sdd\n---\n" + body)
	in := input{main: main, doc: section.Parse(main), profile: profile.Versioned{Loaded: profile.Loaded{Profile: profile.Profile{Checks: []profile.Check{
		{Slug: "sdd.data.model", Scope: "doc"}, {Slug: "sdd.section-check", Scope: "section"},
	}}}}}
	in.fm, _, _ = source.ReadFrontmatter(main)
	var err error
	if in.dec, err = source.ParseDecisions([]byte(sidecar)); err != nil {
		t.Fatal(err)
	}
	at := func(path ...string) anchor.Anchor { return anchor.Anchor{File: "SPEC.md", HeadingPath: path} }
	ev := &evaluation{findings: []pending{
		{slug: "sdd.data.model", level: kernel.Must, anchor: at("Doc", "Interfaces")},
		{slug: "sdd.section-check", level: kernel.Must, anchor: at("Doc", "Data")},
		{slug: "sdd.section-check", level: kernel.Must, anchor: at("Doc", "Interfaces")},
	}}
	got := applyWaivers(in, ev)
	if want := []bool{true, true, false}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("waived %v, want %v", got, want)
	}
}

// A waiver of coherence.contradiction that names a conflict covers that conflict only, with the
// quotes folded for case and spaces. A new conflict in the same section stays open (#136). A
// waiver from before Speccy recorded the conflict still covers each conflict in its section.
func TestWaiver_ContradictionBindsToTheConflict(t *testing.T) {
	body := "# Doc\n\n## Timing\n\nPaid in 10 days. Paid in 7 days.\n\n## Legacy\n\nPaid in 3 days.\n"
	doc := section.Parse([]byte(body))
	timing, _ := section.HashAt(doc, []byte(body), []string{"Doc", "Timing"})
	legacy, _ := section.HashAt(doc, []byte(body), []string{"Doc", "Legacy"})
	sidecar := fmt.Sprintf("waivers:\n"+
		"  - check: coherence.contradiction\n    section: [Doc, Timing]\n    reason: The PRD changes next week.\n    section_hash: %s\n"+
		"    conflict:\n      with: prd\n      quote: \"paid in  10 DAYS.\"\n      with_quote: Paid in 5 days.\n"+
		"  - check: coherence.contradiction\n    section: [Doc, Legacy]\n    reason: An old waiver of the section.\n    section_hash: %s\n", timing, legacy)
	main := []byte("---\ntype: sdd\n---\n" + body)
	in := input{main: main, doc: section.Parse(main)}
	var err error
	if in.dec, err = source.ParseDecisions([]byte(sidecar)); err != nil {
		t.Fatal(err)
	}
	conflict := func(quote string, path ...string) pending {
		return pending{slug: ContradictionSlug, level: kernel.Must, anchor: anchor.Anchor{File: "SPEC.md", HeadingPath: path},
			evidence: map[string]any{"upstream": "prd", "quote": quote, "upstream_quote": "Paid in 5 days."}}
	}
	ev := &evaluation{findings: []pending{
		conflict("Paid in 10 days.", "Doc", "Timing"),
		conflict("Paid in 7 days.", "Doc", "Timing"),
		conflict("Paid in 3 days.", "Doc", "Legacy"),
	}}
	got := applyWaivers(in, ev)
	if want := []bool{true, false, true}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("waived %v, want %v", got, want)
	}
}
