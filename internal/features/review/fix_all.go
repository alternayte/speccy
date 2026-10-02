package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// PromptFixAll is the prompt of the bulk fix of a reword check.
const PromptFixAll = "fix-all-v1"

var fixAllSchema = []byte(`{"type":"object","additionalProperties":false,"required":["new"],"properties":{"new":{"type":"string"}}}`)

// sectionFindings is the reword findings of one check in one section, with the text that one
// model call rewrites.
type sectionFindings struct {
	path       []string
	start, end int
	rows       []pgdb.Finding
	anchors    []anchor.Anchor
}

// SuggestFixes rewrites every reword finding of one check, with one model call for each
// section that has such findings: one call for each finding would send the file each time.
// Speccy lints each rewritten section and drops one that still fails the check, so it never
// offers a rewrite that it knows does not work. Each rewrite is stored as a patch on the first
// finding of its section. The doc does not change (T-092).
func (a *API) SuggestFixes(ctx context.Context, req api.SuggestFixesRequestObject) (api.SuggestFixesResponseObject, error) {
	run, b, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	slug := req.Body.CheckSlug
	if !profile.Reword(slug) {
		return nil, kernel.Invalid("not_reword", "%s is not a reword check: a finding of it needs a fact from the author. Fix its findings one by one.", slug)
	}
	if _, err := a.Service.Gateway.Assigned(ctx, model.RoleWriter); err != nil {
		return nil, err
	}
	q := a.DB.Queries()
	rows, err := q.ListFindings(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	cur, err := version.LoadCurrent(ctx, q, b)
	if err != nil {
		return nil, err
	}
	src := cur.File(b.DocPath)
	if !utf8.Valid(src) {
		return nil, kernel.Invalid("not_text", "The file %s is not text, so Speccy cannot rewrite it.", b.DocPath)
	}
	doc := section.Parse(src)
	files, err := version.Files(ctx, q, cur.ID)
	if err != nil {
		return nil, err
	}
	before, err := a.Service.lintFiles(ctx, b, files)
	if err != nil {
		return nil, err
	}

	// One group for each section. A finding in the heading line puts the heading in the text to
	// rewrite; otherwise the heading stays out, so the model cannot rename a section.
	groups := map[int]*sectionFindings{}
	for _, f := range rows {
		if f.CheckSlug != slug || f.Waived || fixKind(f.CheckSlug, f.Evidence) != api.Reword {
			continue
		}
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		an, ok := cur.Anchor(an)
		if !ok || an.File != b.DocPath {
			continue
		}
		// The current lint must still give the finding: a fixed one needs no rewrite.
		if _, ok := sameFinding(f, an, before); !ok {
			continue
		}
		i := sectionAt(doc, an.Start)
		if i < 0 {
			continue
		}
		sec := doc.Sections[i]
		g := groups[i]
		if g == nil {
			g = &sectionFindings{path: sec.Path, start: sec.BodyStart, end: sec.Start + len(bytes.TrimRight(src[sec.Start:sec.OwnEnd], " \t\r\n"))}
			// The blank lines under the heading stay out of the rewrite, so they stay in the doc.
			for g.start < g.end {
				nl := bytes.IndexByte(src[g.start:g.end], '\n')
				if nl < 0 || len(bytes.TrimSpace(src[g.start:g.start+nl])) > 0 {
					break
				}
				g.start += nl + 1
			}
			groups[i] = g
		}
		if an.Start < sec.BodyStart {
			g.start = sec.Start
		}
		g.rows, g.anchors = append(g.rows, f), append(g.anchors, an)
	}
	if len(groups) == 0 {
		return nil, kernel.Invalid("nothing_to_fix", "The doc has no reword finding of %s now.", slug)
	}
	order := make([]int, 0, len(groups))
	for i := range groups {
		order = append(order, i)
	}
	sort.Ints(order)

	out := api.SectionFixes{CheckSlug: slug, VersionId: cur.ID, Sections: []api.SectionFix{}, Dropped: []api.DroppedFix{}, Calls: len(order)}
	results := make([]sectionResult, len(order))
	sem := make(chan struct{}, a.Service.parallel(ctx))
	var wg sync.WaitGroup
	for n, i := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[n] = a.rewriteSection(ctx, b, slug, files, src, before, groups[i])
		}()
	}
	wg.Wait()
	var firstErr error
	for n, i := range order {
		g, r := groups[i], results[n]
		path := g.path
		if path == nil {
			path = []string{}
		}
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		if r.dropped != "" {
			out.Dropped = append(out.Dropped, api.DroppedFix{HeadingPath: path, Reason: r.dropped})
			continue
		}
		pt := patch{File: b.DocPath, Start: g.start, End: g.end, Old: string(src[g.start:g.end]), New: r.text, Version: cur.ID,
			Explanation: fmt.Sprintf("Rewrites %d finding%s of %s in this section.", len(g.rows), plural(len(g.rows)), slug)}
		first := g.rows[0]
		var sugg suggestion
		_ = json.Unmarshal(first.Suggestion, &sugg)
		sugg.Patch = &pt
		raw, _ := json.Marshal(sugg)
		if err := q.SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: first.ID, Suggestion: raw}); err != nil {
			return nil, err
		}
		out.Sections = append(out.Sections, api.SectionFix{FindingId: first.ID, File: pt.File, HeadingPath: path, Old: pt.Old, New: pt.New,
			Line: lineOf(src, g.start), EndLine: lineOf(src, max(g.end-1, g.start)), Findings: len(g.rows)})
	}
	// When every call failed, the cause is the backend, not the sections.
	if len(out.Sections) == 0 && firstErr != nil {
		return nil, fixError(firstErr)
	}
	return api.SuggestFixes200JSONResponse(out), nil
}

