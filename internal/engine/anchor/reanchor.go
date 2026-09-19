package anchor

import (
	"bytes"
	"slices"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/alternayte/speccy/internal/engine/section"
)

// fuzzyThreshold is the diff-match-patch match threshold for the last re-anchor step (SDD §8.8).
const fuzzyThreshold = 0.3

// Reanchor finds a in src, a newer text of the same file, in the order of SDD §8.8: the exact
// prefix, quote, and suffix; the exact quote under the same heading path; a fuzzy match under
// the same heading path. ok is false when all three fail: the anchor is detached. A quote is
// never matched outside its heading path, so an anchor is never silently wrong.
func Reanchor(a Anchor, src []byte, doc section.Doc) (Anchor, bool) {
	// A whole-doc anchor is the frontmatter. It follows the frontmatter when that changes.
	if a.Start == 0 && len(a.HeadingPath) == 0 && doc.BodyStart > 0 && isFrontmatter(a.Quote) {
		return New(a.File, src, doc, 0, doc.BodyStart), true
	}
	return reanchor(a, src, doc)
}

func isFrontmatter(quote string) bool {
	fm, end := section.SplitFrontmatter([]byte(quote))
	return fm != nil && end == len(quote)
}

func reanchor(a Anchor, src []byte, doc section.Doc) (Anchor, bool) {
	if a.Quote == "" {
		lo, _, found := region(doc, src, a.HeadingPath)
		if !found {
			return a, false
		}
		return New(a.File, src, doc, lo, lo), true
	}
	full := []byte(a.Prefix + a.Quote + a.Suffix)
	if i := nearest(src, full, 0, len(src), a.Start-len(a.Prefix)); i >= 0 {
		s := i + len(a.Prefix)
		if n := New(a.File, src, doc, s, s+len(a.Quote)); slices.Equal(n.HeadingPath, a.HeadingPath) {
			return n, true
		}
	}
	lo, hi, found := region(doc, src, a.HeadingPath)
	if !found {
		return a, false
	}
	if i := nearest(src, []byte(a.Quote), lo, hi, a.Start); i >= 0 {
		if n := New(a.File, src, doc, i, i+len(a.Quote)); slices.Equal(n.HeadingPath, a.HeadingPath) {
			return n, true
		}
	}
	if s, e, ok := fuzzy(src[lo:hi], a.Quote, a.Start-lo); ok {
		if n := New(a.File, src, doc, lo+s, lo+e); slices.Equal(n.HeadingPath, a.HeadingPath) {
			return n, true
		}
	}
	return a, false
}

// region returns the own content of the section with the heading path, with its heading line.
// The empty path is the text before the first heading.
func region(doc section.Doc, src []byte, path []string) (lo, hi int, ok bool) {
	if len(path) == 0 {
		hi = len(src)
		for _, s := range doc.Sections {
			if s.Level > 0 {
				hi = s.Start
				break
			}
		}
		return 0, hi, true
	}
	for _, s := range doc.Sections {
		if s.Level > 0 && slices.Equal(s.Path, path) {
			return s.Start, s.OwnEnd, true
		}
	}
	return 0, 0, false
}

// nearest returns the start of the occurrence of pat in src[lo:hi] that is closest to near,
// or -1.
func nearest(src, pat []byte, lo, hi, near int) int {
	best := -1
	for from := lo; from <= hi-len(pat); {
		i := bytes.Index(src[from:hi], pat)
		if i < 0 {
			break
		}
		i += from
		if best < 0 || abs(i-near) < abs(best-near) {
			best = i
		}
		from = i + 1
	}
	return best
}

// fuzzy finds text close to quote in text, near loc. The match must differ from the quote by
// at most fuzzyThreshold of the quote's length.
func fuzzy(text []byte, quote string, loc int) (start, end int, ok bool) {
	dmp := diffmatchpatch.New()
	dmp.MatchThreshold = fuzzyThreshold
	dmp.MatchDistance = max(len(text), 1)
	t := string(text)
	loc = min(max(loc, 0), len(t))
	bits := dmp.MatchMaxBits
	if len(quote) <= bits {
		start = dmp.MatchMain(t, quote, loc)
		if start < 0 {
			return 0, 0, false
		}
		// An edit can make the text longer or shorter than the quote: take the end that differs least.
		best := -1
		for e := start + len(quote)*7/10; e <= min(start+len(quote)*13/10, len(t)); e++ {
			if d := dmp.DiffLevenshtein(dmp.DiffMain(quote, t[start:e], false)); best < 0 || d < best {
				best, end = d, e
			}
		}
	} else {
		start = dmp.MatchMain(t, quote[:bits], loc)
		if start < 0 {
			return 0, 0, false
		}
		last := dmp.MatchMain(t, quote[len(quote)-bits:], start+len(quote)-bits)
		if last < 0 || last < start {
			return 0, 0, false
		}
		end = min(last+bits, len(t))
	}
	start, end = runeStart(text, start), runeEnd(text, end)
	if end <= start {
		return 0, 0, false
	}
	diffs := dmp.DiffMain(quote, t[start:end], false)
	if float64(dmp.DiffLevenshtein(diffs)) > fuzzyThreshold*float64(len(quote)) {
		return 0, 0, false
	}
	return start, end, true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
