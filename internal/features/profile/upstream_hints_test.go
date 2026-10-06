package profile

import (
	"strings"
	"testing"
)

// #143: a rubric check that names a doc type of links.upstream and does not read the upstream
// docs gets a hint. A check that reads them, a check of another stage, and a word that only
// holds the type's letters get none.
func TestUpstreamHints(t *testing.T) {
	var l Loaded
	l.Profile.Links.Upstream = &Upstream{Kinds: []string{"implements"}, Types: []string{"prd"}}
	l.Profile.Checks = []Check{
		{Slug: "x.intro", Stage: "rubric", Question: "Is the context a delta?", PassWhen: "Neither reprints the PRD."},
		{Slug: "x.name", Stage: "rubric", Question: "Does it restate the product requirements document?", PassWhen: "It does not."},
		{Slug: "x.read", Stage: "rubric", Reads: []string{ReadsUpstream}, Question: "Is the context a delta?", PassWhen: "Neither reprints the PRD."},
		{Slug: "x.word", Stage: "rubric", Question: "Is the PRDX tool named?", PassWhen: "It is."},
		{Slug: "x.link", Stage: "coherence", Question: "Does the doc link the PRD?", PassWhen: "It does."},
	}
	var got []string
	for _, h := range UpstreamHints(l) {
		got = append(got, h.Slug+"="+h.Type)
	}
	if want := "x.intro=PRD x.name=product requirements document"; strings.Join(got, " ") != want {
		t.Errorf("hints = %v, want %s", got, want)
	}
}
