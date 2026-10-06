package review

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
)

// pointedSections returns the Pointed-to sections of sec, in doc order: the sections of the
// same doc that the text of sec links to by a heading anchor, or names by the full title of a
// heading (#139). A title of one word counts only as a link: a word such as "Data" gives false
// matches. Speccy follows pointers one level deep, so a pointed-to section's own pointers add
// nothing.
//
// sec with its subsections and the sections that hold sec are not pointed-to sections: the
// check reads them already, or they would bring in the whole doc. A section inside another
// pointed-to section is left out, because that section holds it.
func pointedSections(docPath string, src []byte, doc section.Doc, sec *section.Section) []*section.Section {
	if sec == nil {
		return nil
	}
	ids := headingIDs(doc)
	tree := sec.Tree(src)
	root := section.Markdown().Parser().Parse(text.NewReader(tree))
	var words strings.Builder
	picked := map[*section.Section]bool{}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Heading:
			// A subsection heading is part of the section, not a pointer.
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if s := ids[linkAnchor(docPath, string(t.Destination))]; s != nil {
				picked[s] = true
			}
		case *ast.Text:
			words.Write(t.Segment.Value(tree))
			words.WriteByte(' ')
		case *ast.String:
			words.Write(t.Value)
			words.WriteByte(' ')
		}
		if n.Type() == ast.TypeBlock {
			words.WriteByte(' ')
		}
		return ast.WalkContinue, nil
	})
	said := " " + strings.ToLower(strings.Join(strings.Fields(words.String()), " ")) + " "
	named := map[string]bool{}
	for i := range doc.Sections {
		s := &doc.Sections[i]
		title := lint.NormTitle(s.Title)
		if s.Level == 0 || len(strings.Fields(title)) < 2 || named[title] {
			continue
		}
		// Two headings with one title: the first one is the pointed-to section, as for the
		// section that a check names.
		named[title] = true
		if mentions(said, title) {
			picked[s] = true
		}
	}
	var out []*section.Section
	for i := range doc.Sections {
		s := &doc.Sections[i]
		if !picked[s] || within(s, sec) || within(sec, s) {
			continue
		}
		inside := false
		for o := range picked {
			if o != s && !within(sec, o) && !within(o, sec) && within(s, o) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, s)
		}
	}
	return out
}

// within reports whether section a lies inside section b, or is b.
func within(a, b *section.Section) bool { return a.Start >= b.Start && a.End <= b.End }

// mentions reports whether said holds title as whole words. said is lower case with single
// spaces.
func mentions(said, title string) bool {
	for from := 0; ; {
		i := strings.Index(said[from:], title)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(title)
		if !wordRune(lastRune(said[:i])) && !wordRune(firstRune(said[end:])) {
			return true
		}
		from = i + 1
	}
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return ' '
	}
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return ' '
}

// linkAnchor returns the heading anchor that a link destination points to in the doc at
// docPath, in lower case, or "" for a link to another file, to a web page, or with no anchor.
func linkAnchor(docPath, dest string) string {
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Fragment == "" {
		return ""
	}
	if u.Path != "" {
		p, err := url.PathUnescape(u.Path)
		if err != nil {
			p = u.Path
		}
		if path.Clean(path.Join(path.Dir(docPath), p)) != path.Clean(docPath) {
			return ""
		}
	}
	return strings.ToLower(u.Fragment)
}

// headingIDs maps each anchor of a heading of the doc to its section. A heading has the anchor
// that the preview gives it and the anchor that GitHub gives it; a second heading with the same
// anchor gets "-1" after it, and so on.
func headingIDs(doc section.Doc) map[string]*section.Section {
	out := map[string]*section.Section{}
	seen := [2]map[string]int{{}, {}}
	for i := range doc.Sections {
		s := &doc.Sections[i]
		if s.Level == 0 {
			continue
		}
		for k, id := range []string{previewID(s.Title), githubID(s.Title)} {
			n := seen[k][id]
			seen[k][id]++
			if n > 0 {
				id += "-" + strconv.Itoa(n)
			}
			if _, ok := out[id]; !ok {
				out[id] = s
			}
		}
	}
	return out
}

// previewID is the anchor that the preview renderer (goldmark) gives a heading title: ASCII
// letters and digits in lower case, with a hyphen for a space, a hyphen or an underscore.
func previewID(title string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
		case r == ' ' || r == '\t' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "heading"
	}
	return b.String()
}

// githubID is the anchor that GitHub gives a heading title: letters, digits, hyphens and
// underscores in lower case, with a hyphen for each space.
func githubID(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}
