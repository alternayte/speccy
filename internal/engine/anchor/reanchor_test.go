package anchor

import (
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
)

// T-080: anchors survive edits, or become detached. Never silently wrong.
func TestAnchor_Reanchor(t *testing.T) {
	const old = "# Payments\n\nIntro text.\n\n## Retries\n\nOn a timeout, the client retries the request up to three times.\n\n## Limits\n\nThe client retries nothing here.\n"
	quote := "the client retries the request"
	start := strings.Index(old, quote)
	a := New("SPEC.md", []byte(old), section.Parse([]byte(old)), start, start+len(quote))

	for _, c := range []struct {
		name string
		src  string
		want string // the text the anchor points at; "" means detached
	}{
		{"unchanged", old, quote},
		{"text added above", strings.Replace(old, "Intro text.", "Intro text, now longer.", 1), quote},
		{"context changed", strings.Replace(old, "On a timeout, ", "After an error, ", 1), quote},
		{"small edit in the quote", strings.Replace(old, "retries the request", "retries each request", 1), "the client retries each request"},
		{"quote deleted", strings.Replace(old, "On a timeout, the client retries the request up to three times.", "Nothing retries.", 1), ""},
		{"heading renamed", strings.Replace(old, "## Retries", "## Retry policy", 1), ""},
		{"quote moved to another section", strings.Replace(strings.Replace(old, "On a timeout, the client retries the request up to three times.\n", "", 1),
			"The client retries nothing here.", "On a timeout, the client retries the request up to three times.", 1), ""},
		{"rewritten beyond the threshold", strings.Replace(old, "the client retries the request", "a server resends all calls", 1), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := []byte(c.src)
			got, ok := Reanchor(a, src, section.Parse(src))
			if c.want == "" {
				if ok {
					t.Fatalf("want detached, got %q", src[got.Start:got.End])
				}
				return
			}
			if !ok {
				t.Fatalf("want %q, got detached", c.want)
			}
			if s := string(src[got.Start:got.End]); s != c.want || got.Quote != c.want {
				t.Fatalf("got %q (quote %q), want %q", s, got.Quote, c.want)
			}
			if strings.Join(got.HeadingPath, "/") != "Payments/Retries" {
				t.Fatalf("heading path %v", got.HeadingPath)
			}
		})
	}
}

// A whole-doc anchor on the frontmatter follows the frontmatter when a waiver changes it.
func TestAnchor_ReanchorFrontmatter(t *testing.T) {
	old := []byte("---\ntype: sdd\n---\n# A\n\nText.\n")
	d := section.Parse(old)
	a := New("SPEC.md", old, d, 0, d.BodyStart)
	src := []byte("---\ntype: sdd\nwaivers:\n  - check: x\n---\n# A\n\nText.\n")
	got, ok := Reanchor(a, src, section.Parse(src))
	if !ok || got.Start != 0 || got.Quote != "---\ntype: sdd\nwaivers:\n  - check: x\n---\n" {
		t.Fatalf("got %+v, %v", got, ok)
	}
}
