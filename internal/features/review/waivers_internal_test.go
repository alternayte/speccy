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
	fm := fmt.Sprintf("---\ntype: sdd\nwaivers:\n  - check: sdd.data.model\n    section: []\n    reason: The migrations hold the types.\n    section_hash: %s\n  - check: sdd.section-check\n    section: [Doc, Data]\n    reason: Not in this section.\n    section_hash: %s\n---\n", wholeHash, dataHash)
	main := []byte(fm + body)
	in := input{main: main, doc: section.Parse(main), profile: profile.Versioned{Loaded: profile.Loaded{Profile: profile.Profile{Checks: []profile.Check{
		{Slug: "sdd.data.model", Scope: "doc"}, {Slug: "sdd.section-check", Scope: "section"},
	}}}}}
	in.fm, _, _ = source.ReadFrontmatter(main)
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
