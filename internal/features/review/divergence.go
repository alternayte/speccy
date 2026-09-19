package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/divergence"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store"
)

// Divergence finding slugs (REQ-045).
const (
	DivergenceAmbiguous = "divergence.ambiguous"
	DivergenceGap       = "divergence.gap"
)

// LowDiversityNote is the run report note of REQ-046.
const LowDiversityNote = "Low reader diversity: fewer than 2 different models answer as readers, so the divergence test finds less ambiguity. An admin can assign different models to the reader roles in Admin → Models."

// readerBatch is the most questions one reader call answers.
const readerBatch = 10

var traceIDRe = regexp.MustCompile(`^[A-Z]{2,6}-\d{3,}$`)
var mustRe = regexp.MustCompile(`\bMUST\b`)

// readerRoles returns the roles of n readers (REQ-101 names three).
func readerRoles(n int) []string {
	all := []string{model.RoleReader1, model.RoleReader2, model.RoleReader3}
	return all[:max(1, min(n, len(all)))]
}

// divergenceRoles returns the roles a divergence test calls besides the reviewer. The judge
// is needed only when there are two or more readers.
func divergenceRoles(p profile.Profile) []string {
	roles := readerRoles(p.Divergence.Readers)
	if len(roles) > 1 {
		roles = append(roles, model.RoleJudge)
	}
	return roles
}

// cite is what a build question depends on (REQ-041): a section or a trace ID.
type cite struct {
	Kind string   `json:"kind"` // section | trace
	Path []string `json:"path,omitempty"`
	ID   string   `json:"id,omitempty"`
}

type buildQuestion struct {
	id     uuid.UUID
	number int
	text   string
	level  kernel.Level
	cites  []cite
	anchor anchor.Anchor // pinned with the question: the version's text does not change
}

// readerAnswer is one reader's answer to one question, as cached.
type readerAnswer struct {
	Answer string   `json:"answer"`
	Quotes []string `json:"quotes"`
}

// checkedAnswer is a reader answer after the quote check (REQ-043).
type checkedAnswer struct {
	role        string
	fingerprint string
	answer      string
	quotes      []quoteEvidence
	quotesFound bool // at least one quote is in the bundle
	answered    bool // false for NOT SPECIFIED, and for an answer with no quote found
}

// questionOutcome is one question's answers and result, before it is stored.
type questionOutcome struct {
	q       buildQuestion
	answers []checkedAnswer
	result  divergence.Result
	groups  [][]int // reader numbers, from 1
}

