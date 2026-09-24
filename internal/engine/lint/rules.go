package lint

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"

	"github.com/alternayte/speccy/internal/engine/ears"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/kernel"
)

// placeholders finds TBD, TODO, XXX, FIXME, {{…}}, <…>, and lorem ipsum in prose, and
// <…> placeholders that the parser took for raw HTML.
var placeholderRe = regexp.MustCompile(`(?i)\b(TBD|TODO|XXX|FIXME)\b|\{\{[^{}]*\}\}|lorem ipsum`)

func placeholders(d *doc, _ Config, emit emitter) {
	msg := "The text has a placeholder."
	fix := "Replace the placeholder with the real content, or delete it."
	for _, p := range d.blocks {
		for _, m := range placeholderRe.FindAllIndex(p.text, -1) {
			tok := p.text[m[0]:m[1]]
			if isCaseSensitiveMarker(tok) {
				continue
			}
			s, e := p.span(m[0], m[1])
			emit(Placeholder, s, e, msg, fix)
		}
	}
	// "<Product name>": the parser makes a Placeholder node, or an HTML block when it stands
	// alone on a line with attribute-like words.
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *section.Placeholder:
			emit(Placeholder, t.Segment.Start, t.Segment.Stop, msg, fix)
		case *ast.HTMLBlock:
			for i := 0; i < t.Lines().Len(); i++ {
				seg := t.Lines().At(i)
				for _, m := range htmlPlaceholderRe.FindAllSubmatchIndex(d.body[seg.Start:seg.Stop], -1) {
					if !section.IsHTMLTag(string(d.body[seg.Start+m[2] : seg.Start+m[3]])) {
						emit(Placeholder, seg.Start+m[0], seg.Start+m[1], msg, fix)
					}
				}
			}
		}
		return ast.WalkContinue, nil
	})
}

var htmlPlaceholderRe = regexp.MustCompile(`</?([A-Za-z][A-Za-z0-9-]*)[^<>]*>`)

// isCaseSensitiveMarker skips "xxx" and "todo" written in lower case inside words such as
// "Todo list": only the upper-case markers count.
func isCaseSensitiveMarker(tok []byte) bool {
	s := string(tok)
	switch strings.ToUpper(s) {
	case "TBD", "TODO", "XXX", "FIXME":
		return s != strings.ToUpper(s)
	}
	return false
}

func requiredHeadings(d *doc, cfg Config, emit emitter) {
	have := map[string]bool{}
	for _, s := range d.sections.Sections {
		if s.Level > 0 {
			have[NormTitle(s.Title)] = true
		}
	}
	at := 0
	for _, s := range d.sections.Sections {
		if s.Level == 1 {
			at = s.Start - d.offset
			break
		}
	}
	end := lineEnd(d.body, at)
	for _, h := range cfg.Required {
		if have[NormTitle(h.Title)] {
			continue
		}
		// A heading that only a larger doc must have is a note here, not a failure.
		if h.MinSize != "" && !cfg.Size.AtLeast(h.MinSize) {
			emit(RequiredHeadings, at, end,
				fmt.Sprintf("The template requires the %q section at size %s. This doc is size %s.", h.Title, h.MinSize, cfg.Size),
				fmt.Sprintf("Add a heading %q with its content, or leave it out at this size.", strings.Repeat("#", h.Level)+" "+h.Title),
				kernel.Info)
			continue
		}
		emit(RequiredHeadings, at, end,
			fmt.Sprintf("The template requires the %q section. The doc has none.", h.Title),
			fmt.Sprintf("Add a heading %q with its content.", strings.Repeat("#", h.Level)+" "+h.Title))
	}
}

// sectionNumber is a heading's own number: "5.", "7.2", "A.1", or "Appendix A".
var sectionNumber = regexp.MustCompile(`^(?:appendix\s+[a-z](?:\.\d+)*\.?|\d+(?:\.\d+)*\.?|[a-z](?:\.\d+)+\.?)\s+(?:[—–-]\s+)?`)

