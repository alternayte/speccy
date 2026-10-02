package review

import (
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
)

const targetDoc = "---\ntype: sdd\n---\n# Pay\n\nIntro.\n\n## Context\n\nFirst line of one paragraph,\nits second line.\n\n- item one\n- item two\n\n## Risks\n\nA risk.\n"

// Speccy picks the text a fix replaces, so the model copies nothing: the paragraph of the
// finding, its section when the finding is on the heading, and a place for new text when the
// doc lacks a section.
func TestFixTarget(t *testing.T) {
	src := []byte(targetDoc)
	doc := section.Parse(src)
	s := &Service{Profiles: func() map[string]profile.Versioned {
		return map[string]profile.Versioned{"sdd": {Loaded: profile.Loaded{TemplateText: []byte("# Title\n\n## Context <!-- required -->\n\n## Monitoring <!-- required -->\n\n## Risks <!-- required -->\n")}}}
	}}
	b := pgdb.SpecDoc{ProfileKey: "sdd", DocPath: "SPEC.md"}
	at := func(quote string) anchor.Anchor {
		i := strings.Index(targetDoc, quote)
		return anchor.New("SPEC.md", src, doc, i, i+len(quote))
	}
	text := func(tg fixTarget) string { return targetDoc[tg.start:tg.end] }

	if got := text(s.fixTarget(b, pgdb.Finding{CheckSlug: lint.PassiveVoice, Stage: StageLint}, at("second"), src)); got != "First line of one paragraph,\nits second line." {
		t.Errorf("a finding in a paragraph: target %q", got)
	}
	if got := text(s.fixTarget(b, pgdb.Finding{CheckSlug: lint.SlopPhrase, Stage: StageLint}, at("item two"), src)); got != "- item one\n- item two" {
		t.Errorf("a finding in a list: target %q", got)
	}
	if got := text(s.fixTarget(b, pgdb.Finding{CheckSlug: "sdd.context", Stage: StageRubric}, at("## Context"), src)); got != "## Context\n\nFirst line of one paragraph,\nits second line.\n\n- item one\n- item two" {
		t.Errorf("a finding on a heading: target %q", got)
	}
	if got := text(s.fixTarget(b, pgdb.Finding{CheckSlug: "links.has-children", Stage: StageCoherence}, at("---\ntype: sdd\n---\n"), src)); got != "---\ntype: sdd\n---" {
		t.Errorf("a link check: target %q", got)
	}

	// A missing required heading goes where the template puts it: in front of Risks.
	missing := pgdb.Finding{CheckSlug: lint.RequiredHeadings, Stage: StageLint, Message: `The template requires the "Monitoring" section. The doc has none.`}
	tg := s.fixTarget(b, missing, at("# Pay"), src)
	if tg.insert != insertBefore || tg.before != "## Risks" || tg.heading != "## Monitoring" {
		t.Fatalf("a missing heading: %+v", tg)
	}
	pt := patch{New: "## Monitoring\n\nGrafana.", Insert: tg.insert, Before: tg.before}
	e, ok := pt.edit(src)
	next, _, _ := applyEdits(src, []edit{e})
	if !ok || !strings.Contains(string(next), "- item two\n\n## Monitoring\n\nGrafana.\n\n## Risks\n") {
		t.Errorf("the new section landed here:\n%s", next)
	}

	// A patch holds after an edit elsewhere, and is stale after an edit of its own text.
	old := "First line of one paragraph,\nits second line."
	i := strings.Index(targetDoc, old)
	pt = patch{Old: old, New: "One line.", Start: i, End: i + len(old)}
	moved := []byte(strings.Replace(targetDoc, "Intro.", "A longer intro.", 1))
	if e, ok := pt.edit(moved); !ok || string(moved[e.start:e.end]) != old {
		t.Errorf("after an edit elsewhere: %+v, %v", e, ok)
	}
	if _, ok := pt.edit([]byte(strings.Replace(targetDoc, "its second line", "its last line", 1))); ok {
		t.Error("a patch applied to text that changed")
	}
}
