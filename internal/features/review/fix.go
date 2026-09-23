package review

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// PromptFix is the prompt of a suggested fix (REQ-025).
const PromptFix = "fix-v2"

var fixSchema = []byte(`{"type":"object","additionalProperties":false,"required":["old","new","explanation"],"properties":{"old":{"type":"string"},"new":{"type":"string"},"explanation":{"type":"string"}}}`)

// patch replaces one exact text in one file. It is stored on the finding until the author
// accepts it.
type patch struct {
	File        string    `json:"file"`
	Old         string    `json:"old"`
	New         string    `json:"new"`
	Explanation string    `json:"explanation"`
	Version     uuid.UUID `json:"version_id"`
}

// suggestion is the finding's suggestion column: the fix text from the review, and the patch.
type suggestion struct {
	Fix   string `json:"fix,omitempty"`
	Patch *patch `json:"patch,omitempty"`
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

// SuggestFix asks the writer role for a patch that fixes one finding (REQ-025). It stores the
// patch on the finding and returns it. The doc does not change (T-092). A missing upstream link
// gets no patch: the person picks the doc, and Speccy writes the link (#75).
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

	var p strings.Builder
	p.WriteString("Write the smallest change to the file that fixes the finding below. Keep the author's words and style where they are not the problem.\n")
	p.WriteString("Return \"old\": an exact text from the file, copied character for character, that occurs only once in the file, and \"new\": the text that replaces it. ")
	p.WriteString("To add text, include a nearby line in \"old\" and repeat it in \"new\". Do not invent facts: where the doc needs a decision that the file does not contain, write a short placeholder question for the author. ")
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
	if strings.TrimSpace(an.Quote) != "" && len(an.Quote) < len(src) {
		p.WriteString(data("The text the finding points at", an.Quote))
	}
	p.WriteString(data("File "+an.File, string(src)))

	call := model.Call{Role: model.RoleWriter, PromptVersion: PromptFix, System: systemPrompt, Prompt: p.String(), Schema: fixSchema, MaxTokens: 4000}
	var pt patch
	for attempt := 0; ; attempt++ {
		res, err := a.Service.Gateway.Call(ctx, call)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(res.JSON, &pt); err != nil {
			return nil, err
		}
		var why string
		if pt.Old == "" || pt.Old == pt.New || bytes.Count(src, []byte(pt.Old)) != 1 {
			why = "\"old\" must be text that occurs exactly once in the file, and \"new\" must differ from it."
		} else if isLint {
			after, err := a.Service.lintFiles(ctx, b, withFile(files, an.File, bytes.Replace(src, []byte(pt.Old), []byte(pt.New), 1)))
			if err != nil {
				return nil, err
			}
			if still, ok := sameFinding(f, an, after); ok {
				why = "the file still fails the check after the change: " + still.message
			}
		}
		if why == "" {
			break
		}
		if attempt == 1 {
			if strings.HasPrefix(why, "the file still fails") {
				return nil, kernel.Invalid("fix_failed", "The AI could not write a change that passes the check: %s Fix the finding by hand.", strings.TrimPrefix(why, "the file still fails the check after the change: "))
			}
			return nil, kernel.Invalid("fix_failed", "The AI could not write a change that applies to this file. Fix the finding by hand.")
		}
		// One more try, with the reason (DEC-014 allows one retry).
		call.Prompt = p.String() + "\nYour last answer did not work: " + why + "\n"
	}
	pt.File, pt.Version = an.File, cur.ID
	sugg.Patch = &pt
	raw, _ := json.Marshal(sugg)
	if err := a.DB.Queries().SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: f.ID, Suggestion: raw}); err != nil {
		return nil, err
	}
	return api.SuggestFix200JSONResponse{FindingId: f.ID, File: pt.File, Old: pt.Old, New: pt.New, Explanation: pt.Explanation, VersionId: pt.Version}, nil
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
	if src == nil || bytes.Count(src, []byte(pt.Old)) != 1 {
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
	next := bytes.Replace(src, []byte(pt.Old), []byte(pt.New), 1)
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
	if still, ok := sameFinding(f, an, now); ok {
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
	in, err := s.loadFiles(ctx, b, uuid.Nil, files, p)
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
