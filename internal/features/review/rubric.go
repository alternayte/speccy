package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// maxAssetText bounds the text assets that go into a prompt with the main doc.
const maxAssetText = 200_000

// rubricBatch is the most checks one reviewer call answers.
const rubricBatch = 8

// runCtx is the state of one full run: counters for the run report (REQ-022), the bound on
// parallel calls (REQ-105), and the progress stream (REQ-026).
type runCtx struct {
	id       uuid.UUID
	sem      chan struct{}
	progress *Broker

	mu        sync.Mutex
	tokensIn  int64
	tokensOut int64
	cost      float64
	cacheHits int
	roles     map[string]string
	prompts   map[string]string
	notes     []string
	prices    map[string][2]float64 // role → price in, out per million tokens
	stages    []stageTiming
	// temps says, for each role this run called, whether its calls went out at temperature 0.
	temps map[string]bool
}

// stageTiming is when a stage started and ended, for the run report (SDD §13.1).
type stageTiming struct {
	Stage      string     `json:"stage"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// enter ends the current stage and starts the next one, and tells live views.
func (rc *runCtx) enter(stage string) {
	now := time.Now().UTC()
	rc.mu.Lock()
	if n := len(rc.stages); n > 0 && rc.stages[n-1].FinishedAt == nil {
		rc.stages[n-1].FinishedAt = &now
	}
	rc.stages = append(rc.stages, stageTiming{Stage: stage, StartedAt: now})
	rc.mu.Unlock()
	rc.publish(Event{Type: "stage", Stage: stage})
}

func (rc *runCtx) call(ctx context.Context, g *model.Gateway, c model.Call) (model.Result, error) {
	select {
	case rc.sem <- struct{}{}:
	case <-ctx.Done():
		return model.Result{}, ctx.Err()
	}
	defer func() { <-rc.sem }()
	res, err := g.Call(ctx, c)
	rc.mu.Lock()
	rc.tokensIn += res.TokensIn
	rc.tokensOut += res.TokensOut
	if p, ok := rc.prices[c.Role]; ok {
		rc.cost += float64(res.TokensIn)/1e6*p[0] + float64(res.TokensOut)/1e6*p[1]
	}
	if res.Fingerprint != "" {
		rc.roles[c.Role] = res.Fingerprint
	}
	if res.Attempts > 0 {
		if rc.temps == nil {
			rc.temps = map[string]bool{}
		}
		rc.temps[c.Role] = res.Temperature != nil
	}
	rc.prompts[c.PromptVersion[:strings.LastIndexByte(c.PromptVersion, '-')]] = c.PromptVersion
	rc.mu.Unlock()
	return res, err
}

func (rc *runCtx) hit() {
	rc.mu.Lock()
	rc.cacheHits++
	rc.mu.Unlock()
}

func (rc *runCtx) note(n string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, x := range rc.notes {
		if x == n {
			return
		}
	}
	rc.notes = append(rc.notes, n)
}

func (rc *runCtx) publish(e Event) {
	rc.mu.Lock()
	e.CacheHits = rc.cacheHits
	rc.mu.Unlock()
	rc.progress.Publish(rc.id, e)
}

// bundleHash is the input hash of a doc-scope step: every file's path and content hash. The
// sidecar is left out: approving a waiver writes it, and changes no text that a model reviews,
// so the cache and the pinned questions stay (REQ-021, REQ-047).
func bundleHash(in input) string {
	parts := make([]string, 0, len(in.files)*2)
	for _, f := range in.files {
		if source.IsSidecar(f.Path) {
			continue
		}
		parts = append(parts, f.Path, version.Hash(f.Content))
	}
	return hashOf(parts...)
}

// textAssets returns the bundle's text files other than the main doc, up to maxAssetText.
func textAssets(in input) []textFile {
	var out []textFile
	total := 0
	for _, f := range in.files {
		if f.Path == in.bundle.DocPath || !utf8.Valid(f.Content) || len(f.Content) == 0 {
			continue
		}
		if total+len(f.Content) > maxAssetText {
			continue
		}
		total += len(f.Content)
		out = append(out, textFile{path: f.Path, text: string(f.Content)})
	}
	return out
}

func snapshot(in input) []model.File {
	out := make([]model.File, len(in.files))
	for i, f := range in.files {
		out[i] = model.File{Path: f.Path, Content: f.Content}
	}
	return out
}

// rubricAnswer is one check's answer, as cached.
type rubricAnswer struct {
	Slug   string   `json:"slug"`
	Result string   `json:"result"`
	Reason string   `json:"reason"`
	Quotes []string `json:"quotes"`
	// Shortfalls are the reasons a failed check fails. Each becomes one finding.
	Shortfalls []shortfall `json:"shortfalls"`
	// Prior is what the model says about each shortfall of the last review, by its number.
	Prior []priorState `json:"prior,omitempty"`
}

// priorState is the model's answer about one shortfall of the last review.
type priorState struct {
	N     int    `json:"n"`
	State string `json:"state"`
	// Question is the question of a shortfall that still holds and had none: a review of an
	// older Speccy wrote no question.
	Question string `json:"question,omitempty"`
}

// withPrior returns the answer of a failed check with the shortfalls of the last review that
// still stand, in front of the ones the model found now. A shortfall of the last review
// leaves only when the model says it is fixed: one that the model does not mention stays,
// because silence is not evidence of a fix. A check that passes has no shortfall.
func withPrior(a rubricAnswer, prior []shortfall) rubricAnswer {
	if a.Result != "fail" || len(prior) == 0 {
		return a
	}
	prior = slices.Clone(prior)
	fixed := map[int]bool{}
	for _, p := range a.Prior {
		if p.State == "fixed" {
			fixed[p.N] = true
		} else if p.N >= 1 && p.N <= len(prior) && prior[p.N-1].Question == "" {
			prior[p.N-1].Question = strings.TrimSpace(p.Question)
		}
	}
	var out []shortfall
	seen := map[[2]string]bool{}
	for i, sf := range prior {
		if !fixed[i+1] && !seen[sf.same()] {
			seen[sf.same()] = true
			out = append(out, sf)
		}
	}
	for _, sf := range a.Shortfalls {
		sf.Reason, sf.Quote = strings.TrimSpace(sf.Reason), strings.TrimSpace(sf.Quote)
		if !seen[sf.same()] {
			seen[sf.same()] = true
			out = append(out, sf)
		}
	}
	a.Shortfalls = out
	return a
}

// priorShortfalls returns the shortfalls of the last full review of the doc, by check and by
// the section that the check read. A shortfall whose quoted text is gone from the doc is left
// out: the text it was about does not exist. A doc that a person asked a fresh review for
// after that review has none.
//
// fresh marks the cache entries of the doc since the last request for a fresh review, or is
// empty when nobody asked for one: a fresh review reads no answer of a review before it.
func (s *Service) priorShortfalls(ctx context.Context, in input) (prior map[string][]shortfall, fresh string, err error) {
	if in.version == uuid.Nil || in.bundle.ID == uuid.Nil {
		return nil, "", nil // content that is not saved has no review before it
	}
	q := s.DB.Queries()
	doc, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: in.bundle.ID})
	if err != nil {
		return nil, "", err
	}
	if doc.FreshAt.Valid {
		fresh = fmt.Sprintf(":fresh:%d", doc.FreshAt.Time.UnixNano())
	}
	full, err := q.LatestFullReview(ctx, in.bundle.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fresh, nil
	}
	if err != nil {
		return nil, "", err
	}
	if doc.FreshAt.Valid && !full.StartedAt.After(doc.FreshAt.Time) {
		return nil, fresh, nil
	}
	rows, err := q.ListFindings(ctx, full.ID)
	if err != nil {
		return nil, "", err
	}
	carried, err := carriedRows(ctx, q, full.ID)
	if err != nil {
		return nil, "", err
	}
	out := map[string][]shortfall{}
	for _, f := range append(rows, carried...) {
		if f.Stage != StageRubric {
			continue
		}
		var ev struct {
			Reason  string          `json:"reason"`
			Quotes  []quoteEvidence `json:"quotes"`
			Section string          `json:"section"`
			Named   string          `json:"named"`
		}
		_ = json.Unmarshal(f.Evidence, &ev)
		if strings.TrimSpace(ev.Reason) == "" {
			continue
		}
		var sugg suggestion
		_ = json.Unmarshal(f.Suggestion, &sugg)
		sf := shortfall{Reason: strings.TrimSpace(ev.Reason), Question: sugg.Question}
		for _, quote := range ev.Quotes {
			if quote.Found {
				sf.Quote = strings.TrimSpace(quote.Text)
				break
			}
		}
		if sf.Quote != "" {
			if _, _, ok := anchor.Find(in.main, sf.Quote); !ok {
				continue
			}
		}
		key := priorKey(f.CheckSlug, ev.Section, ev.Named)
		out[key] = append(out[key], sf)
	}
	return out, fresh, nil
}

// unitsWithPrior returns the rubric units of the doc, each check with the shortfalls of the
// last full review that it judges again, one by one.
func (s *Service) unitsWithPrior(ctx context.Context, in input) ([]scopeUnit, error) {
	units := rubricUnits(in)
	prior, fresh, err := s.priorShortfalls(ctx, in)
	if err != nil {
		return nil, err
	}
	for i := range units {
		u := &units[i]
		// A check whose text did not change keeps its cached answer, with the shortfalls it
		// had. After a request for a fresh review, the answers before it do not count.
		u.inputHash += fresh
		checks := make([]rubricCheck, len(u.checks))
		for k, c := range u.checks {
			c.Prior = prior[priorKey(c.Slug, u.sectionKey(), u.namedKey())]
			checks[k] = c
		}
		u.checks = checks
	}
	return units, nil
}

// priorKey names a check with the text it read: the section of a check that runs once for each
// section, and the section that a check names. A check whose section key changed reads other
// text, so the shortfalls of its last review are not its own: the model cannot judge a
// shortfall of text that it does not read, and one that it does not call fixed stays (#116).
func priorKey(slug, sectionPath, named string) string {
	return slug + "\x00" + sectionPath + "\x00" + named
}

// shortfall is one reason that a rubric check fails, with its quote. The quote is empty when
// the content is missing.
type shortfall struct {
	Reason string `json:"reason"`
	Quote  string `json:"quote"`
	// Question is what the author must answer to fix the shortfall: it names the subject and
	// asks only for the missing fact (#110).
	Question string `json:"question,omitempty"`
}

// same names a shortfall by what it says, without its question.
func (sf shortfall) same() [2]string { return [2]string{sf.Reason, sf.Quote} }

// scopeUnit is what one group of rubric checks reads: the whole bundle, or one section.
type scopeUnit struct {
	sec *section.Section
	// named says the checks name sec: the model reads sec with its child sections and the doc
	// title, and no other doc text. Otherwise sec is one section of a scope: section check,
	// which reads the bundle for context.
	named     bool
	inputHash string
	checks    []rubricCheck
	levels    map[string]kernel.Level
}

// sectionKey names the section of a unit of a check that runs once for each section, and is
// empty for a check that runs once for the doc or for the section it names.
func (u scopeUnit) sectionKey() string {
	if u.sec == nil || u.named {
		return ""
	}
	return strings.Join(u.sec.Path, " > ")
}

// namedKey names the section that the checks of a unit name, and is empty for the other units.
func (u scopeUnit) namedKey() string {
	if !u.named {
		return ""
	}
	return strings.Join(u.sec.Path, " > ")
}

// outside reports whether an answer of the checks that name a section holds a shortfall on
// text of another section. The model read the named section alone, so such a shortfall came
// from a whole-doc review of the check through an older Speccy (#116), and the answer is not
// one of this section.
func (u scopeUnit) outside(in input, a rubricAnswer) bool {
	if !u.named {
		return false
	}
	for _, sf := range a.Shortfalls {
		if strings.TrimSpace(sf.Quote) == "" {
			continue
		}
		if _, _, ok := anchor.Find(in.main[u.sec.Start:u.sec.End], sf.Quote); ok {
			continue
		}
		if _, _, ok := anchor.Find(in.main, sf.Quote); ok {
			return true
		}
	}
	return false
}

// assets are the text files of the bundle other than the main doc, as model files.
func assets(in input) []model.File {
	var out []model.File
	for _, f := range textAssets(in) {
		out = append(out, model.File{Path: f.path, Content: []byte(f.text)})
	}
	return out
}

// docTitle is the title of the doc: the frontmatter's, or the first heading.
func docTitle(in input) string {
	if t := strings.TrimSpace(in.fm.Title); t != "" {
		return t
	}
	for _, s := range in.doc.Sections {
		if s.Level == 1 {
			return s.Title
		}
	}
	return in.bundle.Title
}

// namedHash is the input hash of the checks that name sec: the section with its child
// sections, the doc title, and the text assets. An edit to another section leaves it as it is.
func namedHash(in input, sec *section.Section) string {
	parts := []string{sec.TreeHash(in.main), docTitle(in)}
	for _, f := range textAssets(in) {
		parts = append(parts, f.path, version.Hash([]byte(f.text)))
	}
	return hashOf(parts...)
}

// rubricUnits groups the rubric checks of the profile by what each reads: the whole bundle,
// the section a check names, or every section for a check with scope section.
func rubricUnits(in input) []scopeUnit {
	levels := map[string]kernel.Level{}
	var docChecks, sectionChecks []rubricCheck
	named := map[*section.Section][]rubricCheck{}
	var order []*section.Section
	for _, c := range in.profile.Profile.Checks {
		if c.Stage != StageRubric || !c.AppliesAt(in.size) {
			continue
		}
		levels[c.Slug] = in.level(c.Slug, kernel.Level(c.Level))
		rcheck := rubricCheck{Slug: c.Slug, Question: c.Question, PassWhen: c.PassWhen}
		switch sec := c.Named(in.doc); {
		case c.Scope == "section":
			sectionChecks = append(sectionChecks, rcheck)
		case sec != nil:
			if _, ok := named[sec]; !ok {
				order = append(order, sec)
			}
			named[sec] = append(named[sec], rcheck)
		default:
			docChecks = append(docChecks, rcheck)
		}
	}
	var units []scopeUnit
	if len(docChecks) > 0 {
		units = append(units, scopeUnit{inputHash: bundleHash(in), checks: docChecks, levels: levels})
	}
	for _, sec := range order {
		units = append(units, scopeUnit{sec: sec, named: true, inputHash: namedHash(in, sec), checks: named[sec], levels: levels})
	}
	if len(sectionChecks) > 0 {
		for i := range in.doc.Sections {
			sec := &in.doc.Sections[i]
			if sec.Level == 0 || len(strings.Fields(section.Normalize(sec.Own(in.main)))) == 0 {
				continue
			}
			units = append(units, scopeUnit{sec: sec, inputHash: sec.Hash, checks: sectionChecks, levels: levels})
		}
	}
	return units
}

// rubricStage answers every rubric check with the reviewer (SDD §8.3). Checks with scope
// section run per section; others per doc. Each check's answer is cached (REQ-021).
func (s *Service) rubricStage(ctx context.Context, rc *runCtx, in input, ev *evaluation, fingerprint string) error {
	units, err := s.unitsWithPrior(ctx, in)
	if err != nil {
		return err
	}

	type result struct {
		unit    scopeUnit
		answers map[string]rubricAnswer
		err     error
	}
	results := make([]result, len(units))
	var wg sync.WaitGroup
	total := 0
	for _, u := range units {
		total += len(u.checks)
	}
	var doneMu sync.Mutex
	done := 0
	for i, u := range units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers, err := s.answerChecks(ctx, rc, in, u, fingerprint, func(n int) {
				doneMu.Lock()
				done += n
				d := done
				doneMu.Unlock()
				rc.publish(Event{Type: "progress", Stage: StageRubric, Done: d, Total: total})
			})
			results[i] = result{unit: u, answers: answers, err: err}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil {
			return r.err
		}
	}

	for _, r := range results {
		for _, c := range r.unit.checks {
			a := r.answers[c.Slug]
			lvl := r.unit.levels[c.Slug]
			applicable := a.Result != "not_applicable"
			ev.items = append(ev.items, verdict.Item{Slug: c.Slug, Category: verdict.Completeness, Level: lvl, Passed: a.Result == "pass", Applicable: applicable})
			if a.Result != "fail" {
				continue
			}
			// Each shortfall is one finding, so the author sees every one in this review and not
			// the next one after each fix. An answer with none, from an older cache entry or a
			// model that gave none, is one finding with the reason of the check.
			falls := a.Shortfalls
			if len(falls) == 0 {
				falls = []shortfall{{Reason: a.Reason}}
				if len(a.Quotes) > 0 {
					falls[0].Quote = a.Quotes[0]
				}
			}
			seen := map[[2]string]bool{}
			for _, sf := range falls {
				sf.Reason, sf.Quote = strings.TrimSpace(sf.Reason), strings.TrimSpace(sf.Quote)
				if sf.Reason == "" {
					sf.Reason = a.Reason
				}
				if seen[sf.same()] {
					continue
				}
				seen[sf.same()] = true
				var quoted []string
				if sf.Quote != "" {
					quoted = []string{sf.Quote}
				}
				an, quotes := rubricAnchor(in, r.unit.sec, quoted)
				msg := sf.Reason
				if r.unit.sec != nil && !r.unit.named {
					msg = fmt.Sprintf("%s: %s", strings.Join(r.unit.sec.Path, " › "), sf.Reason)
				}
				ev.findings = append(ev.findings, pending{
					slug: c.Slug, level: lvl, stage: StageRubric, anchor: an, message: sentence(msg),
					fix:      "Change the doc so that this holds: " + c.PassWhen,
					question: strings.TrimSpace(sf.Question),
					evidence: map[string]any{"question": c.Question, "reason": sf.Reason, "quotes": quotes, "section": r.unit.sectionKey(), "named": r.unit.namedKey()},
				})
			}
		}
	}
	return nil
}

// answerChecks answers the checks of one unit: from the cache, then in batches.
func (s *Service) answerChecks(ctx context.Context, rc *runCtx, in input, u scopeUnit, fingerprint string, progress func(int)) (map[string]rubricAnswer, error) {
	answers := map[string]rubricAnswer{}
	key := func(c rubricCheck) cacheKey {
		return cacheKey{Step: "rubric:" + c.Slug, InputHash: u.inputHash, ProfileVer: in.profile.Version, Fingerprint: fingerprint, PromptVersion: PromptRubric}
	}
	var todo []rubricCheck
	for _, c := range u.checks {
		var a rubricAnswer
		ok, err := s.cached(ctx, key(c), &a)
		if err != nil {
			return nil, err
		}
		if ok && !u.outside(in, a) {
			// An answer of an older Speccy can hold one shortfall two times.
			if a, err = s.oneOfEach(ctx, rc, c, a, fingerprint); err != nil {
				return nil, err
			}
			answers[c.Slug] = a
			rc.hit()
			progress(1)
			continue
		}
		todo = append(todo, c)
	}
	bundle := bundleData(in.bundle.DocPath, in.main, textAssets(in))
	files := snapshot(in)
	scopeNote := ""
	switch {
	case u.named:
		// The model reads the named section and nothing else of the doc, so its answer holds
		// when another section changes.
		name := strings.Join(u.sec.Path, " > ")
		scopeNote = fmt.Sprintf("The data holds one section of the doc \"%s\": the section \"%s\" with its subsections. Answer the checks from this section. The rest of the doc is not here; do not guess what it says.", docTitle(in), name)
		bundle = data("Section "+name, string(in.main[u.sec.Start:u.sec.End]))
		for _, a := range textAssets(in) {
			bundle += "\n" + data("Asset "+a.path, a.text)
		}
		files = assets(in)
	case u.sec != nil:
		scopeNote = fmt.Sprintf("Answer the checks for the section \"%s\" only. The whole bundle is below for context.", strings.Join(u.sec.Path, " > "))
		bundle = data("Section "+strings.Join(u.sec.Path, " > "), string(u.sec.Own(in.main))) + "\n" + bundle
	}
	for len(todo) > 0 {
		n := min(rubricBatch, len(todo))
		batch := todo[:n]
		todo = todo[n:]
		got, err := s.askRubric(ctx, rc, in, batch, scopeNote, bundle, files)
		if err != nil {
			return nil, err
		}
		// A check the reviewer skipped gets one more call on its own.
		var missing []rubricCheck
		for _, c := range batch {
			if _, ok := got[c.Slug]; !ok {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			again, err := s.askRubric(ctx, rc, in, missing, scopeNote, bundle, files)
			if err != nil {
				return nil, err
			}
			for k, v := range again {
				got[k] = v
			}
		}
		for _, c := range batch {
			a, ok := got[c.Slug]
			if !ok {
				a = rubricAnswer{Slug: c.Slug, Result: "not_applicable", Reason: "The reviewer gave no answer for this check."}
				rc.note(fmt.Sprintf("The reviewer gave no answer for %s; it counts as not applicable.", c.Slug))
			} else {
				if a, err = s.oneOfEach(ctx, rc, c, withPrior(a, c.Prior), fingerprint); err != nil {
					return nil, err
				}
				if err := s.putCache(ctx, key(c), a); err != nil {
					return nil, err
				}
			}
			answers[c.Slug] = a
		}
		progress(len(batch))
	}
	return answers, nil
}

func (s *Service) askRubric(ctx context.Context, rc *runCtx, in input, checks []rubricCheck, scopeNote, bundle string, files []model.File) (map[string]rubricAnswer, error) {
	slugs := make([]string, len(checks))
	for i, c := range checks {
		slugs[i] = c.Slug
	}
	sort.Strings(slugs)
	res, err := rc.call(ctx, s.Gateway, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptRubric, System: systemPrompt,
		Prompt: rubricPrompt(in.profile.Profile.Name, checks, scopeNote, bundle),
		Schema: rubricSchema(slugs), Files: files,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []rubricAnswer `json:"results"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		return nil, err
	}
	got := map[string]rubricAnswer{}
	for _, a := range out.Results {
		if _, dup := got[a.Slug]; !dup {
			got[a.Slug] = a
		}
	}
	return got, nil
}

