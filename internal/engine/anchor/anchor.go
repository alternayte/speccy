// Package anchor points findings and threads at text in a doc, and finds that text again
// after an edit (SDD §8.8).
package anchor

import (
	"unicode/utf8"

	"github.com/alternayte/speccy/internal/engine/section"
)

// contextBytes is the length of the prefix and the suffix around a quote.
const contextBytes = 32

// Anchor is a range of text in one file, with enough context to find it again.
type Anchor struct {
	File        string   `json:"file"`
	HeadingPath []string `json:"heading_path"`
	Quote       string   `json:"quote"`
	Prefix      string   `json:"prefix"`
	Suffix      string   `json:"suffix"`
	Start       int      `json:"start"`
	End         int      `json:"end"`
}

// New returns the anchor for src[start:end] in file. doc is src parsed with section.Parse.
func New(file string, src []byte, doc section.Doc, start, end int) Anchor {
	path := []string{}
	for _, s := range doc.Sections {
		if s.Start <= start && start < s.End && s.Level > 0 {
			path = s.Path
		}
	}
	return Anchor{
		File:        file,
		HeadingPath: path,
		Quote:       string(src[start:end]),
		Prefix:      string(src[runeStart(src, start-contextBytes):start]),
		Suffix:      string(src[end:runeEnd(src, end+contextBytes)]),
		Start:       start,
		End:         end,
	}
}

// runeStart moves i forward to the start of a rune, and clamps it to src.
func runeStart(src []byte, i int) int {
	if i <= 0 {
		return 0
	}
	for i < len(src) && !utf8.RuneStart(src[i]) {
		i++
	}
	return i
}

// runeEnd moves i back to the start of a rune, and clamps it to src.
func runeEnd(src []byte, i int) int {
	if i >= len(src) {
		return len(src)
	}
	for i > 0 && !utf8.RuneStart(src[i]) {
		i--
	}
	return i
}
