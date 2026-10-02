package review

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// PromptFix is the prompt of a suggested fix (REQ-025).
const PromptFix = "fix-v3"

var fixSchema = []byte(`{"type":"object","additionalProperties":false,"required":["new","explanation"],"properties":{"new":{"type":"string"},"explanation":{"type":"string"}}}`)

// Where a patch that adds text puts it.
const (
	insertStart  = "start"  // at the start of the file
	insertEnd    = "end"    // at the end of the file
	insertBefore = "before" // in front of the heading line in Before
)

// patch replaces one range of one file with New, or adds New at one place. Speccy picks the
// range, and the model writes only New, so a patch never fails on a miscopied quote. The patch
// is stored on the finding until the author accepts it.
type patch struct {
	File string `json:"file"`
	// Old is the text of the range at Version. It is empty for a patch that adds text.
	Old   string `json:"old"`
	New   string `json:"new"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	// Insert is set for a patch that adds text, and Before is the heading line it goes before.
	Insert      string    `json:"insert,omitempty"`
	Before      string    `json:"before,omitempty"`
	Explanation string    `json:"explanation"`
	Version     uuid.UUID `json:"version_id"`
}

// edit is one replacement in a file: src[start:end] becomes text.
type edit struct {
	start, end int
	text       string
}

// edit finds the patch in src, which may be a later version than the one it was written for.
// A range holds while its text is where it was, or occurs exactly once. ok is false when the
// text changed.
func (pt patch) edit(src []byte) (edit, bool) {
	switch pt.Insert {
	case insertStart:
		return edit{0, 0, pt.New + "\n\n"}, true
	case insertEnd:
		n := len(bytes.TrimRight(src, " \t\r\n"))
		if n == 0 {
			return edit{0, len(src), pt.New + "\n"}, true
		}
		return edit{n, len(src), "\n\n" + pt.New + "\n"}, true
	case insertBefore:
		// The heading line must start exactly one line of the file.
		line := []byte(pt.Before + "\n")
		inner := append([]byte("\n"), line...)
		first := bytes.HasPrefix(src, line)
		n := bytes.Count(src, inner)
		if first {
			n++
		}
		if n != 1 {
			return edit{}, false
		}
		i := 0
		if !first {
			i = bytes.Index(src, inner) + 1
		}
		return edit{i, i, pt.New + "\n\n"}, true
	}
	if pt.Old == "" {
		return edit{}, false
	}
	if pt.End <= len(src) && pt.Start <= pt.End && string(src[pt.Start:pt.End]) == pt.Old {
		return edit{pt.Start, pt.End, pt.New}, true
	}
	if bytes.Count(src, []byte(pt.Old)) != 1 {
		return edit{}, false
	}
	i := bytes.Index(src, []byte(pt.Old))
	return edit{i, i + len(pt.Old), pt.New}, true
}

// applyEdits returns src with every edit applied, and the range each edit's text has in the
// result. The edits must not overlap.
func applyEdits(src []byte, edits []edit) ([]byte, []edit, bool) {
	order := make([]int, len(edits))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return edits[order[i]].start < edits[order[j]].start })
	var out bytes.Buffer
	placed := make([]edit, len(edits))
	at := 0
	for _, i := range order {
		e := edits[i]
		if e.start < at || e.end < e.start || e.end > len(src) {
			return nil, nil, false
		}
		out.Write(src[at:e.start])
		placed[i] = edit{out.Len(), out.Len() + len(e.text), e.text}
		out.WriteString(e.text)
		at = e.end
	}
	out.Write(src[at:])
	return out.Bytes(), placed, true
}

// suggestion is the finding's suggestion column: the fix text from the review, and the patch.
type suggestion struct {
	Fix string `json:"fix,omitempty"`
	// Question is the question the reviewer wrote for the shortfall of a rubric check.
	Question string `json:"question,omitempty"`
	Patch    *patch `json:"patch,omitempty"`
}

func (a *API) finding(ctx context.Context, runID, id uuid.UUID) (pgdb.ReviewRun, pgdb.SpecDoc, pgdb.Finding, error) {
	run, b, err := a.run(ctx, runID)
	if err != nil {
		return run, b, pgdb.Finding{}, err
	}
	f, err := a.DB.Queries().GetFinding(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && f.RunID != run.ID) {
		return run, b, f, kernel.NotFound("finding_not_found", "The run has no finding with this ID.")
	}
	return run, b, f, err
}

// candidateEvidence is the evidence of a broken link that one bundle file can take.
type candidateEvidence struct {
	Candidate string `json:"candidate"`
}

// fixKind is the fix kind of a finding. The Check catalog names the reword checks; a broken
// link is a reword finding only when one bundle file has the linked name. Every other finding
// needs a fact from the author.
func fixKind(slug string, evidence []byte) api.FixKind {
	if !profile.Reword(slug) {
		return api.Answer
	}
	if slug == lint.BrokenLink {
		var ev candidateEvidence
		_ = json.Unmarshal(evidence, &ev)
		if ev.Candidate == "" {
			return api.Answer
		}
	}
	return api.Reword
}

// kind is the fix kind of a finding that is not stored yet.
func (p pending) kind() api.FixKind {
	ev, _ := json.Marshal(p.evidence)
	return fixKind(p.slug, ev)
}

// fixTarget is the text a fix replaces, or the place where it adds text.
type fixTarget struct {
	start, end     int
	insert, before string
	// what names the target in the prompt, and heading is the heading line that new text
	// must start with.
	what, heading string
}

// fixTarget picks the text that a fix of f replaces: the frontmatter for a link check, the
// paragraph of the finding, or its section when the finding is about the section. A finding
// about content that the doc does not have gets a place for new text instead: where the
// template puts a missing heading, or the end of the doc.
func (s *Service) fixTarget(b pgdb.SpecDoc, f pgdb.Finding, an anchor.Anchor, src []byte) fixTarget {
	doc := section.Parse(src)
	trimmed := func(from, to int) int { return from + len(bytes.TrimRight(src[from:to], " \t\r\n")) }
	if strings.HasPrefix(f.CheckSlug, "links.") || f.CheckSlug == FrontmatterReadableSlug {
		end := doc.BodyStart
		if end == 0 {
			// Speccy reads no block: the fix is in the text before the first heading.
			end = len(src)
			for _, sec := range doc.Sections {
				if sec.Level > 0 {
					end = sec.Start
					break
				}
			}
		}
		if end = trimmed(0, end); end == 0 {
			return fixTarget{insert: insertStart, what: "the frontmatter block, which the file does not have"}
		}
		return fixTarget{end: end, what: "the frontmatter block"}
	}
	if f.CheckSlug == lint.RequiredHeadings {
		if p, ok := s.Profiles()[b.ProfileKey]; ok {
			required := profile.RequiredHeadings(p.TemplateText)
			for i, h := range required {
				if !strings.Contains(f.Message, strconv.Quote(h.Title)) {
					continue
				}
				t := fixTarget{insert: insertEnd, heading: strings.Repeat("#", h.Level) + " " + h.Title}
				t.what = "a new section, at the end of the doc"
				// The new section goes in front of the first section that the template puts after it.
				for _, later := range required[i+1:] {
					for _, sec := range doc.Sections {
						if sec.Level > 0 && lint.NormTitle(sec.Title) == lint.NormTitle(later.Title) {
							t.insert, t.before = insertBefore, headingLine(src, sec)
							t.what = "a new section, in front of the section " + sec.Title
							return t
						}
					}
				}
				return t
			}
		}
		return fixTarget{insert: insertEnd, what: "a new section, at the end of the doc"}
	}
	// A finding of an AI stage on the whole doc says that content is missing.
	if an.Start == 0 && f.Stage != StageLint {
		return fixTarget{insert: insertEnd, what: "new text, at the end of the doc"}
	}
	var sec *section.Section
	for i := range doc.Sections {
		if x := &doc.Sections[i]; x.Start <= an.Start && an.Start < x.End {
			sec = x
		}
	}
	if sec == nil {
		start, end := paragraphAt(src, 0, len(src), an.Start, an.End)
		return fixTarget{start: start, end: end, what: "the paragraph of the finding"}
	}
	if sec.Level > 0 && an.Start == sec.Start {
		return fixTarget{start: sec.Start, end: trimmed(sec.Start, sec.OwnEnd), what: "the section of the finding, with its heading line"}
	}
	start, end := paragraphAt(src, sec.Start, sec.OwnEnd, an.Start, an.End)
	return fixTarget{start: start, end: end, what: "the paragraph of the finding"}
}

// headingLine is the heading line of sec, without its line end.
func headingLine(src []byte, sec section.Section) string {
	line := src[sec.Start:]
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimRight(string(line), "\r")
}

// paragraphAt returns the run of lines around src[start:end] that no blank line breaks, inside
// src[lo:hi]: the paragraph, the list or the table that holds the text.
func paragraphAt(src []byte, lo, hi, start, end int) (int, int) {
	start, end = min(max(start, lo), hi), min(max(end, start), hi)
	blank := func(from, to int) bool { return len(bytes.TrimSpace(src[from:to])) == 0 }
	lineStart := func(i int) int {
		if j := bytes.LastIndexByte(src[lo:i], '\n'); j >= 0 {
			return lo + j + 1
		}
		return lo
	}
	lineEnd := func(i int) int {
		if j := bytes.IndexByte(src[i:hi], '\n'); j >= 0 {
			return i + j
		}
		return hi
	}
	s := lineStart(start)
	for s > lo {
		prev := lineStart(s - 1)
		if blank(prev, s-1) {
			break
		}
		s = prev
	}
	if end > start && src[end-1] == '\n' {
		end--
	}
	e := lineEnd(end)
	for e < hi {
		next := lineEnd(e + 1)
		if blank(e+1, next) {
			break
		}
		e = next
	}
	return s, e
}

// lineOf is the line of offset off in src, from 1.
func lineOf(src []byte, off int) int {
	return 1 + bytes.Count(src[:min(max(off, 0), len(src))], []byte("\n"))
}

// fixError turns a failed model call into an error the author reads: the cause, in words.
func fixError(err error) error {
	if _, ok := kernel.AsError(err); ok {
		return err
	}
	return kernel.Invalid("fix_failed", "The AI could not write a fix: %s.", strings.TrimRight(err.Error(), "."))
}

// placeholders counts the placeholder findings in ps.
func placeholders(ps []pending) int {
	n := 0
	for _, p := range ps {
		if p.slug == lint.Placeholder {
			n++
		}
	}
	return n
}

// SuggestFix asks the writer role for a patch that fixes one finding (REQ-025). Speccy picks
// the text to replace, and the model returns only the new text. An answer finding needs the
// author's answer first: with no fact the model writes a placeholder, and a placeholder is a
// new MUST finding. Speccy stores the patch on the finding and returns it. The doc does not
// change (T-092). A missing upstream link gets no patch: the person picks the doc, and Speccy
// writes the link (#75).
func (a *API) SuggestFix(ctx context.Context, req api.SuggestFixRequestObject) (api.SuggestFixResponseObject, error) {
	_, b, f, err := a.finding(ctx, req.RunId, req.FindingId)
	if err != nil {
		return nil, err
	}
	var an anchor.Anchor
	_ = json.Unmarshal(f.Anchor, &an)
	cur, err := version.LoadCurrent(ctx, a.DB.Queries(), b)
	if err != nil {
		return nil, err
	}
	an, ok := cur.Anchor(an)
	if !ok {
		return nil, kernel.Conflict("finding_detached", "The text of this finding changed, so Speccy cannot find it. Run the review again.")
	}
	if f.CheckSlug == HasUpstreamSlug {
		return a.suggestLink(ctx, b, f, cur)
	}
	src := cur.File(an.File)
	if !utf8.Valid(src) {
		return nil, kernel.Invalid("not_text", "The file %s is not text, so Speccy cannot suggest a fix for it.", an.File)
	}
	answer := ""
	if req.Body != nil && req.Body.Answer != nil {
		answer = strings.TrimSpace(*req.Body.Answer)
	}
	needsAnswer := fixKind(f.CheckSlug, f.Evidence) == api.Answer
	if needsAnswer && answer == "" {
		return nil, kernel.Invalid("answer_needed", "This finding needs a fact from you. Write your answer, and Speccy writes it into the doc.")
	}
	files, err := version.Files(ctx, a.DB.Queries(), cur.ID)
	if err != nil {
		return nil, err
	}
	// A lint finding is one the lint gives on the current text. Speccy lints each patch for it,
	// so it never offers a fix that it knows does not work.
	before, err := a.Service.lintFiles(ctx, b, files)
	if err != nil {
		return nil, err
	}
	_, isLint := sameFinding(f, an, before)
	var sugg suggestion
	_ = json.Unmarshal(f.Suggestion, &sugg)
	t := a.Service.fixTarget(b, f, an, src)
	pt := patch{File: an.File, Start: t.start, End: t.end, Old: string(src[t.start:t.end]), Insert: t.insert, Before: t.before, Version: cur.ID}

	var p strings.Builder
	p.WriteString("Fix the finding below. Speccy picked one part of the file, and replaces that part with the text you write in \"new\". Write only the new text of that part. Do not copy the rest of the file.\n")
	p.WriteString("Keep the author's words, style and markdown where they are not the problem. Keep every fact, number, name, trace ID and link target that the finding does not ask you to change.\n")
	if answer != "" {
		p.WriteString("The author gave the fact that the doc needs. State that fact in the text, in the style of the doc. Add no fact that the author did not give. Do not write a placeholder, a question, or a note to the author.\n")
	} else {
		p.WriteString("Change the words and no fact. Do not add a fact, and do not write a placeholder or a question.\n")
	}
	if t.heading != "" {
		fmt.Fprintf(&p, "The part is %s. Start it with the heading line %q.\n", t.what, t.heading)
	} else if t.insert != "" {
		fmt.Fprintf(&p, "The part is %s. Give it a heading line when it is a section of its own.\n", t.what)
	} else {
		fmt.Fprintf(&p, "The part is %s.\n", t.what)
	}
	p.WriteString("Put one sentence in \"explanation\" that says what the change does.\n\n")
	if strings.HasPrefix(f.CheckSlug, "links.") || f.CheckSlug == FrontmatterReadableSlug {
		p.WriteString(frontmatterFormat)
	}
	fmt.Fprintf(&p, "Finding: %s (%s, level %s)\n", f.Message, f.CheckSlug, f.Level)
	if sugg.Fix != "" {
		fmt.Fprintf(&p, "Suggested direction: %s\n", sugg.Fix)
	}
	if len(an.HeadingPath) > 0 {
		fmt.Fprintf(&p, "Section: %s\n", strings.Join(an.HeadingPath, " > "))
	}
	p.WriteString("\n")
	if answer != "" {
		if q := answerQuestion(f.CheckSlug, f.Message, an.Quote, sugg.Question, f.Evidence); q != "" {
			p.WriteString(data("The question the author answered", q))
		}
		p.WriteString(data("The author's answer", answer))
	}
	if strings.TrimSpace(an.Quote) != "" && an.Quote != pt.Old && len(an.Quote) < len(src) {
		p.WriteString(data("The text the finding points at", an.Quote))
	}
	if pt.Old != "" {
		p.WriteString(data("The part to replace", pt.Old))
	}
	p.WriteString(data("File "+an.File+", for context", string(src)))

	// The new text is as long as the part it replaces, plus the answer. A model that reasons
	// spends tokens on its thinking too; the gateway raises the limit once when it cuts off.
	limit := int64(4000 + (len(pt.Old)+len(answer))/2)
	call := model.Call{Role: model.RoleWriter, PromptVersion: PromptFix, System: systemPrompt, Prompt: p.String(), Schema: fixSchema, MaxTokens: limit}
	var next []byte
	for attempt := 0; ; attempt++ {
		res, err := a.Service.Gateway.Call(ctx, call)
		if err != nil {
			return nil, fixError(err)
		}
		var out struct {
			New         string `json:"new"`
			Explanation string `json:"explanation"`
		}
		if err := json.Unmarshal(res.JSON, &out); err != nil {
			return nil, err
		}
		pt.New, pt.Explanation = strings.Trim(out.New, "\r\n"), out.Explanation
		var why string
		e, _ := pt.edit(src)
		if strings.TrimSpace(pt.New) == "" || pt.New == pt.Old {
			why = "\"new\" must hold the new text of the part, and it must differ from the text it replaces."
		} else {
			next, _, _ = applyEdits(src, []edit{e})
			after, err := a.Service.lintFiles(ctx, b, withFile(files, an.File, next))
			if err != nil {
				return nil, err
			}
			if still, ok := stillFails(f.CheckSlug, before, after); isLint && ok {
				why = "the file still fails the check after the change: " + still.message
			} else if placeholders(after) > placeholders(before) {
				why = "the new text has a placeholder. State the fact, or leave the sentence out."
			}
		}
		if why == "" {
			break
		}
		if attempt == 1 {
			if strings.HasPrefix(why, "the file still fails") {
				return nil, kernel.Invalid("fix_failed", "The AI could not write a change that passes the check: %s Fix the finding by hand.", strings.TrimPrefix(why, "the file still fails the check after the change: "))
			}
			if strings.HasPrefix(why, "the new text has a placeholder") {
				return nil, kernel.Invalid("fix_failed", "The AI wrote a placeholder, because the answer does not hold the fact the finding asks for. Give a fuller answer, or fix the finding by hand.")
			}
			return nil, kernel.Invalid("fix_failed", "The AI gave no new text for this finding. Fix the finding by hand.")
		}
		// One more try, with the reason (DEC-014 allows one retry).
		call.Prompt = p.String() + "\nYour last answer did not work: " + why + "\n"
	}
	sugg.Patch = &pt
	raw, _ := json.Marshal(sugg)
	if err := a.DB.Queries().SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: f.ID, Suggestion: raw}); err != nil {
		return nil, err
	}
	return api.SuggestFix200JSONResponse(pt.suggestion(f.ID, src)), nil
}

// suggestion is the patch as the API shows it, with the lines it changes in src.
func (pt patch) suggestion(finding uuid.UUID, src []byte) api.FixSuggestion {
	e, _ := pt.edit(src)
	out := api.FixSuggestion{FindingId: finding, File: pt.File, Old: pt.Old, New: pt.New, Explanation: pt.Explanation, VersionId: pt.Version,
		Line: lineOf(src, e.start), EndLine: lineOf(src, e.start)}
	if pt.Insert == "" && e.end > e.start {
		out.EndLine = lineOf(src, e.end-1)
	}
	return out
}

// frontmatterFormat tells the model the one form of links that Speccy reads, so a fix for a
// link check does not invent one (#75).
const frontmatterFormat = "The frontmatter is the block between the --- lines at the start of the file, and it may sit inside <!-- -->. Keep its form: YAML stays YAML, and JSON stays JSON. " +
	"links is a list of pairs with the keys kind and target, and no other keys. kind is one of implements, refines, references, supersedes. target is the path of the other doc, relative to this doc, not URL-encoded. In YAML:\n" +
	"links:\n  - kind: implements\n    target: \"PRD - Example.md\"\n" +
	"In JSON: \"links\": [{\"kind\": \"implements\", \"target\": \"PRD - Example.md\"}]\n\n"

// AcceptFix applies the finding's stored patch to the current version (REQ-025). This is the
// only path by which a suggested fix changes a doc (T-092). For a lint finding it says at once
// whether the result still fails the check.
func (a *API) AcceptFix(ctx context.Context, req api.AcceptFixRequestObject) (api.AcceptFixResponseObject, error) {
	_, b, f, err := a.finding(ctx, req.RunId, req.FindingId)
	if err != nil {
		return nil, err
	}
	if f.CheckSlug == HasUpstreamSlug {
		if req.Body == nil || req.Body.LinkTo == nil {
			return nil, kernel.Invalid("no_link", "Pick the doc that the link names.")
		}
		return a.acceptLink(ctx, b, f, *req.Body.LinkTo)
	}
	if a.Change == nil {
		return nil, kernel.Invalid("not_supported", "This server cannot change bundle files.")
	}
	var sugg suggestion
	_ = json.Unmarshal(f.Suggestion, &sugg)
	if sugg.Patch == nil {
		return nil, kernel.Invalid("no_suggestion", "This finding has no suggested fix. Ask for one first.")
	}
	pt := sugg.Patch
	cur, err := version.LoadCurrent(ctx, a.DB.Queries(), b)
	if err != nil {
		return nil, err
	}
	src := cur.File(pt.File)
	e, ok := pt.edit(src)
	if src == nil || !ok {
		return nil, kernel.Conflict("fix_stale", "The text changed after the fix was suggested. Ask for a new fix.")
	}
	files, err := version.Files(ctx, a.DB.Queries(), cur.ID)
	if err != nil {
		return nil, err
	}
	an, was, err := a.lintBefore(ctx, b, f, cur, files)
	if err != nil {
		return nil, err
	}
	next, _, _ := applyEdits(src, []edit{e})
	by := kernel.ActorFrom(ctx).UserID
	v, changed, err := a.Change(ctx, b.ID, cur.ID, source.Op{Kind: source.OpWrite, Path: pt.File, Content: next}, by, "Applied a suggested fix for "+f.CheckSlug)
	if err != nil {
		return nil, err
	}
	sugg.Patch = nil
	raw, _ := json.Marshal(sugg)
	if err := a.DB.Queries().SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: f.ID, Suggestion: raw}); err != nil {
		return nil, err
	}
	ver := version.ToAPI(v)
	out := api.AcceptFix200JSONResponse{Version: &ver, Changed: changed}
	out.Result, out.Message, err = a.fixResult(ctx, b, f, an, was, withFile(files, pt.File, next))
	return out, err
}

// fixResult says whether the lint of after still gives f. was is the lint before the fix. A
// finding that was does not give is an AI finding, and only a new review checks it.
func (a *API) fixResult(ctx context.Context, b pgdb.SpecDoc, f pgdb.Finding, an anchor.Anchor, was []pending, after []source.File) (api.AcceptedFixResult, *string, error) {
	if _, ok := sameFinding(f, an, was); !ok {
		return api.ReviewAgain, nil, nil
	}
	now, err := a.Service.lintFiles(ctx, b, after)
	if err != nil {
		return "", nil, err
	}
	if still, ok := stillFails(f.CheckSlug, was, now); ok {
		return api.StillFails, &still.message, nil
	}
	return api.Fixed, nil, nil
}

// lintBefore re-anchors f on the current version and lints files, before a fix changes them.
func (a *API) lintBefore(ctx context.Context, b pgdb.SpecDoc, f pgdb.Finding, cur *version.Current, files []source.File) (anchor.Anchor, []pending, error) {
	var an anchor.Anchor
	_ = json.Unmarshal(f.Anchor, &an)
	an, _ = cur.Anchor(an)
	was, err := a.Service.lintFiles(ctx, b, files)
	return an, was, err
}

// lintFiles runs the lint stage on files as b's version, without storing a run.
func (s *Service) lintFiles(ctx context.Context, b pgdb.SpecDoc, files []source.File) ([]pending, error) {
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		return nil, kernel.Invalid("no_profile", "%s", s.noProfile(b.ProfileKey))
	}
	in, err := s.loadFiles(ctx, b, uuid.Nil, files, p, nil)
	if err != nil {
		return nil, err
	}
	return lintStage(in).findings, nil
}

// sameFinding returns the finding in ps that stands for f: the same check at the same place, or
// with the same message. A check on the whole doc has one place, the start of the file.
func sameFinding(f pgdb.Finding, an anchor.Anchor, ps []pending) (pending, bool) {
	whole := func(a anchor.Anchor) bool { return a.Start == 0 }
	for _, p := range ps {
		if p.slug != f.CheckSlug {
			continue
		}
		if p.message == f.Message || (whole(an) && whole(p.anchor)) ||
			(an.Quote != "" && p.anchor.Quote == an.Quote && slices.Equal(p.anchor.HeadingPath, an.HeadingPath)) {
			return p, true
		}
	}
	return pending{}, false
}

// stillFails reports whether a fix left the check as it was: lint gives as many findings of
// slug after the fix as before it. A check such as lint.passive-voice gives many findings with
// one message, so the count decides, not the message. It returns one finding that is left.
func stillFails(slug string, was, now []pending) (pending, bool) {
	count := func(ps []pending) (n int, last pending) {
		for _, p := range ps {
			if p.slug == slug {
				n, last = n+1, p
			}
		}
		return n, last
	}
	before, _ := count(was)
	after, left := count(now)
	return left, after > 0 && after >= before
}

// withFile returns files with the content of one file replaced.
func withFile(files []source.File, path string, content []byte) []source.File {
	out := make([]source.File, len(files))
	copy(out, files)
	for i := range out {
		if out[i].Path == path {
			out[i].Content = content
		}
	}
	return out
}
