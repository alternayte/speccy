package review

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
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

// bundleHash is the input hash of a doc-scope step: every file's path and content hash.
func bundleHash(in input) string {
	parts := make([]string, 0, len(in.files)*2)
	for _, f := range in.files {
		parts = append(parts, f.Path, version.Hash(f.Content))
	}
	return hashOf(parts...)
}

// textAssets returns the bundle's text files other than the main doc, up to maxAssetText.
func textAssets(in input) []textFile {
	var out []textFile
	total := 0
	for _, f := range in.files {
		if f.Path == in.bundle.MainDoc || !utf8.Valid(f.Content) || len(f.Content) == 0 {
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
}

// scopeUnit is what one group of rubric checks reads: the whole bundle, or one section.
type scopeUnit struct {
	sec       *section.Section
	inputHash string
	checks    []rubricCheck
	levels    map[string]kernel.Level
}

// rubricStage answers every rubric check with the reviewer (SDD §8.3). Checks with scope
// section run per section; others per doc. Each check's answer is cached (REQ-021).
func (s *Service) rubricStage(ctx context.Context, rc *runCtx, in input, ev *evaluation, fingerprint string) error {
	var docChecks []rubricCheck
	levels := map[string]kernel.Level{}
	var sectionChecks []rubricCheck
	for _, c := range in.profile.Profile.Checks {
		if c.Stage != StageRubric {
			continue
		}
		levels[c.Slug] = in.level(c.Slug, kernel.Level(c.Level))
		rcheck := rubricCheck{Slug: c.Slug, Question: c.Question, PassWhen: c.PassWhen}
		if c.Scope == "section" {
			sectionChecks = append(sectionChecks, rcheck)
		} else {
			docChecks = append(docChecks, rcheck)
		}
	}
	var units []scopeUnit
	if len(docChecks) > 0 {
		units = append(units, scopeUnit{inputHash: bundleHash(in), checks: docChecks, levels: levels})
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
			ev.items = append(ev.items, verdict.Item{Category: verdict.Completeness, Level: lvl, Passed: a.Result == "pass", Applicable: applicable})
			if a.Result != "fail" {
				continue
			}
			an, quotes := rubricAnchor(in, r.unit.sec, a.Quotes)
			msg := a.Reason
			if r.unit.sec != nil {
				msg = fmt.Sprintf("%s: %s", strings.Join(r.unit.sec.Path, " › "), a.Reason)
			}
			ev.findings = append(ev.findings, pending{
				slug: c.Slug, level: lvl, stage: StageRubric, anchor: an, message: sentence(msg),
				fix:      "Change the doc so that this holds: " + c.PassWhen,
				evidence: map[string]any{"question": c.Question, "reason": a.Reason, "quotes": quotes},
			})
		}
	}
	return nil
}

// answerChecks answers the checks of one unit: from the cache, then in batches.
func (s *Service) answerChecks(ctx context.Context, rc *runCtx, in input, u scopeUnit, fingerprint string, progress func(int)) (map[string]rubricAnswer, error) {
	answers := map[string]rubricAnswer{}
	key := func(slug string) cacheKey {
		return cacheKey{Step: "rubric:" + slug, InputHash: u.inputHash, ProfileVer: in.profile.Version, Fingerprint: fingerprint, PromptVersion: PromptRubric}
	}
	var todo []rubricCheck
	for _, c := range u.checks {
		var a rubricAnswer
		ok, err := s.cached(ctx, key(c.Slug), &a)
		if err != nil {
			return nil, err
		}
		if ok {
			answers[c.Slug] = a
			rc.hit()
			progress(1)
			continue
		}
		todo = append(todo, c)
	}
	bundle := bundleData(in.bundle.MainDoc, in.main, textAssets(in))
	scopeNote := ""
	if u.sec != nil {
		scopeNote = fmt.Sprintf("Answer the checks for the section \"%s\" only. The whole bundle is below for context.", strings.Join(u.sec.Path, " > "))
		bundle = data("Section "+strings.Join(u.sec.Path, " > "), string(u.sec.Own(in.main))) + "\n" + bundle
	}
	for len(todo) > 0 {
		n := min(rubricBatch, len(todo))
		batch := todo[:n]
		todo = todo[n:]
		got, err := s.askRubric(ctx, rc, in, batch, scopeNote, bundle)
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
			again, err := s.askRubric(ctx, rc, in, missing, scopeNote, bundle)
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
			} else if err := s.putCache(ctx, key(c.Slug), a); err != nil {
				return nil, err
			}
			answers[c.Slug] = a
		}
		progress(len(batch))
	}
	return answers, nil
}

func (s *Service) askRubric(ctx context.Context, rc *runCtx, in input, checks []rubricCheck, scopeNote, bundle string) (map[string]rubricAnswer, error) {
	slugs := make([]string, len(checks))
	for i, c := range checks {
		slugs[i] = c.Slug
	}
	sort.Strings(slugs)
	res, err := rc.call(ctx, s.Gateway, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptRubric, System: systemPrompt,
		Prompt: rubricPrompt(in.profile.Profile.Name, checks, scopeNote, bundle),
		Schema: rubricSchema(slugs), Files: snapshot(in),
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
			a := anchor.New(in.bundle.MainDoc, in.main, in.doc, s, e)
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
		return anchor.New(in.bundle.MainDoc, in.main, in.doc, sec.Start, end), ev
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