// sectionAt returns the index of the deepest section of doc that holds offset, or -1.
func sectionAt(doc section.Doc, off int) int {
	at := -1
	for i, s := range doc.Sections {
		if s.Start <= off && off < s.End {
			at = i
		}
	}
	return at
}

// sectionResult is one rewritten section, or why Speccy dropped it.
type sectionResult struct {
	text    string
	dropped string
	err     error
}

// rewriteSection asks the writer for the section rewritten, then lints the result. The section
// passes when lint gives no reword finding of the check in it, and no MUST finding that the
// doc did not have.
func (a *API) rewriteSection(ctx context.Context, b pgdb.SpecDoc, slug string, files []source.File, src []byte, before []pending, g *sectionFindings) sectionResult {
	old := string(src[g.start:g.end])
	var p strings.Builder
	p.WriteString("Rewrite the section below so that it has none of the findings listed. Speccy replaces the section with the text you write in \"new\", so write the whole section.\n")
	p.WriteString("Change only the sentences that the findings name. Change the words and no fact: keep every fact, number, name, trace ID and link target. Keep the markdown as it is: heading lines, lists, tables and code blocks stay. Do not add a sentence, a placeholder, a question or a note.\n\n")
	fmt.Fprintf(&p, "Check: %s\nFindings:\n", slug)
	for i, f := range g.rows {
		var sugg suggestion
		_ = json.Unmarshal(f.Suggestion, &sugg)
		fmt.Fprintf(&p, "- %s", f.Message)
		if sugg.Fix != "" {
			fmt.Fprintf(&p, " %s", sugg.Fix)
		}
		if quote := strings.TrimSpace(g.anchors[i].Quote); quote != "" {
			fmt.Fprintf(&p, " Text: %q", quote)
		}
		p.WriteString("\n")
	}
	p.WriteString("\n")
	p.WriteString(data("The section to rewrite", old))
	res, err := a.Service.Gateway.Call(ctx, model.Call{Role: model.RoleWriter, PromptVersion: PromptFixAll, System: systemPrompt,
		Prompt: p.String(), Schema: fixAllSchema, MaxTokens: int64(4000 + len(old)/2)})
	if err != nil {
		reason := err.Error()
		if ke, ok := kernel.AsError(err); ok {
			reason = ke.Detail
		}
		return sectionResult{dropped: "The AI gave no rewrite: " + reason, err: err}
	}
	var out struct {
		New string `json:"new"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		return sectionResult{dropped: "The AI gave no rewrite.", err: err}
	}
	text := strings.Trim(out.New, "\r\n")
	if strings.TrimSpace(text) == "" || text == old {
		return sectionResult{dropped: "The AI left the section as it is."}
	}
	next, placed, _ := applyEdits(src, []edit{{g.start, g.end, text}})
	after, err := a.Service.lintFiles(ctx, b, withFile(files, b.DocPath, next))
	if err != nil {
		return sectionResult{dropped: "Speccy could not lint the rewrite.", err: err}
	}
	if still := rewordIn(after, slug, placed[0].start, placed[0].end); len(still) > 0 {
		return sectionResult{dropped: "The rewrite still fails the check: " + still[0].message}
	}
	if extra, ok := newMust(before, after); ok {
		return sectionResult{dropped: fmt.Sprintf("The rewrite adds a MUST finding of %s: %s", extra.slug, extra.message)}
	}
	return sectionResult{text: text}
}

// rewordIn returns the reword findings of slug that lint gives in the range of a file.
func rewordIn(ps []pending, slug string, start, end int) []pending {
	var out []pending
	for _, p := range ps {
		if p.slug == slug && p.kind() == api.Reword && p.anchor.Start >= start && p.anchor.Start < end {
			out = append(out, p)
		}
	}
	return out
}

// newMust returns a MUST lint finding of after whose check has more MUST findings than before.
func newMust(before, after []pending) (pending, bool) {
	had := map[string]int{}
	for _, p := range before {
		if p.level == kernel.Must {
			had[p.slug]++
		}
	}
	for _, p := range after {
		if p.level != kernel.Must {
			continue
		}
		if had[p.slug]--; had[p.slug] < 0 {
			return p, true
		}
	}
	return pending{}, false
}

// AcceptFixes applies the section rewrites the author accepted to the current version, as one
// version. It says how many reword findings lint still gives in those sections.
func (a *API) AcceptFixes(ctx context.Context, req api.AcceptFixesRequestObject) (api.AcceptFixesResponseObject, error) {
	if a.Change == nil {
		return nil, kernel.Invalid("not_supported", "This server cannot change bundle files.")
	}
	run, b, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	q := a.DB.Queries()
	cur, err := version.LoadCurrent(ctx, q, b)
	if err != nil {
		return nil, err
	}
	var rows []pgdb.Finding
	var suggs []suggestion
	var edits []edit
	var slugs []string
	file := ""
	seen := map[uuid.UUID]bool{}
	for _, id := range req.Body.FindingIds {
		if seen[id] {
			continue
		}
		seen[id] = true
		_, _, f, err := a.finding(ctx, run.ID, id)
		if err != nil {
			return nil, err
		}
		var sugg suggestion
		_ = json.Unmarshal(f.Suggestion, &sugg)
		if sugg.Patch == nil {
			return nil, kernel.Invalid("no_suggestion", "A section has no rewrite to accept. Run Fix all again.")
		}
		if file == "" {
			file = sugg.Patch.File
		}
		e, ok := sugg.Patch.edit(cur.File(sugg.Patch.File))
		if !ok || sugg.Patch.File != file {
			return nil, kernel.Conflict("fix_stale", "The text changed after the rewrites were written. Run Fix all again.")
		}
		rows, suggs, edits = append(rows, f), append(suggs, sugg), append(edits, e)
		if !slices.Contains(slugs, f.CheckSlug) {
			slugs = append(slugs, f.CheckSlug)
		}
	}
	next, placed, ok := applyEdits(cur.File(file), edits)
	if !ok {
		return nil, kernel.Conflict("fix_stale", "Two rewrites change the same text. Run Fix all again.")
	}
	files, err := version.Files(ctx, q, cur.ID)
	if err != nil {
		return nil, err
	}
	by := kernel.ActorFrom(ctx).UserID
	v, changed, err := a.Change(ctx, b.ID, cur.ID, source.Op{Kind: source.OpWrite, Path: file, Content: next}, by,
		fmt.Sprintf("Applied Fix all for %s in %d section%s", strings.Join(slugs, ", "), len(edits), plural(len(edits))))
	if err != nil {
		return nil, err
	}
	for i, f := range rows {
		suggs[i].Patch = nil
		raw, _ := json.Marshal(suggs[i])
		if err := q.SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: f.ID, Suggestion: raw}); err != nil {
			return nil, err
		}
	}
	after, err := a.Service.lintFiles(ctx, b, withFile(files, file, next))
	if err != nil {
		return nil, err
	}
	left := 0
	for i, f := range rows {
		left += len(rewordIn(after, f.CheckSlug, placed[i].start, placed[i].end))
	}
	ver := version.ToAPI(v)
	return api.AcceptFixes200JSONResponse{Version: &ver, Changed: changed, Sections: len(edits), Left: left}, nil
}

// GetFixPrompt returns the prompt that a person gives to a coding agent to fix the findings of
// a doc over MCP: the doc, and the loop that the MCP server instructions hold too.
func (a *API) GetFixPrompt(ctx context.Context, req api.GetFixPromptRequestObject) (api.GetFixPromptResponseObject, error) {
	b, err := a.DB.Queries().GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: req.DocId})
	if err != nil {
		return nil, kernel.NotFound("bundle_not_found", "No bundle has the ID %s.", req.DocId)
	}
	prompt := fmt.Sprintf("Fix the findings of the Speccy review of %q (%s). Use the tools of the Speccy MCP server. In each tool call, give %q as the bundle.\n\n%s",
		b.Title, b.DocPath, b.Slug, api.FixLoop)
	return api.GetFixPrompt200JSONResponse{Prompt: prompt}, nil
}
