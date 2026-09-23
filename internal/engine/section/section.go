// Package section parses a markdown doc into sections with source positions (SDD §8.1).
// The review engine, the preview renderer, and anchors all use this parser (DEC-017).
package section

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Markdown returns the goldmark configuration that every consumer of doc text uses:
// GitHub-flavoured markdown. Callers add renderer options only.
func Markdown(opts ...goldmark.Option) goldmark.Markdown {
	return goldmark.New(append([]goldmark.Option{
		goldmark.WithExtensions(extension.GFM, placeholderExt{}),
	}, opts...)...)
}

// Doc is a parsed markdown file.
type Doc struct {
	// Frontmatter is the YAML between the opening and closing --- lines, or nil. The block may
	// sit in an HTML comment.
	Frontmatter []byte
	// BodyStart is the byte offset where the markdown after the frontmatter starts.
	BodyStart int
	// Sections are in document order. The first section is the preamble before the first
	// heading (level 0, empty path) when the doc has text there.
	Sections []Section
}

// Section is the text under one heading, down to the next heading of the same or higher level.
// Offsets are bytes into the whole file, including frontmatter.
type Section struct {
	Level int
	Title string
	// Path is the titles from the top-level heading down to this one.
	Path []string
	// Start is the start of the heading line. For the preamble it is BodyStart.
	Start int
	// BodyStart is the first byte after the heading.
	BodyStart int
	// OwnEnd is where the section's own content ends: the first child heading, or End.
	OwnEnd int
	// End is where the section ends, child sections included.
	End int
	// Hash is "sha256:<hex>" of the normalized own content.
	Hash string
}

// Own returns the section's own content: the text after the heading, without child sections.
func (s Section) Own(src []byte) []byte { return src[s.BodyStart:s.OwnEnd] }

var frontmatterRe = regexp.MustCompile(`\A---[ \t]*\r?\n`)
var frontmatterEndRe = regexp.MustCompile(`(?m)^(---|\.\.\.)[ \t]*(\r?\n|\z)`)

// A wiki shows an HTML comment as nothing, so a team hides its frontmatter in one: a <!-- line,
// the --- block, and a --> line, with blank lines around them (#76).
var wrappedOpenRe = regexp.MustCompile(`\A(?:[ \t]*\r?\n)*<!--[ \t]*\r?\n(?:[ \t]*\r?\n)*---[ \t]*\r?\n`)
var wrappedCloseRe = regexp.MustCompile(`\A(?:[ \t]*\r?\n)*-->[ \t]*(?:\r?\n|\z)`)

// Form is how a file holds its frontmatter block, so a write keeps it: the text before and
// after the block, and whether the block is JSON.
type Form struct {
	// Open is the text before the block, and Close the text after it, up to the body.
	Open, Close []byte
	// Wrapped says the block is inside an HTML comment.
	Wrapped bool
	// JSON says the block is a JSON object, and Indent is its indent unit.
	JSON   bool
	Indent string
}

// PlainForm is the form of a new frontmatter block.
var PlainForm = Form{Open: []byte("---\n"), Close: []byte("---\n")}

// SplitFrontmatter returns the YAML frontmatter and the offset of the body.
// A file without an opening --- line, plain or in an HTML comment, has no frontmatter.
func SplitFrontmatter(src []byte) (fm []byte, bodyStart int) {
	fm, bodyStart, _ = Frontmatter(src)
	return fm, bodyStart
}

// Frontmatter returns the frontmatter, the offset of the body, and the form of the block. A
// file with no frontmatter gets PlainForm.
func Frontmatter(src []byte) (fm []byte, bodyStart int, form Form) {
	if open := frontmatterRe.Find(src); open != nil {
		rest := src[len(open):]
		loc := frontmatterEndRe.FindIndex(rest)
		if loc == nil {
			return nil, 0, PlainForm
		}
		fm = rest[:loc[0]]
		form = Form{Open: open, Close: rest[loc[0]:loc[1]]}
		return fm, len(open) + loc[1], withBody(form, fm)
	}
	open := wrappedOpenRe.Find(src)
	if open == nil {
		return nil, 0, PlainForm
	}
	rest := src[len(open):]
	loc := frontmatterEndRe.FindIndex(rest)
	if loc == nil {
		return nil, 0, PlainForm
	}
	end := wrappedCloseRe.Find(rest[loc[1]:])
	if end == nil {
		return nil, 0, PlainForm
	}
	fm = rest[:loc[0]]
	form = Form{Open: open, Close: rest[loc[0] : loc[1]+len(end)], Wrapped: true}
	return fm, len(open) + loc[1] + len(end), withBody(form, fm)
}

