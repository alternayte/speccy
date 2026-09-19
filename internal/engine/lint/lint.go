// Package lint runs the deterministic writing rules of Appendix B (SDD §8.2). It never calls
// a model (REQ-060). Findings describe writing, never authorship (DEC-012).
package lint

import (
	"sort"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/kernel"
)

// Rule slugs (Appendix B).
const (
	Placeholder      = "lint.placeholder"
	RequiredHeadings = "lint.required-headings"
	BrokenLink       = "lint.broken-link"
	DuplicateID      = "lint.duplicate-id"
	DanglingRef      = "lint.dangling-ref"
	SlopPhrase       = "lint.slop-phrase"
	SentenceLength   = "lint.sentence-length"
	Weasel           = "lint.weasel"
	UndefinedAcronym = "lint.undefined-acronym"
	RFC2119Case      = "lint.rfc2119-case"
	ProseLimit       = "lint.prose-limit"
	AssetNudge       = "lint.asset-nudge"
	PassiveVoice     = "lint.passive-voice"
)

// Rules lists every rule with its default level.
var Rules = []struct {
	Slug  string
	Level kernel.Level
}{
	{Placeholder, kernel.Must},
	{RequiredHeadings, kernel.Must},
	{BrokenLink, kernel.Must},
	{DuplicateID, kernel.Must},
	{DanglingRef, kernel.Should},
	{SlopPhrase, kernel.Should},
	{SentenceLength, kernel.Should},
	{Weasel, kernel.Should},
	{UndefinedAcronym, kernel.Should},
	{RFC2119Case, kernel.Should},
	{ProseLimit, kernel.Should},
	{AssetNudge, kernel.Should},
	{PassiveVoice, kernel.Info},
}

// Heading is a heading that the profile template requires.
type Heading struct {
	Level int
	Title string
}

// Config is everything the rules need from the profile and the bundle.
type Config struct {
	Path              string   // the main doc's path in the bundle
	Files             []string // every file path in the bundle, for broken links
	MaxWords          int
	MaxSectionWords   int
	MaxSentenceWords  int
	MaxCodeBlockLines int
	MaxTableRows      int
	Prefixes          []string // trace ID prefixes this doc uses (REQ-051)
	// UpstreamPrefixes are resolved against linked bundles, not this doc. UpstreamIDs holds the
	// IDs that the linked upstream bundles define; when it is nil (no upstream bundle), a
	// reference with an upstream prefix is never dangling.
	UpstreamPrefixes []string
	UpstreamIDs      map[string]bool
	Required         []Heading
	SlopExtra        []string
	// Levels overrides a rule's default level. "off" turns the rule off (REQ-062).
	Levels map[string]string
}

// Finding is one failed check with its anchor (REQ-023).
type Finding struct {
	Slug    string
	Level   kernel.Level
	Message string
	Fix     string
	Anchor  anchor.Anchor
}

// Result is the output of a lint run.
type Result struct {
	Findings []Finding
	// Rules is every rule that ran, with the level it ran at. A rule with no finding passed.
	Rules map[string]kernel.Level
}

// doc is the parsed main doc that the rules read.
type doc struct {
	src      []byte
	body     []byte
	offset   int // where body starts in src
	sections section.Doc
	root     ast.Node
	blocks   []prose
}

// prose is the plain text of one block, with a map back to source offsets.
type prose struct {
	node ast.Node
	kind proseKind
	text []byte
	pos  []int // pos[i] is the offset in body of text[i]
}

type proseKind int

const (
	kindParagraph proseKind = iota
	kindHeading
	kindCell
)

// Run lints src, the main doc, with cfg.
func Run(src []byte, cfg Config) Result {
	sd := section.Parse(src)
	body := src[sd.BodyStart:]
	root := section.Markdown().Parser().Parse(text.NewReader(body))
	d := &doc{src: src, body: body, offset: sd.BodyStart, sections: sd, root: root}
	d.blocks = collectProse(root, body)

	levels := map[string]kernel.Level{}
	for _, r := range Rules {
		lvl := r.Level
		if o, ok := cfg.Levels[r.Slug]; ok {
			if o == "off" {
				continue
			}
			lvl = kernel.Level(o)
		}
		levels[r.Slug] = lvl
	}

	var out []Finding
	emit := func(slug string, start, end int, msg, fix string) {
		lvl, ok := levels[slug]
		if !ok {
			return
		}
		out = append(out, Finding{Slug: slug, Level: lvl, Message: msg, Fix: fix,
			Anchor: anchor.New(cfg.Path, src, sd, start+d.offset, end+d.offset)})
	}
	for _, rule := range []func(*doc, Config, emitter){
		placeholders, requiredHeadings, brokenLinks, traceIDs, phrases, sentences,
		acronyms, rfc2119, proseLimits, assetNudges,
	} {
		rule(d, cfg, emit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Anchor.Start < out[j].Anchor.Start })
	return Result{Findings: out, Rules: levels}
}

// emitter records a finding at body offsets [start, end).
type emitter func(slug string, start, end int, msg, fix string)

