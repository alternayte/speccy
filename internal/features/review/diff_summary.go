package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// PromptDiffSummary is the prompt of the AI diff summary (REQ-007).
const PromptDiffSummary = "diff-summary-v1"

var diffSummarySchema = []byte(`{"type":"object","additionalProperties":false,"required":["summary","changes"],"properties":{"summary":{"type":"string"},"changes":{"type":"array","items":{"type":"string"}}}}`)

// maxDiffText bounds the diff text sent to the model.
const maxDiffText = 60_000

type diffSummary struct {
	Summary string   `json:"summary"`
	Changes []string `json:"changes"`
}

// SummarizeDiff returns what changed in meaning between two versions, from the writer role,
// and the change in findings, from the stored runs (REQ-007). The summary is cached by the
// content of both versions.
func (a *API) SummarizeDiff(ctx context.Context, req api.SummarizeDiffRequestObject) (api.SummarizeDiffResponseObject, error) {
	b, err := a.bundle(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	q := a.DB.Queries()
	from, err := version.Get(ctx, q, b, &req.Params.From)
	if err != nil {
		return nil, err
	}
	to, err := version.Get(ctx, q, b, &req.Params.To)
	if err != nil {
		return nil, err
	}
	fromFiles, err := version.Files(ctx, q, from.ID)
	if err != nil {
		return nil, err
	}
	toFiles, err := version.Files(ctx, q, to.ID)
	if err != nil {
		return nil, err
	}
	out := api.DiffSummary{Changes: []string{}, Fixed: []api.FindingBrief{}, Added: []api.FindingBrief{}}
	if err := a.verdictChange(ctx, b, from.ID, to.ID, &out); err != nil {
		return nil, err
	}

	text := diffText(b.DocPath, fromFiles, toFiles)
	if text == "" {
		out.Summary = "The two versions have the same content."
		return api.SummarizeDiff200JSONResponse(out), nil
	}
	assigned, err := a.Service.Gateway.Assigned(ctx, model.RoleWriter)
	if err != nil {
		return nil, err
	}
	k := cacheKey{Step: "diff-summary", InputHash: hashOf(filesHash(fromFiles), filesHash(toFiles)),
		Fingerprint: assigned.Backend.Kind + ":" + assigned.Model, PromptVersion: PromptDiffSummary}
	var sum diffSummary
	if hit, err := a.Service.cached(ctx, k, &sum); err != nil {
		return nil, err
	} else if !hit {
		var p strings.Builder
		p.WriteString("Below is the change from an older version of a specification to a newer one. Lines that start with - were removed; lines that start with + were added.\n")
		p.WriteString("Say what changed in meaning for the people who build from the doc: decisions, requirements, limits, behaviour, and scope. Ignore changes to wording, format, and spelling that do not change the meaning.\n")
		p.WriteString("Put one or two sentences in \"summary\". Put each change of meaning in \"changes\", one short sentence each, at most 8. When nothing changed in meaning, say so in \"summary\" and return no changes.\n\n")
		p.WriteString(data("Change", text))
		res, err := a.Service.Gateway.Call(ctx, model.Call{Role: model.RoleWriter, PromptVersion: PromptDiffSummary, System: systemPrompt,
			Prompt: p.String(), Schema: diffSummarySchema, MaxTokens: 2000})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(res.JSON, &sum); err != nil {
			return nil, err
		}
		if err := a.Service.putCache(ctx, k, sum); err != nil {
			return nil, err
		}
	}
	out.Summary = sum.Summary
	if sum.Changes != nil {
		out.Changes = sum.Changes
	}
	return api.SummarizeDiff200JSONResponse(out), nil
}

// verdictChange compares the findings of the two versions: those of their full reviews when
// both have one, and otherwise those of their lint runs.
func (a *API) verdictChange(ctx context.Context, b pgdb.SpecDoc, from, to uuid.UUID, out *api.DiffSummary) error {
	q := a.DB.Queries()
	latest := func(v uuid.UUID, kind string) (*pgdb.ReviewRun, error) {
		r, err := q.LatestCompleteRun(ctx, pgdb.LatestCompleteRunParams{SpecDocID: b.ID, VersionID: v, Kind: kind})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return &r, err
	}
	var runs [2]*pgdb.ReviewRun
	for _, kind := range []string{"full", "lint"} {
		var err error
		if runs[0], err = latest(from, kind); err != nil {
			return err
		}
		if runs[1], err = latest(to, kind); err != nil {
			return err
		}
		if runs[0] != nil && runs[1] != nil {
			break
		}
	}
	if runs[0] == nil || runs[1] == nil {
		return nil
	}
	var sets [2]map[string]api.FindingBrief
	for i, r := range runs {
		if vd, err := q.GetVerdict(ctx, r.ID); err == nil {
			res := api.VerdictResult(vd.Result)
			if i == 0 {
				out.FromVerdict = &res
			} else {
				out.ToVerdict = &res
			}
		}
		rows, err := q.ListFindings(ctx, r.ID)
		if err != nil {
			return err
		}
		sets[i] = map[string]api.FindingBrief{}
		for _, f := range rows {
			if f.Waived {
				continue
			}
			var an anchor.Anchor
			_ = json.Unmarshal(f.Anchor, &an)
			key := strings.Join([]string{f.CheckSlug, f.Message, strings.Join(strings.Fields(an.Quote), " ")}, "\x00")
			sets[i][key] = api.FindingBrief{CheckSlug: f.CheckSlug, Level: api.FindingBriefLevel(f.Level), Message: f.Message}
		}
	}
	for k, f := range sets[0] {
		if _, ok := sets[1][k]; !ok {
			out.Fixed = append(out.Fixed, f)
		}
	}
	for k, f := range sets[1] {
		if _, ok := sets[0][k]; !ok {
			out.Added = append(out.Added, f)
		}
	}
	sortBriefs(out.Fixed)
	sortBriefs(out.Added)
	return nil
}

func sortBriefs(fs []api.FindingBrief) {
	rank := map[api.FindingBriefLevel]int{api.FindingBriefLevelMUST: 0, api.FindingBriefLevelSHOULD: 1, api.FindingBriefLevelINFO: 2}
	sort.Slice(fs, func(i, j int) bool {
		if rank[fs[i].Level] != rank[fs[j].Level] {
			return rank[fs[i].Level] < rank[fs[j].Level]
		}
		return fs[i].CheckSlug+fs[i].Message < fs[j].CheckSlug+fs[j].Message
	})
}

// diffText is the change between two file sets as text: the main doc by section, then the
// other text files, with only a little unchanged context. It is empty when nothing changed.
func diffText(main string, from, to []source.File) string {
	var b strings.Builder
	var fromMain, toMain []byte
	for _, f := range from {
		if f.Path == main {
			fromMain = f.Content
		}
	}
	for _, f := range to {
		if f.Path == main {
			toMain = f.Content
		}
	}
	for _, s := range version.DiffSections(fromMain, toMain) {
		if s.Status == api.Unchanged {
			continue
		}
		title := strings.Join(s.HeadingPath, " > ")
		if title == "" {
			title = "(before the first heading)"
		}
		fmt.Fprintf(&b, "Section %s (%s)\n", title, s.Status)
		writeLines(&b, s.Lines)
	}
	for _, f := range version.DiffFiles(from, to) {
		if f.Path == main || f.Status == api.Unchanged {
			continue
		}
		fmt.Fprintf(&b, "File %s (%s)\n", f.Path, f.Status)
		if f.Binary {
			b.WriteString("  (not text)\n")
			continue
		}
		writeLines(&b, f.Lines)
	}
	s := b.String()
	if len(s) > maxDiffText {
		s = strings.ToValidUTF8(s[:maxDiffText], "") + "\n(the rest of the change is cut)\n"
	}
	return s
}

func writeLines(b *strings.Builder, ops []api.LineOp) {
	for _, op := range ops {
		lines := strings.SplitAfter(op.Text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		mark := "  "
		switch op.Op {
		case api.Insert:
			mark = "+ "
		case api.Delete:
			mark = "- "
		default:
			if len(lines) > 4 {
				lines = []string{lines[0], "…\n", lines[len(lines)-1]}
			}
		}
		for _, l := range lines {
			b.WriteString(mark + strings.TrimSuffix(l, "\n") + "\n")
		}
	}
}

// filesHash identifies a file set by its paths and contents.
func filesHash(files []source.File) string {
	parts := make([]string, 0, 2*len(files))
	for _, f := range files {
		parts = append(parts, f.Path, version.Hash(f.Content))
	}
	return hashOf(parts...)
}
