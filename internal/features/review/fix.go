package review

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
const PromptFix = "fix-v1"

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

func (a *API) finding(ctx context.Context, runID, id uuid.UUID) (pgdb.ReviewRun, pgdb.Bundle, pgdb.Finding, error) {
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
// patch on the finding and returns it. The doc does not change (T-092).
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
	src := cur.File(an.File)
	if !utf8.Valid(src) {
		return nil, kernel.Invalid("not_text", "The file %s is not text, so Speccy cannot suggest a fix for it.", an.File)
	}
	var sugg suggestion
	_ = json.Unmarshal(f.Suggestion, &sugg)

	var p strings.Builder
	p.WriteString("Write the smallest change to the file that fixes the finding below. Keep the author's words and style where they are not the problem.\n")
	p.WriteString("Return \"old\": an exact text from the file, copied character for character, that occurs only once in the file, and \"new\": the text that replaces it. ")
	p.WriteString("To add text, include a nearby line in \"old\" and repeat it in \"new\". Do not invent facts: where the doc needs a decision that the file does not contain, write a short placeholder question for the author. ")
	p.WriteString("Put one sentence in \"explanation\" that says what the change does.\n\n")
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
		if pt.Old != "" && pt.Old != pt.New && bytes.Count(src, []byte(pt.Old)) == 1 {
			break
		}
		if attempt == 1 {
			return nil, kernel.Invalid("fix_failed", "The AI could not write a change that applies to this file. Fix the finding by hand.")
		}
		// One more try: the old text must be in the file once (DEC-014 allows one retry).
		call.Prompt = p.String() + "\nYour last answer did not work: \"old\" must be text that occurs exactly once in the file, and \"new\" must differ from it.\n"
	}
	pt.File, pt.Version = an.File, cur.ID
	sugg.Patch = &pt
	raw, _ := json.Marshal(sugg)
	if err := a.DB.Queries().SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: f.ID, Suggestion: raw}); err != nil {
		return nil, err
	}
	return api.SuggestFix200JSONResponse{FindingId: f.ID, File: pt.File, Old: pt.Old, New: pt.New, Explanation: pt.Explanation, VersionId: pt.Version}, nil
}

// AcceptFix applies the finding's stored patch to the current version (REQ-025). This is the
// only path by which a suggested fix changes a doc (T-092).
func (a *API) AcceptFix(ctx context.Context, req api.AcceptFixRequestObject) (api.AcceptFixResponseObject, error) {
	_, b, f, err := a.finding(ctx, req.RunId, req.FindingId)
	if err != nil {
		return nil, err
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
	return api.AcceptFix200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}