// quoteEvidence is one quote the reviewer gave, and whether it is in the doc.
type quoteEvidence struct {
	Text  string `json:"text"`
	Found bool   `json:"found"`
}

// rubricAnchor anchors a finding on its first quote that is in the doc; else on the section,
// else on the doc.
func rubricAnchor(in input, sec *section.Section, quotes []string) (anchor.Anchor, []quoteEvidence) {
	var ev []quoteEvidence
	var an *anchor.Anchor
	for _, q := range quotes {
		s, e, ok := anchor.Find(in.main, q)
		ev = append(ev, quoteEvidence{Text: q, Found: ok})
		if ok && an == nil {
			a := anchor.New(in.bundle.DocPath, in.main, in.doc, s, e)
			an = &a
		}
	}
	if an != nil {
		return *an, ev
	}
	if sec != nil {
		end := sec.BodyStart
		if end > sec.Start {
			end--
		}
		return anchor.New(in.bundle.DocPath, in.main, in.doc, sec.Start, end), ev
	}
	return docAnchor(in), ev
}

// sentence starts msg with a capital letter and ends it with a full stop.
func sentence(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return msg
	}
	r, n := utf8.DecodeRuneInString(msg)
	msg = strings.ToUpper(string(r)) + msg[n:]
	if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, "?") && !strings.HasSuffix(msg, "!") {
		msg += "."
	}
	return msg
}
