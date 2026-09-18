package http

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
)

// RenderMarkdown renders markdown to HTML on the server (DEC-017), with the parser that the
// review engine uses. Each block carries its source position for overlays and scroll sync.
func (core) RenderMarkdown(_ context.Context, req api.RenderMarkdownRequestObject) (api.RenderMarkdownResponseObject, error) {
	var dir string
	if req.Body.Path != nil {
		dir = path.Dir(*req.Body.Path)
	}
	out, err := Render([]byte(req.Body.Markdown), req.Body.BundleId, dir)
	if err != nil {
		return nil, err
	}
	return api.RenderMarkdown200JSONResponse{Html: out}, nil
}

// Render returns the HTML for src. When bundle is set, relative image links load from that
// bundle, and relative links to other files carry data-bundle-path for the client to open.
func Render(src []byte, bundle *uuid.UUID, dir string) (string, error) {
	fm, bodyStart := section.SplitFrontmatter(src)
	body := src[bodyStart:]
	md := section.Markdown(
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			// Doc text is untrusted (SDD §14.3): raw HTML is not rendered.
			renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100)),
		),
	)
	doc := md.Parser().Parse(text.NewReader(body))
	lines := newLineIndex(src)
	annotate(doc, body, bodyStart, lines)
	rewriteLinks(doc, bundle, dir)

	var b bytes.Buffer
	if fm != nil {
		fmt.Fprintf(&b, `<pre class="frontmatter" data-src-start="0" data-src-end="%d" data-line="1"><code>%s</code></pre>`+"\n",
			bodyStart, html.EscapeString(string(fm)))
	}
	if err := md.Renderer().Render(&b, body, doc); err != nil {
		return "", err
	}
	return b.String(), nil
}

// annotate sets data-src-start, data-src-end, and data-line on every block node.
func annotate(doc ast.Node, body []byte, offset int, lines lineIndex) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Type() != ast.TypeBlock || n.Kind() == ast.KindDocument {
			return ast.WalkContinue, nil
		}
		start, end, ok := blockSpan(n, body)
		if !ok {
			return ast.WalkContinue, nil
		}
		start, end = start+offset, end+offset
		n.SetAttributeString("data-src-start", []byte(strconv.Itoa(start)))
		n.SetAttributeString("data-src-end", []byte(strconv.Itoa(end)))
		n.SetAttributeString("data-line", []byte(strconv.Itoa(lines.line(start))))
		return ast.WalkContinue, nil
	})
}

