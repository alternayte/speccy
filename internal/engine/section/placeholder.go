package section

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// KindPlaceholder is the kind of a template placeholder such as "<Product name>".
var KindPlaceholder = ast.NewNodeKind("Placeholder")

// Placeholder is angle-bracketed text that is not HTML and not a link: a template slot that
// the author has not filled (lint.placeholder). The parser keeps it out of raw HTML, so the
// preview can show it and lint can find it.
type Placeholder struct {
	ast.BaseInline
	Segment text.Segment
}

func (n *Placeholder) Kind() ast.NodeKind { return KindPlaceholder }

func (n *Placeholder) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"Text": string(n.Segment.Value(src))}, nil)
}

type placeholderParser struct{}

func (placeholderParser) Trigger() []byte { return []byte{'<'} }

var tagName = regexp.MustCompile(`^/?([A-Za-z][A-Za-z0-9-]*)`)

func (placeholderParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, seg := block.PeekLine()
	end := bytes.IndexByte(line, '>')
	if end < 2 {
		return nil
	}
	inner := string(line[1:end])
	if strings.ContainsAny(inner, "<") || !isLetter(inner[0]) {
		return nil
	}
	// An autolink or an e-mail address is a link, not a placeholder.
	if strings.Contains(inner, "://") || strings.Contains(inner, "@") || strings.HasPrefix(strings.ToLower(inner), "mailto:") {
		return nil
	}
	if m := tagName.FindStringSubmatch(inner); m != nil && IsHTMLTag(m[1]) {
		return nil
	}
	block.Advance(end + 1)
	return &Placeholder{Segment: text.NewSegment(seg.Start, seg.Start+end+1)}
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

type placeholderExt struct{}

func (placeholderExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(placeholderParser{}, 350)))
}

// htmlTags are HTML element names. "<table>" is markup; "<Product name>" is a placeholder.
var htmlTags = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`a abbr address area article aside audio b base bdi bdo blockquote body br
		button canvas caption cite code col colgroup data datalist dd del details dfn dialog div dl dt em embed
		fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 head header hr html i iframe img input ins kbd
		label legend li link main map mark meta meter nav noscript object ol optgroup option output p param
		picture pre progress q rp rt ruby s samp script section select small source span strong style sub
		summary sup svg table tbody td template textarea tfoot th thead time title tr track u ul var video wbr
		center font path g circle rect line`) {
		htmlTags[t] = true
	}
}

// IsHTMLTag reports whether name is an HTML element name.
func IsHTMLTag(name string) bool { return htmlTags[strings.ToLower(name)] }