// NormTitle compares headings without case, extra spaces, or a section number, so "## 5.
// Decisions" has the template's "## Decisions".
func NormTitle(s string) string {
	t := strings.ToLower(strings.Join(strings.Fields(s), " "))
	return sectionNumber.ReplaceAllString(t, "")
}

func lineEnd(src []byte, off int) int {
	if off >= len(src) {
		return len(src)
	}
	if i := bytes.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i
	}
	return len(src)
}

func brokenLinks(d *doc, cfg Config, emit emitter) {
	files := map[string]bool{}
	dirs := map[string]bool{}
	for _, f := range cfg.Files {
		files[f] = true
		for dir := path.Dir(f); dir != "."; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	base := path.Dir(cfg.Path)
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		switch t := n.(type) {
		case *ast.Link:
			dest = t.Destination
		case *ast.Image:
			dest = t.Destination
		default:
			return ast.WalkContinue, nil
		}
		u, perr := url.Parse(string(dest))
		if perr != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
			return ast.WalkContinue, nil //nolint:nilerr // a destination that is not a URL is not a file link
		}
		p, uerr := url.PathUnescape(u.Path)
		if uerr != nil {
			p = u.Path
		}
		target := path.Clean(path.Join(base, p))
		if files[target] || dirs[target] {
			return ast.WalkContinue, nil
		}
		s, e := nodeSpan(n, d.body)
		if strings.HasPrefix(target, "../") || target == ".." {
			emit(BrokenLink, s, e, fmt.Sprintf("The link to %s points outside the bundle.", p),
				"Move the file into the bundle and link to it there, or use a full URL.")
		} else {
			emit(BrokenLink, s, e, fmt.Sprintf("The link points to %s, which is not in the bundle.", target),
				"Fix the path, or add the file to the bundle.")
		}
		return ast.WalkSkipChildren, nil
	})
}