// withBody sets whether the block fm is JSON, and its indent unit.
func withBody(form Form, fm []byte) Form {
	t := bytes.TrimSpace(fm)
	if len(t) == 0 || t[0] != '{' {
		return form
	}
	form.JSON, form.Indent = true, "  "
	for _, line := range strings.Split(string(fm), "\n")[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if ind := line[:len(line)-len(strings.TrimLeft(line, " \t"))]; ind != "" {
			form.Indent = ind
		}
		break
	}
	return form
}

// Parse parses src into frontmatter and sections.
func Parse(src []byte) Doc {
	fm, bodyStart := SplitFrontmatter(src)
	body := src[bodyStart:]
	root := Markdown().Parser().Parse(text.NewReader(body))

	type heading struct {
		level        int
		title        string
		start, after int
	}
	var heads []heading
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Lines().Len() == 0 {
			continue
		}
		start := lineStart(body, h.Lines().At(0).Start)
		after := headingEnd(body, h)
		heads = append(heads, heading{level: h.Level, title: plainText(h, body), start: start, after: after})
	}

	var sections []Section
	firstHead := len(body)
	if len(heads) > 0 {
		firstHead = heads[0].start
	}
	if len(bytes.TrimSpace(body[:firstHead])) > 0 {
		sections = append(sections, Section{
			Level: 0, Path: []string{}, Start: 0, BodyStart: 0, OwnEnd: firstHead, End: firstHead,
		})
	}
	var stack []Section // open ancestors, for the path
	for i, h := range heads {
		end := len(body)
		for _, next := range heads[i+1:] {
			if next.level <= h.level {
				end = next.start
				break
			}
		}
		ownEnd := end
		if i+1 < len(heads) && heads[i+1].start < end {
			ownEnd = heads[i+1].start
		}
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.level {
			stack = stack[:len(stack)-1]
		}
		path := make([]string, 0, len(stack)+1)
		for _, a := range stack {
			path = append(path, a.Title)
		}
		path = append(path, h.title)
		s := Section{Level: h.level, Title: h.title, Path: path, Start: h.start, BodyStart: h.after, OwnEnd: ownEnd, End: end}
		stack = append(stack, s)
		sections = append(sections, s)
	}
	for i := range sections {
		s := &sections[i]
		s.Hash = Hash(body[s.BodyStart:s.OwnEnd])
		s.Start += bodyStart
		s.BodyStart += bodyStart
		s.OwnEnd += bodyStart
		s.End += bodyStart
	}
	return Doc{Frontmatter: fm, BodyStart: bodyStart, Sections: sections}
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

// Normalize applies the §8.1 normalizations: line endings to \n, trailing spaces removed,
// runs of blank lines collapsed to one. Leading and trailing blank lines are removed too,
// so the blank line after a heading does not count.
func Normalize(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	s = blankRuns.ReplaceAllString(s, "\n\n")
	return strings.Trim(s, "\n")
}

// Hash returns "sha256:<hex>" of the normalized text. It is the section hash (SDD §4).
func Hash(own []byte) string {
	sum := sha256.Sum256([]byte(Normalize(own)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func lineStart(src []byte, off int) int {
	return bytes.LastIndexByte(src[:off], '\n') + 1
}

func lineEnd(src []byte, off int) int {
	if i := bytes.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i + 1
	}
	return len(src)
}

var setextUnderline = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*\r?$`)

// headingEnd returns the offset after the heading's last source line. A setext heading
// ends after its underline.
func headingEnd(src []byte, h *ast.Heading) int {
	last := h.Lines().At(h.Lines().Len() - 1)
	end := lineEnd(src, last.Stop-1)
	if end < len(src) {
		next := src[end:lineEnd(src, end)]
		if setextUnderline.Match(bytes.TrimRight(next, "\n")) {
			return lineEnd(src, end)
		}
	}
	return end
}

// plainText returns the text of a heading without markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.CodeSpan:
			for cc := t.FirstChild(); cc != nil; cc = cc.NextSibling() {
				if tx, ok := cc.(*ast.Text); ok {
					b.Write(tx.Segment.Value(src))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}