// collectProse returns the text of each paragraph, heading, and table cell. Code, raw HTML,
// and link destinations are not prose; link text is.
func collectProse(root ast.Node, src []byte) []prose {
	var out []prose
	var cur *prose
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n.Kind() {
		case ast.KindParagraph, ast.KindTextBlock, ast.KindHeading, extast.KindTableCell:
			if entering {
				k := kindParagraph
				if n.Kind() == ast.KindHeading {
					k = kindHeading
				} else if n.Kind() == extast.KindTableCell {
					k = kindCell
				}
				cur = &prose{node: n, kind: k}
			} else if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			return ast.WalkContinue, nil
		case ast.KindCodeSpan, ast.KindRawHTML, ast.KindAutoLink, section.KindPlaceholder:
			return ast.WalkSkipChildren, nil
		case ast.KindText:
			if !entering || cur == nil {
				return ast.WalkContinue, nil
			}
			t := n.(*ast.Text)
			seg := t.Segment
			for i := seg.Start; i < seg.Stop; i++ {
				cur.text = append(cur.text, src[i])
				cur.pos = append(cur.pos, i)
			}
			if t.SoftLineBreak() || t.HardLineBreak() {
				cur.text = append(cur.text, ' ')
				cur.pos = append(cur.pos, seg.Stop)
			}
		case ast.KindString:
			// Typographic replacements have no source segment; they carry no prose rules.
		}
		return ast.WalkContinue, nil
	})
	return out
}

// span maps a range in p.text to body offsets.
func (p prose) span(i, j int) (int, int) {
	if j <= i {
		return p.pos[i], p.pos[i]
	}
	return p.pos[i], p.pos[j-1] + 1
}

// Definition is one trace ID definition (REQ-051): the ID, its offsets in the file, and the
// text of the item or heading that defines it.
type Definition struct {
	ID    string
	Start int
	End   int
	Text  string
}

// References returns every occurrence of a trace ID with one of prefixes that is not a
// definition (REQ-051), in order.
func References(src []byte, prefixes []string) []Definition {
	_, refs := traceUses(src, prefixes)
	return refs
}

// Definitions returns the trace ID definitions in src with one of prefixes, in order.
func Definitions(src []byte, prefixes []string) []Definition {
	defs, _ := traceUses(src, prefixes)
	return defs
}

func traceUses(src []byte, prefixes []string) (defs, refs []Definition) {
	sd := section.Parse(src)
	body := src[sd.BodyStart:]
	own := set(prefixes)
	for _, p := range collectProse(section.Markdown().Parser().Parse(text.NewReader(body)), body) {
		isDef := p.kind != kindCell && isDefinitionBlock(p.node)
		for i, m := range idRe.FindAllSubmatchIndex(p.text, -1) {
			if !own[string(p.text[m[2]:m[3]])] {
				continue
			}
			s, e := p.span(m[0], m[1])
			d := Definition{ID: string(p.text[m[0]:m[1]]), Start: s + sd.BodyStart, End: e + sd.BodyStart, Text: string(p.text)}
			if i == 0 && isDef && definitionAt(p.text, m[0], m[1]) {
				defs = append(defs, d)
			} else {
				refs = append(refs, d)
			}
		}
	}
	return defs, refs
}

// Paragraph is the plain text of one paragraph or list item paragraph, with its offsets in
// the file.
type Paragraph struct {
	Start int
	End   int
	Text  string
}

// Paragraphs returns the prose paragraphs of src, without headings and table cells.
func Paragraphs(src []byte) []Paragraph {
	sd := section.Parse(src)
	body := src[sd.BodyStart:]
	var out []Paragraph
	for _, p := range collectProse(section.Markdown().Parser().Parse(text.NewReader(body)), body) {
		if p.kind != kindParagraph || len(p.text) == 0 {
			continue
		}
		s, e := p.span(0, len(p.text))
		out = append(out, Paragraph{Start: s + sd.BodyStart, End: e + sd.BodyStart, Text: string(p.text)})
	}
	return out
}

// Item is the first block of one top-level list item: Start is where its text begins, after
// the list marker, as an offset in the file.
type Item struct {
	Start int
	Text  string
}

// ListItems returns the top-level list items of src (items not inside another item), in order.
func ListItems(src []byte) []Item {
	sd := section.Parse(src)
	body := src[sd.BodyStart:]
	root := section.Markdown().Parser().Parse(text.NewReader(body))
	var out []Item
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Kind() != ast.KindListItem {
			return ast.WalkContinue, nil
		}
		for p := n.Parent(); p != nil; p = p.Parent() {
			if p.Kind() == ast.KindListItem {
				return ast.WalkSkipChildren, nil
			}
		}
		first := n.FirstChild()
		if first == nil || first.Lines().Len() == 0 {
			return ast.WalkSkipChildren, nil
		}
		seg := first.Lines().At(0)
		line := first.Lines().At(first.Lines().Len() - 1)
		out = append(out, Item{Start: seg.Start + sd.BodyStart, Text: string(body[seg.Start:line.Stop])})
		return ast.WalkSkipChildren, nil
	})
	return out
}
