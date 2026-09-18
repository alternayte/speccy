package section

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse_Sections(t *testing.T) {
	src := "---\ntype: sdd\ntitle: X\n---\nIntro text.\n\n# Payments\n\nOwn payments text.\n\n## Retries\n\nThe client retries.\n\n### Backoff\n\nExponential.\n\n## Limits\n\n```md\n# not a heading\n```\n\n> # not a heading either\n\n# Security\nSetext child\n------------\n\nBody.\n"
	doc := Parse([]byte(src))

	if string(doc.Frontmatter) != "type: sdd\ntitle: X\n" {
		t.Errorf("frontmatter = %q", doc.Frontmatter)
	}
	if doc.BodyStart != strings.Index(src, "Intro") {
		t.Errorf("body start = %d, want %d", doc.BodyStart, strings.Index(src, "Intro"))
	}

	type want struct {
		level int
		path  []string
		own   string
	}
	wants := []want{
		{0, []string{}, "Intro text."},
		{1, []string{"Payments"}, "Own payments text."},
		{2, []string{"Payments", "Retries"}, "The client retries."},
		{3, []string{"Payments", "Retries", "Backoff"}, "Exponential."},
		{2, []string{"Payments", "Limits"}, "```md\n# not a heading\n```\n\n> # not a heading either"},
		{1, []string{"Security"}, ""},
		{2, []string{"Security", "Setext child"}, "Body."},
	}
	if len(doc.Sections) != len(wants) {
		for _, s := range doc.Sections {
			t.Logf("%d %v", s.Level, s.Path)
		}
		t.Fatalf("got %d sections, want %d", len(doc.Sections), len(wants))
	}
	for i, w := range wants {
		s := doc.Sections[i]
		if s.Level != w.level || !reflect.DeepEqual(s.Path, w.path) {
			t.Errorf("section %d = level %d path %v, want level %d path %v", i, s.Level, s.Path, w.level, w.path)
		}
		if got := Normalize(s.Own([]byte(src))); got != w.own {
			t.Errorf("section %d own = %q, want %q", i, got, w.own)
		}
	}

	payments := doc.Sections[1]
	if src[payments.Start:payments.End] != src[strings.Index(src, "# Payments"):strings.Index(src, "# Security")] {
		t.Errorf("Payments spans %q", src[payments.Start:payments.End])
	}
	if !strings.HasPrefix(src[doc.Sections[6].Start:], "Setext child\n---") {
		t.Errorf("setext section starts at %q", src[doc.Sections[6].Start:])
	}
}

func TestHash_Normalization(t *testing.T) {
	base := Hash([]byte("Line one.\n\nLine two.\n"))
	same := []string{
		"Line one.\r\n\r\nLine two.\r\n",
		"Line one.   \n\n\n\nLine two.\t\n",
		"\n\nLine one.\n\nLine two.",
	}
	for _, s := range same {
		if got := Hash([]byte(s)); got != base {
			t.Errorf("Hash(%q) differs from the normalized form", s)
		}
	}
	for _, s := range []string{"Line one.\nLine two.\n", "Line one.\n\nLine  two.\n", " Line one.\n\nLine two.\n"} {
		if Hash([]byte(s)) == base {
			t.Errorf("Hash(%q) equals the base hash; the change is not whitespace-only", s)
		}
	}
	if !strings.HasPrefix(base, "sha256:") || len(base) != len("sha256:")+64 {
		t.Errorf("hash format %q", base)
	}
}

func TestParse_ChildEditKeepsParentHash(t *testing.T) {
	a := Parse([]byte("# A\n\nParent.\n\n## B\n\nChild.\n"))
	b := Parse([]byte("# A\n\nParent.\n\n## B\n\nChild edited.\n"))
	if a.Sections[0].Hash != b.Sections[0].Hash {
		t.Error("editing a child section changed the parent's hash")
	}
	if a.Sections[1].Hash == b.Sections[1].Hash {
		t.Error("editing a section did not change its hash")
	}
}

func TestSplitFrontmatter_None(t *testing.T) {
	for _, src := range []string{"# Title\n", "---\nno closing line\n", "text\n---\na: b\n---\n"} {
		if fm, off := SplitFrontmatter([]byte(src)); fm != nil || off != 0 {
			t.Errorf("SplitFrontmatter(%q) = %q, %d; want none", src, fm, off)
		}
	}
}