// blockSpan returns the byte range of whole source lines that a block covers.
func blockSpan(n ast.Node, src []byte) (int, int, bool) {
	start, end := -1, -1
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || c.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		ls := c.Lines()
		for i := 0; i < ls.Len(); i++ {
			seg := ls.At(i)
			if start < 0 || seg.Start < start {
				start = seg.Start
			}
			if seg.Stop > end {
				end = seg.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if fc, ok := n.(*ast.FencedCodeBlock); ok {
		// Include the fence lines: the line before the content, and the line after it.
		if fc.Info != nil && (start < 0 || fc.Info.Segment.Start < start) {
			start = fc.Info.Segment.Start
		} else if start > 0 {
			start = lineStartOf(src, start-1)
		}
		if end >= 0 && end < len(src) {
			end = lineEndOf(src, end)
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	return lineStartOf(src, start), lineEndOf(src, maxInt(end-1, start)), true
}

func lineStartOf(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	return bytes.LastIndexByte(src[:off], '\n') + 1
}

func lineEndOf(src []byte, off int) int {
	if off >= len(src) {
		return len(src)
	}
	if i := bytes.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i + 1
	}
	return len(src)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type lineIndex []int // byte offset of the start of each line

func newLineIndex(src []byte) lineIndex {
	idx := lineIndex{0}
	for i, c := range src {
		if c == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// line returns the 1-based line number of a byte offset.
func (l lineIndex) line(off int) int {
	lo, hi := 0, len(l)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if l[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// rewriteLinks points relative images at the bundle file endpoint, and marks relative links
// to bundle files so the client can open them in the editor.
func rewriteLinks(doc ast.Node, bundle *uuid.UUID, dir string) {
	if bundle == nil {
		return
	}
	resolve := func(dest []byte) (string, bool) {
		u, err := url.Parse(string(dest))
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
			return "", false
		}
		p, err := url.PathUnescape(u.Path)
		if err != nil {
			return "", false
		}
		clean, err := source.CleanPath(path.Join(dir, p))
		if err != nil {
			return "", false
		}
		return clean, true
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Image:
			if p, ok := resolve(t.Destination); ok {
				t.Destination = []byte("/api/v1/bundles/" + bundle.String() + "/files/content?path=" + url.QueryEscape(p))
			}
		case *ast.Link:
			if p, ok := resolve(t.Destination); ok {
				t.SetAttributeString("data-bundle-path", []byte(p))
			}
		}
		return ast.WalkContinue, nil
	})
}

// codeRenderer renders code blocks: Mermaid as <pre class="mermaid"> for the client to draw
// (REQ-004), other languages highlighted with chroma CSS classes.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderCode)
	reg.Register(ast.KindCodeBlock, renderCode)
	reg.Register(ast.KindRawHTML, renderRawHTML)
	reg.Register(section.KindPlaceholder, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			seg := n.(*section.Placeholder).Segment
			_, _ = w.WriteString(`<span class="placeholder">` + html.EscapeString(string(seg.Value(src))) + `</span>`)
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(ast.KindHTMLBlock, renderHTMLBlock)
}

// placeholderTag matches "<Product name>": angle brackets around words that are not HTML.
var placeholderTag = regexp.MustCompile(`<(/?)([A-Za-z][A-Za-z0-9-]*)([^<>]*)>`)

// renderPlaceholders writes raw HTML as text when every tag in it is a placeholder, so an
// author sees "<Product name>" in the preview. Real HTML stays out (SDD §14.3).
func renderPlaceholders(w util.BufWriter, raw []byte, class string) bool {
	tags := placeholderTag.FindAllSubmatch(raw, -1)
	if len(tags) == 0 {
		return false
	}
	for _, t := range tags {
		if section.IsHTMLTag(string(t[2])) {
			return false
		}
	}
	_, _ = w.WriteString(`<span class="` + class + `">` + html.EscapeString(string(raw)) + `</span>`)
	return true
}

func renderRawHTML(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	var raw bytes.Buffer
	segs := n.(*ast.RawHTML).Segments
	for i := 0; i < segs.Len(); i++ {
		s := segs.At(i)
		raw.Write(s.Value(src))
	}
	if !renderPlaceholders(w, raw.Bytes(), "placeholder") {
		_, _ = w.WriteString("<!-- raw HTML omitted -->")
	}
	return ast.WalkSkipChildren, nil
}

func renderHTMLBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var raw bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		s := n.Lines().At(i)
		raw.Write(s.Value(src))
	}
	_, _ = w.WriteString("<p")
	gmhtml.RenderAttributes(w, n, nil)
	_, _ = w.WriteString(">")
	if !renderPlaceholders(w, bytes.TrimSpace(raw.Bytes()), "placeholder") {
		_, _ = w.WriteString("<!-- raw HTML omitted -->")
	}
	_, _ = w.WriteString("</p>\n")
	return ast.WalkSkipChildren, nil
}

var chromaFormatter = chromahtml.New(chromahtml.WithClasses(true), chromahtml.PreventSurroundingPre(true))

func renderCode(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var lang string
	if fc, ok := n.(*ast.FencedCodeBlock); ok {
		lang = strings.ToLower(string(fc.Language(src)))
	}
	var code bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		code.Write(seg.Value(src))
	}
	attrs := func(class string) {
		_, _ = w.WriteString(`<pre class="` + class + `"`)
		gmhtml.RenderAttributes(w, n, nil)
		_, _ = w.WriteString(">")
	}
	if lang == "mermaid" {
		attrs("mermaid")
		_, _ = w.WriteString(html.EscapeString(code.String()))
		_, _ = w.WriteString("</pre>\n")
		return ast.WalkSkipChildren, nil
	}
	attrs("code chroma")
	_, _ = w.WriteString("<code")
	if lang != "" {
		_, _ = w.WriteString(` class="language-` + html.EscapeString(lang) + `"`)
	}
	_, _ = w.WriteString(">")
	lexer := lexers.Get(lang)
	if lang == "" || lexer == nil {
		_, _ = w.WriteString(html.EscapeString(code.String()))
	} else {
		it, err := chroma.Coalesce(lexer).Tokenise(nil, code.String())
		if err != nil {
			_, _ = w.WriteString(html.EscapeString(code.String()))
		} else if err := chromaFormatter.Format(w, styles.Fallback, it); err != nil {
			return ast.WalkStop, err
		}
	}
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}
