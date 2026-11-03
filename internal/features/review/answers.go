package review

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// answerItem is one answer of the questions screen, with what Speccy needs to write it.
type answerItem struct {
	f      pgdb.Finding
	an     anchor.Anchor
	sugg   suggestion
	t      fixTarget
	answer string
	option int
}

// span is the part of the file a fix target replaces, or the place where it adds text.
func (it answerItem) span() (start, end int, at string) {
	if it.t.insert != "" {
		return -1, -1, it.t.insert + "\x00" + it.t.before
	}
	return it.t.start, it.t.end, ""
}

// overlaps reports whether two answers change the same text, or add text at one place.
func overlaps(a, b answerItem) bool {
	as, ae, aat := a.span()
	bs, be, bat := b.span()
	if aat != "" || bat != "" {
		return aat == bat
	}
	return as < be && bs < ae || as == bs
}

// SuggestAnswers writes the answers of the questions screen into the doc as patches (material
// gate). Answers that change the same text share one patch, so the author accepts them all in
// one version. A patch that suggestFix wrote for the same answer on the current version, when
// the author picked it, is reused: the author waits only for the answers that share text.
func (a *API) SuggestAnswers(ctx context.Context, req api.SuggestAnswersRequestObject) (api.SuggestAnswersResponseObject, error) {
	run, b, err := a.run(ctx, req.RunId)
	if err != nil {
		return nil, err
	}
	q := a.DB.Queries()
	cur, err := version.LoadCurrent(ctx, q, b)
	if err != nil {
		return nil, err
	}
	files, err := version.Files(ctx, q, cur.ID)
	if err != nil {
		return nil, err
	}
	var items []answerItem
	seen := map[uuid.UUID]bool{}
	file := ""
	for _, x := range req.Body.Answers {
		answer := strings.TrimSpace(x.Answer)
		if seen[x.FindingId] || answer == "" {
			continue
		}
		seen[x.FindingId] = true
		_, _, f, err := a.finding(ctx, run.ID, x.FindingId)
		if err != nil {
			return nil, err
		}
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		an, ok := cur.Anchor(an)
		if !ok {
			return nil, kernel.Conflict("finding_detached", "The text of the question %q changed, so Speccy cannot find it. Run the review again.", f.Message)
		}
		if file == "" {
			file = an.File
		}
		if an.File != file {
			return nil, kernel.Invalid("two_files", "The answers are about two files. Answer the questions of one file at a time.")
		}
		var sugg suggestion
		_ = json.Unmarshal(f.Suggestion, &sugg)
		option := 0
		if x.Option != nil {
			option = *x.Option
		}
		items = append(items, answerItem{f: f, an: an, sugg: sugg, answer: answer, option: option})
	}
	if len(items) == 0 {
		return nil, kernel.Invalid("no_answer", "Give at least one answer.")
	}
	src := cur.File(file)
	if !utf8.Valid(src) {
		return nil, kernel.Invalid("not_text", "The file %s is not text, so Speccy cannot write an answer into it.", file)
	}
	for i := range items {
		items[i].t = a.Service.fixTarget(b, items[i].f, items[i].an, src)
	}
	// Join the answers that change the same text: each group gets one patch.
	group := make([]int, len(items))
	for i := range group {
		group[i] = i
	}
	root := func(i int) int {
		for group[i] != i {
			i = group[i]
		}
		return i
	}
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			if overlaps(items[i], items[j]) {
				group[root(j)] = root(i)
			}
		}
	}
	byRoot := map[int][]int{}
	var roots []int
	for i := range items {
		r := root(i)
		if _, ok := byRoot[r]; !ok {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], i)
	}

	type result struct {
		pt  patch
		err error
	}
	results := make([]result, len(roots))
	sem := make(chan struct{}, a.Service.parallel(ctx))
	var wg sync.WaitGroup
	for gi, r := range roots {
		members := byRoot[r]
		lead := items[members[0]]
		if len(members) == 1 && lead.sugg.Patch != nil && lead.sugg.Answer == lead.answer && lead.sugg.Patch.Version == cur.ID && len(lead.sugg.Answers) == 0 {
			results[gi] = result{pt: *lead.sugg.Patch}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// The patch replaces the union of the members' text.
			t := lead.t
			if t.insert == "" {
				for _, m := range members[1:] {
					t.start, t.end = min(t.start, items[m].t.start), max(t.end, items[m].t.end)
				}
				if len(members) > 1 {
					t.what = "the part of the section that the answers are about"
				}
			}
			pt := patch{File: file, Start: t.start, End: t.end, Old: string(src[t.start:t.end]), Insert: t.insert, Before: t.before, Version: cur.ID}
			qas := make([]answered, 0, len(members))
			for _, m := range members {
				it := items[m]
				qas = append(qas, answered{finding: it.f, anchor: it.an, answer: it.answer,
					question: answerQuestion(it.f.CheckSlug, it.f.Message, it.an.Quote, it.sugg.Question, it.f.Evidence)})
			}
			pt, err := a.writePatch(ctx, b, lead.f, lead.an, src, files, t, pt, "", qas)
			results[gi] = result{pt: pt, err: err}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
	}

	out := api.AnswerPatches{Patches: make([]api.FixSuggestion, 0, len(roots))}
	for gi, r := range roots {
		members := byRoot[r]
		pt := results[gi].pt
		ids := make([]uuid.UUID, len(members))
		for k, m := range members {
			ids[k] = items[m].f.ID
		}
		for k, m := range members {
			it := items[m]
			it.sugg.Answer, it.sugg.Option = it.answer, it.option
			if k == 0 {
				p := pt
				it.sugg.Patch, it.sugg.Answers = &p, slices.Clone(ids[1:])
			} else {
				it.sugg.Patch, it.sugg.Answers = nil, nil
			}
			raw, _ := json.Marshal(it.sugg)
			if err := q.SetFindingSuggestion(ctx, pgdb.SetFindingSuggestionParams{ID: it.f.ID, Suggestion: raw}); err != nil {
				return nil, err
			}
		}
		s := pt.suggestion(ids[0], src)
		s.Answers = &ids
		out.Patches = append(out.Patches, s)
	}
	return api.SuggestAnswers200JSONResponse(out), nil
}