// divergenceStage runs the divergence test (SDD §8.5).
func (s *Service) divergenceStage(ctx context.Context, rc *runCtx, in input, ev *evaluation, reviewerFP string) error {
	roles := readerRoles(in.profile.Profile.Divergence.Readers)
	fps := map[string]string{}
	for _, r := range divergenceRoles(in.profile.Profile) {
		a, err := s.Gateway.Assigned(ctx, r)
		if err != nil {
			return err
		}
		fps[r] = a.Backend.Kind + ":" + a.Model
	}
	distinct := map[string]bool{}
	for _, r := range roles {
		distinct[fps[r]] = true
	}
	if len(distinct) < 2 {
		rc.note(LowDiversityNote) // REQ-046: shown, never blocking
	}

	idx := newCiteIndex(in)
	rc.publish(Event{Type: "progress", Stage: StageDivergence, Message: "Writing build questions"})
	qs, err := s.pinQuestions(ctx, rc, in, idx, reviewerFP)
	if err != nil {
		return err
	}
	if len(qs) == 0 {
		rc.note("The reviewer wrote no build question that cites a section or a trace ID of the doc, so the divergence test did not run.")
		return nil
	}

	// REQ-042: each reader answers alone. A reader's prompt holds the bundle and the
	// questions only.
	bundle := bundleData(in.bundle.MainDoc, in.main, textAssets(in))
	raw := make([][]readerAnswer, len(roles))
	errs := make([]error, len(roles))
	total := len(qs) * len(roles)
	var doneMu sync.Mutex
	done := 0
	var wg sync.WaitGroup
	for ri, role := range roles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw[ri], errs[ri] = s.readerAnswers(ctx, rc, in, role, fps[role], qs, bundle, func(n int) {
				doneMu.Lock()
				done += n
				d := done
				doneMu.Unlock()
				rc.publish(Event{Type: "progress", Stage: StageDivergence, Message: "Readers answering", Done: d, Total: total})
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	texts := bundleTexts(in)
	outcomes := make([]questionOutcome, len(qs))
	for qi, q := range qs {
		o := questionOutcome{q: q}
		for ri, role := range roles {
			a := raw[ri][qi]
			quotes, found := checkQuotes(texts, a.Quotes)
			o.answers = append(o.answers, checkedAnswer{
				role: role, fingerprint: fps[role], answer: a.Answer, quotes: quotes, quotesFound: found,
				answered: !divergence.IsNotSpecified(a.Answer) && found,
			})
		}
		outcomes[qi] = o
	}

	// The judge groups the answers of each question that every reader answered.
	var judged []int
	for qi, o := range outcomes {
		if len(roles) > 1 && allAnswered(o.answers) {
			judged = append(judged, qi)
		}
	}
	jerrs := make([]error, len(judged))
	done = 0
	for ji, qi := range judged {
		wg.Add(1)
		go func() {
			defer wg.Done()
			groups, err := s.judge(ctx, rc, outcomes[qi].q.text, outcomes[qi].answers, fps[model.RoleJudge])
			outcomes[qi].groups, jerrs[ji] = groups, err
			doneMu.Lock()
			done++
			d := done
			doneMu.Unlock()
			rc.publish(Event{Type: "progress", Stage: StageDivergence, Message: "Comparing answers", Done: d, Total: len(judged)})
		}()
	}
	wg.Wait()
	for _, err := range jerrs {
		if err != nil {
			return err
		}
	}

	for qi := range outcomes {
		o := &outcomes[qi]
		answered := make([]bool, len(o.answers))
		for i, a := range o.answers {
			answered[i] = a.answered
		}
		if o.groups == nil && allAnswered(o.answers) {
			o.groups = [][]int{{1}} // one reader: one group
		}
		o.result = divergence.Classify(answered, len(o.groups))
		if o.groups == nil {
			o.groups = [][]int{}
		}
		addDivergenceFinding(in, ev, *o)
	}
	ev.questions = outcomes
	return nil
}

func allAnswered(as []checkedAnswer) bool {
	for _, a := range as {
		if !a.answered {
			return false
		}
	}
	return true
}

// addDivergenceFinding records one question as a Precision item, and diverge and gap as
// findings at the question's level (REQ-045).
func addDivergenceFinding(in input, ev *evaluation, o questionOutcome) {
	slug := DivergenceAmbiguous
	if o.result == divergence.Gap {
		slug = DivergenceGap
	}
	lvl := in.level(slug, o.q.level)
	ev.items = append(ev.items, verdict.Item{Slug: slug, Category: verdict.Precision, Level: lvl, Passed: o.result == divergence.Agree, Applicable: true})
	if o.result == divergence.Agree {
		return
	}
	// Evidence names readers by number only (DEC-013).
	answers := make([]map[string]any, len(o.answers))
	for i, a := range o.answers {
		answers[i] = map[string]any{"reader": i + 1, "answer": a.answer, "quotes": nonNilQuotes(a.quotes), "answered": a.answered}
	}
	evidence := map[string]any{
		"question": o.q.text, "number": o.q.number, "cites": o.q.cites, "result": o.result,
		"answers": answers, "groups": o.groups,
	}
	msg := "Readers gave different answers to this build question: " + o.q.text
	fix := "State one answer in the doc, so that every reader builds the same thing."
	if o.result == divergence.Gap {
		msg = "The doc does not answer this build question: " + o.q.text
		fix = "Answer the question in the doc, or state that it is out of scope."
	}
	ev.findings = append(ev.findings, pending{
		slug: slug, level: lvl, stage: StageDivergence, anchor: o.q.anchor,
		message: msg, fix: fix, evidence: evidence,
	})
}

func nonNilQuotes(q []quoteEvidence) []quoteEvidence {
	if q == nil {
		return []quoteEvidence{}
	}
	return q
}

// pinQuestions returns the build questions of the version: the pinned set when one exists
// (REQ-047), else a new set from the reviewer, which is then pinned.
func (s *Service) pinQuestions(ctx context.Context, rc *runCtx, in input, idx citeIndex, reviewerFP string) ([]buildQuestion, error) {
	if in.version == uuid.Nil {
		return s.cachedQuestions(ctx, rc, in, idx, reviewerFP)
	}
	q := s.DB.Queries()
	rows, err := q.ListQuestions(ctx, in.version)
	if err != nil {
		return nil, err
	}
	inputHash := bundleHash(in)
	if len(rows) == 0 {
		// A version with the same input as an earlier one (a waiver approval) reuses its
		// questions, copied to this version.
		earlier, err := q.ListQuestionsByInput(ctx, pgdb.ListQuestionsByInputParams{BundleID: in.bundle.ID, InputHash: inputHash})
		if err != nil {
			return nil, err
		}
		if len(earlier) > 0 {
			err = s.DB.InTx(ctx, func(tx store.Tx) error {
				for _, e := range earlier {
					if err := tx.Queries().InsertQuestion(ctx, pgdb.InsertQuestionParams{
						ID: kernel.NewID(), WorkspaceID: s.Workspace, BundleID: in.bundle.ID, VersionID: in.version, Number: e.Number,
						Text: e.Text, Level: e.Level, Cites: e.Cites, Anchor: e.Anchor, InputHash: inputHash,
					}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if rows, err = q.ListQuestions(ctx, in.version); err != nil {
				return nil, err
			}
		}
	}
	if len(rows) > 0 {
		rc.hit()
		out := make([]buildQuestion, len(rows))
		for i, r := range rows {
			var cites []cite
			var an anchor.Anchor
			_ = json.Unmarshal(r.Cites, &cites)
			_ = json.Unmarshal(r.Anchor, &an)
			out[i] = buildQuestion{id: r.ID, number: int(r.Number), text: r.Text, level: kernel.Level(r.Level), cites: cites, anchor: an}
		}
		return out, nil
	}

	out, err := s.newQuestions(ctx, rc, in, idx)
	if err != nil {
		return nil, err
	}
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		for _, bq := range out {
			cites, _ := json.Marshal(bq.cites)
			an, _ := json.Marshal(bq.anchor)
			if err := q.InsertQuestion(ctx, pgdb.InsertQuestionParams{
				ID: bq.id, WorkspaceID: s.Workspace, BundleID: in.bundle.ID, VersionID: in.version,
				Number: int64(bq.number), Text: bq.text, Level: string(bq.level), Cites: dbtype.JSON(cites), Anchor: dbtype.JSON(an),
				InputHash: inputHash,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// readerAnswers returns one reader's answer to each question: from the cache, then in
// batches. A question the reader skips gets one more call; still unanswered, it counts as
// NOT SPECIFIED.
func (s *Service) readerAnswers(ctx context.Context, rc *runCtx, in input, role, fp string, qs []buildQuestion, bundle string, progress func(int)) ([]readerAnswer, error) {
	key := func(q buildQuestion) cacheKey {
		return cacheKey{Step: "reader:" + role, InputHash: bundleHash(in), ProfileVer: in.profile.Version, Fingerprint: fp, PromptVersion: PromptReader, Extra: hashOf(q.text)}
	}
	out := make([]readerAnswer, len(qs))
	var todo []int
	for i, q := range qs {
		ok, err := s.cached(ctx, key(q), &out[i])
		if err != nil {
			return nil, err
		}
		if ok {
			rc.hit()
			progress(1)
			continue
		}
		todo = append(todo, i)
	}
	ask := func(batch []int) (map[int]readerAnswer, error) {
		texts := make([]string, len(batch))
		for k, i := range batch {
			texts[k] = qs[i].text
		}
		res, err := rc.call(ctx, s.Gateway, model.Call{
			Role: role, PromptVersion: PromptReader, System: readerSystemPrompt,
			Prompt: readerPrompt(texts, bundle), Schema: readerSchema(len(batch)), Files: snapshot(in), MaxTokens: 8000,
		})
		if err != nil {
			return nil, err
		}
		var got struct {
			Answers []struct {
				Question int `json:"question"`
				readerAnswer
			} `json:"answers"`
		}
		if err := json.Unmarshal(res.JSON, &got); err != nil {
			return nil, err
		}
		m := map[int]readerAnswer{}
		for _, a := range got.Answers {
			if _, dup := m[a.Question-1]; !dup && a.Question >= 1 && a.Question <= len(batch) {
				m[batch[a.Question-1]] = a.readerAnswer
			}
		}
		return m, nil
	}
	for len(todo) > 0 {
		n := min(readerBatch, len(todo))
		batch := todo[:n]
		todo = todo[n:]
		got, err := ask(batch)
		if err != nil {
			return nil, err
		}
		var missing []int
		for _, i := range batch {
			if _, ok := got[i]; !ok {
				missing = append(missing, i)
			}
		}
		if len(missing) > 0 {
			again, err := ask(missing)
			if err != nil {
				return nil, err
			}
			for k, v := range again {
				got[k] = v
			}
		}
		for _, i := range batch {
			a, ok := got[i]
			if !ok {
				out[i] = readerAnswer{Answer: divergence.NotSpecified, Quotes: []string{}}
				rc.note(fmt.Sprintf("A reader gave no answer to build question %d; it counts as NOT SPECIFIED.", qs[i].number))
				continue
			}
			if a.Quotes == nil {
				a.Quotes = []string{}
			}
			out[i] = a
			if err := s.putCache(ctx, key(qs[i]), a); err != nil {
				return nil, err
			}
		}
		progress(len(batch))
	}
	return out, nil
}

// judge groups the answers to one question by meaning (REQ-044). The judge sees letters in
// a shuffled order, never reader roles. It returns groups of reader numbers, from 1.
func (s *Service) judge(ctx context.Context, rc *runCtx, question string, answers []checkedAnswer, fp string) ([][]int, error) {
	// A shuffle that depends only on the content keeps the prompt the same for the cache.
	order := make([]int, len(answers))
	for i := range order {
		order[i] = i
	}
	seed := hashOf(question)
	sort.Slice(order, func(a, b int) bool {
		return hashOf(seed, answers[order[a]].answer, fmt.Sprint(order[a])) < hashOf(seed, answers[order[b]].answer, fmt.Sprint(order[b]))
	})
	texts := make([]string, len(order))
	for k, i := range order {
		texts[k] = answers[i].answer
	}
	key := cacheKey{Step: "judge", InputHash: hashOf(append([]string{question}, texts...)...), Fingerprint: fp, PromptVersion: PromptJudge}
	var got struct {
		Analysis string     `json:"analysis"`
		Groups   [][]string `json:"groups"`
	}
	ok, err := s.cached(ctx, key, &got)
	if err != nil {
		return nil, err
	}
	if ok {
		rc.hit()
	} else {
		res, err := rc.call(ctx, s.Gateway, model.Call{
			Role: model.RoleJudge, PromptVersion: PromptJudge, System: systemPrompt,
			Prompt: judgePrompt(question, texts), Schema: judgeSchema(len(texts)), MaxTokens: 2000,
		})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(res.JSON, &got); err != nil {
			return nil, err
		}
		if err := s.putCache(ctx, key, got); err != nil {
			return nil, err
		}
	}
	raw := make([][]int, len(got.Groups))
	for gi, g := range got.Groups {
		for _, letter := range g {
			if k := int(letter[0] - 'A'); len(letter) == 1 && k >= 0 && k < len(order) {
				raw[gi] = append(raw[gi], order[k])
			}
		}
	}
	groups := divergence.Groups(len(answers), raw)
	for _, g := range groups {
		sort.Ints(g)
		for i := range g {
			g[i]++
		}
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a][0] < groups[b][0] })
	return groups, nil
}

// bundleTexts returns every text file of the bundle, the main doc first, for the quote check.
func bundleTexts(in input) [][]byte {
	out := [][]byte{in.main}
	for _, f := range in.files {
		if f.Path != in.bundle.MainDoc && utf8.Valid(f.Content) {
			out = append(out, f.Content)
		}
	}
	return out
}

// checkQuotes looks for each quote in the bundle text (REQ-043), after whitespace and
// emphasis normalization. found is true when at least one quote is in the bundle.
func checkQuotes(texts [][]byte, quotes []string) (ev []quoteEvidence, found bool) {
	for _, q := range quotes {
		ok := false
		for _, t := range texts {
			if _, _, hit := anchor.Find(t, q); hit {
				ok = true
				break
			}
		}
		ev = append(ev, quoteEvidence{Text: q, Found: ok})
		found = found || ok
	}
	return ev, found
}

// citeIndex resolves the cites of build questions against the doc.
type citeIndex struct {
	in       input
	paths    []string // "A > B" for each heading, in order
	sections map[string]*section.Section
	byTitle  map[string][]*section.Section
	defs     map[string]lint.Definition
	required map[string]bool // lower-case titles of required headings
}

func newCiteIndex(in input) citeIndex {
	idx := citeIndex{in: in, sections: map[string]*section.Section{}, byTitle: map[string][]*section.Section{},
		defs: map[string]lint.Definition{}, required: map[string]bool{}}
	for i := range in.doc.Sections {
		sec := &in.doc.Sections[i]
		if sec.Level == 0 {
			continue
		}
		p := strings.Join(sec.Path, " > ")
		idx.paths = append(idx.paths, p)
		idx.sections[strings.ToLower(p)] = sec
		t := strings.ToLower(strings.TrimSpace(sec.Title))
		idx.byTitle[t] = append(idx.byTitle[t], sec)
	}
	prefixes := append(append([]string{}, in.profile.Profile.Trace.Prefixes...), in.profile.Profile.Trace.Cover...)
	for _, d := range lint.Definitions(in.main, prefixes) {
		if _, dup := idx.defs[d.ID]; !dup {
			idx.defs[d.ID] = d
		}
	}
	for _, h := range profile.RequiredHeadings(in.profile.TemplateText) {
		idx.required[lint.NormTitle(h.Title)] = true
	}
	return idx
}

// resolve turns one cite from the reviewer into a section or a trace ID of the doc.
func (idx citeIndex) resolve(raw string) (cite, bool) {
	c := strings.Trim(strings.TrimSpace(raw), "\"'`#* ")
	if traceIDRe.MatchString(c) {
		if _, ok := idx.defs[c]; ok || regexp.MustCompile(`\b`+c+`\b`).Match(idx.in.main) {
			return cite{Kind: "trace", ID: c}, true
		}
		return cite{}, false
	}
	norm := strings.ToLower(strings.NewReplacer(" › ", " > ", " / ", " > ").Replace(c))
	if sec, ok := idx.sections[norm]; ok {
		return cite{Kind: "section", Path: sec.Path}, true
	}
	if secs := idx.byTitle[norm]; len(secs) == 1 {
		return cite{Kind: "section", Path: secs[0].Path}, true
	}
	return cite{}, false
}

// level is REQ-041: MUST when a cite is a MUST requirement or a section the template
// requires; otherwise SHOULD.
func (idx citeIndex) level(cites []cite) kernel.Level {
	for _, c := range cites {
		switch c.Kind {
		case "trace":
			if d, ok := idx.defs[c.ID]; ok && mustRe.MatchString(d.Text) {
				return kernel.Must
			}
		case "section":
			if len(c.Path) > 0 && idx.required[lint.NormTitle(c.Path[len(c.Path)-1])] {
				return kernel.Must
			}
		}
	}
	return kernel.Should
}

// anchor points a divergence finding at the first cite: a trace ID's definition, or the
// cited section's heading (§8.5 step 6).
func (idx citeIndex) anchor(cites []cite) anchor.Anchor {
	in := idx.in
	for _, c := range cites {
		switch c.Kind {
		case "trace":
			if d, ok := idx.defs[c.ID]; ok {
				return anchor.New(in.bundle.MainDoc, in.main, in.doc, d.Start, d.End)
			}
			if i := bytes.Index(in.main, []byte(c.ID)); i >= 0 {
				return anchor.New(in.bundle.MainDoc, in.main, in.doc, i, i+len(c.ID))
			}
		case "section":
			if sec, ok := idx.sections[strings.ToLower(strings.Join(c.Path, " > "))]; ok {
				an, _ := rubricAnchor(in, sec, nil)
				return an
			}
		}
	}
	return docAnchor(in)
}

// newQuestions asks the reviewer for build questions and keeps those that cite the doc.
func (s *Service) newQuestions(ctx context.Context, rc *runCtx, in input, idx citeIndex) ([]buildQuestion, error) {
	d := in.profile.Profile.Divergence
	res, err := rc.call(ctx, s.Gateway, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptQuestions, System: systemPrompt,
		Prompt: questionsPrompt(in.profile.Profile.Name, d.Questions.Min, d.Questions.Max, d.Themes, idx.paths,
			bundleData(in.bundle.MainDoc, in.main, textAssets(in))),
		Schema: questionsSchema(d.Questions.Min, d.Questions.Max), Files: snapshot(in), MaxTokens: 8000,
	})
	if err != nil {
		return nil, err
	}
	var got struct {
		Questions []struct {
			Text  string   `json:"text"`
			Cites []string `json:"cites"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(res.JSON, &got); err != nil {
		return nil, err
	}
	var out []buildQuestion
	seen := map[string]bool{}
	dropped := 0
	for _, g := range got.Questions {
		text := strings.TrimSpace(g.Text)
		if text == "" || seen[strings.ToLower(text)] {
			continue
		}
		var cites []cite
		for _, c := range g.Cites {
			if ct, ok := idx.resolve(c); ok {
				cites = append(cites, ct)
			}
		}
		if len(cites) == 0 {
			dropped++ // REQ-041: a question must cite a section or a trace ID of the doc
			continue
		}
		seen[strings.ToLower(text)] = true
		out = append(out, buildQuestion{id: kernel.NewID(), number: len(out) + 1, text: text, level: idx.level(cites), cites: cites, anchor: idx.anchor(cites)})
	}
	if dropped > 0 {
		rc.note(fmt.Sprintf("%d build questions cited no section or trace ID of the doc, so they were dropped.", dropped))
	}
	return out, nil
}

// pinnedQuestion is a build question in the cache.
type pinnedQuestion struct {
	Number int           `json:"number"`
	Text   string        `json:"text"`
	Level  kernel.Level  `json:"level"`
	Cites  []cite        `json:"cites"`
	Anchor anchor.Anchor `json:"anchor"`
}

// cachedQuestions pins the questions of content that is not saved by its content hash, in
// the cache: the same content gets the same questions (REQ-047).
func (s *Service) cachedQuestions(ctx context.Context, rc *runCtx, in input, idx citeIndex, reviewerFP string) ([]buildQuestion, error) {
	k := cacheKey{Step: "questions", InputHash: bundleHash(in), ProfileVer: in.profile.Version, Fingerprint: reviewerFP, PromptVersion: PromptQuestions}
	var pinned []pinnedQuestion
	if hit, err := s.cached(ctx, k, &pinned); err != nil {
		return nil, err
	} else if hit {
		rc.hit()
		out := make([]buildQuestion, len(pinned))
		for i, p := range pinned {
			out[i] = buildQuestion{id: kernel.NewID(), number: p.Number, text: p.Text, level: p.Level, cites: p.Cites, anchor: p.Anchor}
		}
		return out, nil
	}
	out, err := s.newQuestions(ctx, rc, in, idx)
	if err != nil {
		return nil, err
	}
	for _, q := range out {
		pinned = append(pinned, pinnedQuestion{Number: q.number, Text: q.text, Level: q.level, Cites: q.cites, Anchor: q.anchor})
	}
	return out, s.putCache(ctx, k, pinned)
}