// nodeSpan returns the body range of an inline node's text, or of its block's first line.
func nodeSpan(n ast.Node, body []byte) (int, int) {
	start, end := -1, -1
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			if start < 0 || t.Segment.Start < start {
				start = t.Segment.Start
			}
			if t.Segment.Stop > end {
				end = t.Segment.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if start >= 0 {
		return start, end
	}
	for p := n; p != nil; p = p.Parent() {
		if p.Type() == ast.TypeBlock && p.Lines().Len() > 0 {
			s := p.Lines().At(0)
			return s.Start, s.Stop
		}
	}
	return 0, 0
}

// traceIDs checks definitions and references (REQ-051): duplicates and dangling references.
// It also says when an ID-like token at a definition place has a prefix the profile does not
// read, and when a requirements section defines no ID at all.
var idRe = regexp.MustCompile(`\b([A-Z]{2,6})-(\d+)\b`)

func traceIDs(d *doc, cfg Config, emit emitter) {
	own := set(cfg.Prefixes)
	upstream := set(cfg.UpstreamPrefixes)
	type use struct{ start, end int }
	defs := map[string][]use{}
	var refs []struct {
		id string
		use
	}
	unknown := map[string][]use{}
	var unknownOrder []string
	for _, p := range d.blocks {
		for i, m := range idRe.FindAllSubmatchIndex(p.text, -1) {
			prefix := string(p.text[m[2]:m[3]])
			s, e := p.span(m[0], m[1])
			if !own[prefix] && !upstream[prefix] {
				if i == 0 && defines(p, m[0], m[1]) {
					if _, ok := unknown[prefix]; !ok {
						unknownOrder = append(unknownOrder, prefix)
					}
					unknown[prefix] = append(unknown[prefix], use{s, e})
				}
				continue
			}
			id := string(p.text[m[0]:m[1]])
			if i == 0 && defines(p, m[0], m[1]) {
				defs[id] = append(defs[id], use{s, e})
				continue
			}
			refs = append(refs, struct {
				id string
				use
			}{id, use{s, e}})
		}
	}
	for id, uses := range defs {
		for _, u := range uses[1:] {
			emit(DuplicateID, u.start, u.end, fmt.Sprintf("%s is defined more than once.", id),
				fmt.Sprintf("Give this item a new ID, or merge it with the first %s.", id))
		}
	}
	for _, r := range refs {
		// A covered prefix (REQ in an SDD) names upstream items: linked bundles resolve those.
		if upstream[r.id[:strings.IndexByte(r.id, '-')]] {
			if cfg.UpstreamIDs != nil && !cfg.UpstreamIDs[r.id] {
				emit(DanglingRef, r.start, r.end, fmt.Sprintf("%s is referenced, but no linked upstream bundle defines it.", r.id),
					fmt.Sprintf("Fix the reference, or link the bundle that defines %s.", r.id))
			}
			continue
		}
		if _, ok := defs[r.id]; !ok {
			emit(DanglingRef, r.start, r.end, fmt.Sprintf("%s is referenced, but no item defines it.", r.id),
				fmt.Sprintf("Define %s, or fix the reference.", r.id))
		}
	}
	// One finding per prefix: a table of forty FR rows is one fact, not forty.
	for _, prefix := range unknownOrder {
		uses := unknown[prefix]
		emit(UnknownPrefix, uses[0].start, uses[0].end,
			fmt.Sprintf("%s-… looks like a trace ID in %d place%s, but this doc type reads only %s, so Speccy does not trace it.",
				prefix, len(uses), plural(len(uses)), strings.Join(cfg.Prefixes, ", ")),
			fmt.Sprintf("Use one of %s, or add %s to trace.prefixes in the profile.", strings.Join(cfg.Prefixes, ", "), prefix))
	}
	if len(defs) == 0 {
		noIDs(d, cfg, emit)
	}
}

// noIDs reports the first section whose heading names requirements, decisions or
// non-functional requirements when the doc defines no trace ID. It is a hint: a doc without
// IDs is not wrong, but no other doc can reference its items.
func noIDs(d *doc, cfg Config, emit emitter) {
	for _, sec := range d.sections.Sections {
		if sec.Level == 0 || SectionPrefix(sec.Path, cfg.Prefixes) == "" {
			continue
		}
		if len(strings.TrimSpace(string(sec.Own(d.src)))) == 0 {
			continue
		}
		start, end := sec.Start-d.offset, sec.BodyStart-d.offset
		emit(NoIDs, start, end,
			fmt.Sprintf("The %q section has no trace IDs, so a linked doc cannot say which of its items it covers.", sec.Title),
			fmt.Sprintf("Open Traceability and add the IDs Speccy suggests, or start each item with an ID such as %s-001:.", SectionPrefix(sec.Path, cfg.Prefixes)))
		return
	}
}

// sectionWords maps a word in a heading to the prefix of the items under it. The first match
// wins, so "Non-functional requirements" gives NFR.
var sectionWords = []struct {
	prefix string
	words  []string
}{
	{"NFR", []string{"non-functional", "nonfunctional", "quality"}},
	{"REQ", []string{"requirement"}},
	{"DEC", []string{"decision"}},
}

// SectionPrefix returns the trace ID prefix of the items under the heading path, or "" when the
// headings name none of prefixes.
func SectionPrefix(path []string, prefixes []string) string {
	heading := strings.ToLower(strings.Join(path, " "))
	for _, h := range sectionWords {
		if !slices.Contains(prefixes, h.prefix) {
			continue
		}
		for _, w := range h.words {
			if strings.Contains(heading, w) {
				return h.prefix
			}
		}
	}
	return ""
}

// defines reports whether the ID at p.text[s:e] defines it (REQ-051): the ID starts the block.
// A heading and the first cell of a table body row define an ID as their first word. A list
// item or a paragraph defines it only with a colon, a dash or an em dash after it, so a
// sentence that starts with an ID stays a reference: "REQ-012: …", "**REQ-012:** …" or
// "REQ-012 — …".
func defines(p prose, s, e int) bool {
	if strings.TrimSpace(string(p.text[:s])) != "" {
		return false
	}
	switch p.kind {
	case kindHeading:
		return true
	case kindCell:
		parent := p.node.Parent()
		return p.node.PreviousSibling() == nil && parent != nil && parent.Kind() == extast.KindTableRow
	}
	rest := strings.TrimLeft(string(p.text[e:]), " ")
	for _, sep := range []string{":", "—", "–", "- "} {
		if strings.HasPrefix(rest, sep) {
			return true
		}
	}
	return false
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// phrases checks the slop list and the weasel list (REQ-063).
func phrases(d *doc, cfg Config, emit emitter) {
	slop := compileList(append(append([]string{}, slopPhrases...), cfg.SlopExtra...))
	weasel := compileList(weaselWords)
	for _, p := range d.blocks {
		for _, m := range slop.FindAllIndex(p.text, -1) {
			s, e := p.span(m[0], m[1])
			w := string(p.text[m[0]:m[1]])
			emit(SlopPhrase, s, e, fmt.Sprintf("%q is a filler phrase that adds no information.", w),
				"Say the specific thing, or delete the phrase.")
		}
		if p.kind == kindHeading {
			continue
		}
		for _, m := range weasel.FindAllIndex(p.text, -1) {
			s, e := p.span(m[0], m[1])
			w := string(p.text[m[0]:m[1]])
			emit(Weasel, s, e, fmt.Sprintf("%q is vague. A reader cannot build from it.", w),
				"Replace it with a number, a list, or a condition.")
		}
	}
}

// compileList returns one case-insensitive regexp for whole words and phrases.
func compileList(words []string) *regexp.Regexp {
	parts := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		q := regexp.QuoteMeta(w)
		q = strings.ReplaceAll(q, "'", "['’]")
		q = strings.ReplaceAll(q, `\ `, `\s+`)
		start, end := `\b`, `\b`
		if !isWordChar(rune(w[0])) {
			start = ""
		}
		if !isWordChar(rune(w[len(w)-1])) {
			end = ""
		}
		parts = append(parts, start+q+end)
	}
	if len(parts) == 0 {
		return regexp.MustCompile(`$^`)
	}
	return regexp.MustCompile(`(?i)(?:` + strings.Join(parts, "|") + `)`)
}

func isWordChar(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// sentences checks sentence length and passive voice. Headings and table cells are skipped.
var sentenceEnd = regexp.MustCompile(`[.!?]+["')\]]*(\s+|$)`)
var wordRe = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}'’\-]*`)
var passiveRe = regexp.MustCompile(`(?i)\b(am|is|are|was|were|be|been|being)\s+(\w+ed|` + irregularParticiples + `)\b`)

func sentences(d *doc, cfg Config, emit emitter) {
	for _, p := range d.blocks {
		if p.kind != kindParagraph {
			continue
		}
		start := 0
		for start < len(p.text) {
			end := len(p.text)
			if loc := nextSentenceEnd(p.text, start); loc >= 0 {
				end = loc
			}
			sent := p.text[start:end]
			trimmed := bytes.TrimSpace(sent)
			if len(trimmed) > 0 {
				lead := start + bytes.Index(sent, trimmed)
				tail := lead + len(trimmed)
				if n := len(wordRe.FindAll(trimmed, -1)); cfg.MaxSentenceWords > 0 && n > cfg.MaxSentenceWords {
					s, e := p.span(lead, tail)
					emit(SentenceLength, s, e,
						fmt.Sprintf("This sentence has %d words. The limit is %d.", n, cfg.MaxSentenceWords),
						"Split it into shorter sentences, one idea each.")
				}
				for _, m := range passiveRe.FindAllIndex(trimmed, -1) {
					s, e := p.span(lead+m[0], lead+m[1])
					emit(PassiveVoice, s, e, "This sentence uses the passive voice, so it hides who acts.",
						"Name the actor: \"the service retries\", not \"the request is retried\".")
				}
			}
			start = end
		}
	}
}

// nextSentenceEnd returns the end of the sentence that starts at from, or -1. A full stop
// inside a number or an abbreviation such as "e.g." does not end a sentence.
func nextSentenceEnd(text []byte, from int) int {
	for _, m := range sentenceEnd.FindAllIndex(text[from:], -1) {
		end := from + m[1]
		dot := from + m[0]
		if end < len(text) && dot > 0 && isAbbrev(text[:dot]) {
			continue
		}
		if end < len(text) && !startsSentence(text[end:]) {
			continue
		}
		return end
	}
	return -1
}

var abbrevs = []string{"e.g", "i.e", "etc", "vs", "cf", "approx", "no", "fig", "mr", "ms", "dr"}

func isAbbrev(before []byte) bool {
	low := strings.ToLower(string(before))
	for _, a := range abbrevs {
		if strings.HasSuffix(low, a) {
			i := len(low) - len(a)
			if i == 0 || !isWordChar(rune(low[i-1])) {
				return true
			}
		}
	}
	return false
}

func startsSentence(rest []byte) bool {
	for _, r := range string(rest) {
		return unicode.IsUpper(r) || unicode.IsDigit(r) || strings.ContainsRune(`"'“‘(*[`, r)
	}
	return false
}

// acronyms finds acronyms used before they are defined. "Full name (ABC)" and "ABC (Full
// name)" define ABC.
var acronymRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,5})(s)?\b`)

func acronyms(d *doc, cfg Config, emit emitter) {
	defined := map[string]bool{}
	reported := map[string]bool{}
	for _, p := range d.blocks {
		for _, m := range acronymRe.FindAllSubmatchIndex(p.text, -1) {
			a := string(p.text[m[2]:m[3]])
			if commonAcronyms[a] || defined[a] || reported[a] || !hasLetter(a, 2) {
				continue
			}
			// An ID such as REQ-012 or a ticket such as PAY-231 is not an acronym.
			if m[1]+1 < len(p.text) && p.text[m[1]] == '-' && p.text[m[1]+1] >= '0' && p.text[m[1]+1] <= '9' {
				continue
			}
			if definesAcronym(p.text, m[0], m[1]) {
				defined[a] = true
				continue
			}
			if definedLater(d.blocks, a) {
				reported[a] = true
				s, e := p.span(m[0], m[1])
				emit(UndefinedAcronym, s, e, fmt.Sprintf("%s is used before it is defined.", a),
					fmt.Sprintf("Write the full name at the first use, then %s in brackets.", a))
				continue
			}
			reported[a] = true
			s, e := p.span(m[0], m[1])
			emit(UndefinedAcronym, s, e, fmt.Sprintf("%s is not defined in the doc.", a),
				fmt.Sprintf("Write the full name at the first use, then %s in brackets.", a))
		}
	}
}

func hasLetter(s string, n int) bool {
	c := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			c++
		}
	}
	return c >= n
}

// definesAcronym reports whether the acronym at text[s:e] is defined in place: "(ABC)" after
// words, or "ABC (" followed by words.
func definesAcronym(text []byte, s, e int) bool {
	if s > 0 && text[s-1] == '(' && e < len(text) && (text[e] == ')' || (e+1 < len(text) && text[e+1] == ')')) {
		return true
	}
	rest := strings.TrimLeft(string(text[e:]), " ")
	return strings.HasPrefix(rest, "(") && len(rest) > 2 && unicode.IsLetter(rune(rest[1]))
}

func definedLater(blocks []prose, a string) bool {
	for _, p := range blocks {
		for _, m := range acronymRe.FindAllSubmatchIndex(p.text, -1) {
			if string(p.text[m[2]:m[3]]) == a && definesAcronym(p.text, m[0], m[1]) {
				return true
			}
		}
	}
	return false
}

// rfc2119 finds must, should, and may in lower case inside a requirement item: a list item
// that defines a REQ or NFR ID, or an ID with a covered prefix.
var rfcLowerRe = regexp.MustCompile(`\b(must|should|may)\b`)

func rfc2119(d *doc, cfg Config, emit emitter) {
	reqPrefixes := map[string]bool{"REQ": true, "NFR": true}
	for _, p := range cfg.UpstreamPrefixes {
		reqPrefixes[p] = true
	}
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Kind() != ast.KindListItem {
			return ast.WalkContinue, nil
		}
		var item []prose
		for _, p := range d.blocks {
			if isInside(p.node, n) {
				item = append(item, p)
			}
		}
		if len(item) == 0 {
			return ast.WalkContinue, nil
		}
		first := item[0]
		m := idRe.FindSubmatchIndex(first.text)
		if m == nil || !reqPrefixes[string(first.text[m[2]:m[3]])] || !defines(first, m[0], m[1]) {
			return ast.WalkContinue, nil
		}
		for _, p := range item {
			for _, w := range rfcLowerRe.FindAllIndex(p.text, -1) {
				word := string(p.text[w[0]:w[1]])
				s, e := p.span(w[0], w[1])
				emit(RFC2119Case, s, e,
					fmt.Sprintf("%q in a requirement reads as RFC 2119, but it is in lower case.", word),
					fmt.Sprintf("Write %q if it is a requirement level, or reword it.", strings.ToUpper(word)))
			}
		}
		return ast.WalkSkipChildren, nil
	})
}

func isInside(n, ancestor ast.Node) bool {
	for p := n; p != nil; p = p.Parent() {
		if p == ancestor {
			return true
		}
	}
	return false
}

// proseLimits checks the word limits for the doc and for each section's own text.
func proseLimits(d *doc, cfg Config, emit emitter) {
	count := func(from, to int) int {
		n := 0
		for _, p := range d.blocks {
			if len(p.pos) == 0 || p.kind == kindHeading {
				continue
			}
			if p.pos[0] >= from && p.pos[0] < to {
				n += len(wordRe.FindAll(p.text, -1))
			}
		}
		return n
	}
	if total := count(0, len(d.body)); cfg.MaxWords > 0 && total > cfg.MaxWords {
		at := 0
		emit(ProseLimit, at, lineEnd(d.body, at),
			fmt.Sprintf("The doc has %d words. The limit is %d.", total, cfg.MaxWords),
			"Move detail to assets, link to other docs instead of repeating them, or split the doc.")
	}
	if cfg.MaxSectionWords <= 0 {
		return
	}
	for _, s := range d.sections.Sections {
		from, to := s.BodyStart-d.offset, s.OwnEnd-d.offset
		if n := count(from, to); n > cfg.MaxSectionWords {
			at := s.Start - d.offset
			emit(ProseLimit, at, lineEnd(d.body, at),
				fmt.Sprintf("This section has %d words. The limit is %d.", n, cfg.MaxSectionWords),
				"Split the section, or move detail to an asset.")
		}
	}
}

// assetNudges finds long code blocks and long tables that belong in an asset.
func assetNudges(d *doc, cfg Config, emit emitter) {
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			lines := n.Lines().Len()
			if cfg.MaxCodeBlockLines > 0 && lines > cfg.MaxCodeBlockLines {
				seg := n.Lines().At(0)
				emit(AssetNudge, seg.Start, seg.Stop,
					fmt.Sprintf("This code block has %d lines. The limit is %d.", lines, cfg.MaxCodeBlockLines),
					"Move it to a file in assets/ and link to it.")
			}
			return ast.WalkSkipChildren, nil
		case *extast.Table:
			rows := 0
			for c := t.FirstChild(); c != nil; c = c.NextSibling() {
				if c.Kind() == extast.KindTableRow {
					rows++
				}
			}
			if cfg.MaxTableRows > 0 && rows > cfg.MaxTableRows {
				s, e := nodeSpan(t.FirstChild(), d.body)
				emit(AssetNudge, s, e,
					fmt.Sprintf("This table has %d rows. The limit is %d.", rows, cfg.MaxTableRows),
					"Move it to a file in assets/ (for example CSV or YAML) and link to it.")
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}

// requirementGrammar parses each trace ID definition into a trigger and a response, in the
// EARS shapes (Appendix B). A definition that does not parse gets a finding, and the review
// judges it by its raw text instead.
func requirementGrammar(d *doc, cfg Config, emit emitter) {
	for _, def := range Definitions(d.src, cfg.Prefixes) {
		if _, ok := ears.Parse(def.Text); ok {
			continue
		}
		emit(RequirementGrammar, def.Start-d.offset, def.End-d.offset,
			fmt.Sprintf("%s does not state a trigger and a response, so a check cannot verify it.", def.ID), ears.Fix)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
