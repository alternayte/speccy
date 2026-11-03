package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
)

// PromptMaterial is the prompt of the materiality judge: it decides whether a MUST finding is
// a material gap.
const PromptMaterial = "material-v1"

// Consequence classes: what the two options of a material gap differ in. ClassNone says the
// finding is no material gap, so it is advice.
const (
	ClassDataModel   = "data_model"
	ClassInterface   = "interface"
	ClassSecurity    = "security"
	ClassUserVisible = "user_visible"
	ClassNone        = "none"
)

var consequenceClasses = []string{ClassDataModel, ClassInterface, ClassSecurity, ClassUserVisible, ClassNone}

// materialBatch is the most findings one judge call reads.
const materialBatch = 8

// material is the judgement of one MUST finding. A material gap has a class other than none,
// two options that a builder could pick, the option that fits the rest of the doc, and the
// question that asks the author to choose.
type material struct {
	Class    string   `json:"class"`
	Options  []string `json:"options,omitempty"`
	Pick     int      `json:"pick,omitempty"`
	Question string   `json:"question,omitempty"`
}

// Material reports whether m is a material gap.
func (m material) Material() bool { return m.Class != "" && m.Class != ClassNone }

// materialOf returns the judgement in a finding's evidence. ok is false when the finding has
// none: the judge did not read it, or it is no AI finding.
func materialOf(evidence []byte) (material, bool) {
	var ev struct {
		Material *material `json:"material"`
	}
	if json.Unmarshal(evidence, &ev) != nil || ev.Material == nil || ev.Material.Class == "" {
		return material{}, false
	}
	return *ev.Material, true
}

// advice reports whether a finding is advice: the judge read it and found no material gap. A
// finding with no judgement is not advice, so a MUST finding the judge did not read still
// blocks.
func advice(evidence []byte) bool {
	m, ok := materialOf(evidence)
	return ok && !m.Material()
}

// judged reports whether the judge reads a finding: a MUST finding of a stage that reads the
// doc with a model, or the build question of a placeholder. A deterministic MUST check blocks
// as the profile says, and a MUST check with no answer blocks until it has one.
func judged(f pending) bool {
	if f.level != kernel.Must || f.carried != uuid.Nil || f.unanswered() {
		return false
	}
	if f.placeholder {
		return true
	}
	switch f.stage {
	case StageRubric, StageDivergence, StageGrounding:
		return true
	case StageCoherence:
		return f.slug == ContradictionSlug || f.slug == ExternalConflictSlug
	}
	return false
}

// evidenceMap returns the evidence of a finding as a map, so the judge can add its key.
func evidenceMap(e any) map[string]any {
	out := map[string]any{}
	if e == nil {
		return out
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// withMaterial records the judgement on a finding. The question of a material gap replaces the
// finding's question: it is the one the author answers on the questions screen.
func (f *pending) withMaterial(m material) {
	ev := evidenceMap(f.evidence)
	ev["material"] = m
	f.evidence = ev
	if m.Material() && strings.TrimSpace(m.Question) != "" {
		f.question = strings.TrimSpace(m.Question)
	}
}

// materialText is what the judge reads of a finding, and what its cache key hashes.
func materialText(f pending) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Check: %s\nFinding: %s\n", f.slug, f.message)
	if q := strings.TrimSpace(f.anchor.Quote); q != "" && len(q) < 600 {
		fmt.Fprintf(&b, "Quoted text: %q\n", q)
	}
	if f.question != "" {
		fmt.Fprintf(&b, "Question for the author: %s\n", f.question)
	}
	ev := evidenceMap(f.evidence)
	if answers, ok := ev["answers"].([]any); ok && f.slug == DivergenceAmbiguous {
		for _, a := range answers {
			if m, ok := a.(map[string]any); ok {
				fmt.Fprintf(&b, "Reader %v answered: %v\n", m["reader"], m["answer"])
			}
		}
	}
	return b.String()
}

// materialContext is the text the judge reads with the findings of one section: the section
// with its subsections, or the whole bundle for a finding about the whole doc.
func materialContext(in input, path []string) (label, text string) {
	if len(path) > 0 {
		if sec := section.At(in.doc, path); sec != nil {
			return "Section " + strings.Join(path, " > "), string(in.main[sec.Start:sec.End])
		}
	}
	return "Main doc " + in.bundle.DocPath, string(in.main)
}

func materialKey(in input, f pending, fingerprint string) cacheKey {
	label, text := materialContext(in, f.anchor.HeadingPath)
	return cacheKey{Step: "material", InputHash: hashOf(materialText(f), label, text), ProfileVer: in.profile.Version,
		Fingerprint: fingerprint, PromptVersion: PromptMaterial}
}

// judgeMaterial decides for each MUST finding of an AI stage whether it is a material gap
// (material gate). A judgement in the cache applies at once. With call false, as in a lint run,
// the judge makes no model call: a finding with no cached judgement stays unjudged and blocks.
// A call that fails leaves its findings unjudged, with a run note, and the run goes on.
func (s *Service) judgeMaterial(ctx context.Context, rc *runCtx, in input, ev *evaluation, call bool) error {
	var todo []int
	for i, f := range ev.findings {
		if judged(f) {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 || s.Gateway == nil {
		return nil
	}
	assigned, err := s.Gateway.Assigned(ctx, model.RoleReviewer)
	if err != nil {
		if call {
			return err
		}
		return nil // no model: nothing to read from the cache either
	}
	fp := assigned.Backend.Kind + ":" + assigned.Model
	groups := map[string][]int{}
	var order []string
	for _, i := range todo {
		f := ev.findings[i]
		var m material
		hit, err := s.cached(ctx, materialKey(in, f, fp), &m)
		if err != nil {
			return err
		}
		if hit && m.Class != "" {
			ev.findings[i].withMaterial(m)
			if rc != nil {
				rc.hit()
				rc.question(ev.findings[i], m)
			}
			continue
		}
		key := strings.Join(f.anchor.HeadingPath, "\x00")
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	if !call || rc == nil || len(order) == 0 {
		return nil
	}
	var batches [][]int
	for _, k := range order {
		g := groups[k]
		for len(g) > 0 {
			n := min(materialBatch, len(g))
			batches = append(batches, g[:n])
			g = g[n:]
		}
	}
	results := make([]map[int]material, len(batches))
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	for bi, batch := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[bi], errs[bi] = s.askMaterial(ctx, rc, in, ev, batch)
		}()
	}
	wg.Wait()
	failed := 0
	for bi, batch := range batches {
		if errs[bi] != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed += len(batch)
			continue
		}
		for n, i := range batch {
			m, ok := results[bi][n+1]
			if !ok {
				failed++
				continue
			}
			if err := s.putCache(ctx, materialKey(in, ev.findings[i], fp), m); err != nil {
				return err
			}
			ev.findings[i].withMaterial(m)
			rc.question(ev.findings[i], m)
		}
	}
	if failed > 0 {
		rc.note(fmt.Sprintf("The judge gave no valid answer for %d MUST finding%s, so %s Build Ready until the next review.", failed, plural(failed), map[bool]string{true: "it blocks", false: "they block"}[failed == 1]))
	}
	return nil
}

// question tells live views of a material gap as soon as the judge finds it, so the questions
// screen fills while the review runs.
func (rc *runCtx) question(f pending, m material) {
	if !m.Material() {
		return
	}
	rc.publish(Event{Type: "question", Stage: f.stage, Question: &StreamedQuestion{
		Fork: f.fork(), CheckSlug: f.slug, Question: f.question, HeadingPath: f.anchor.HeadingPath, Material: m,
	}})
}

// askMaterial asks the judge about one batch of findings of one section. The answers come back
// by the finding's number in the batch, from 1.
func (s *Service) askMaterial(ctx context.Context, rc *runCtx, in input, ev *evaluation, batch []int) (map[int]material, error) {
	label, text := materialContext(in, ev.findings[batch[0]].anchor.HeadingPath)
	texts := make([]string, len(batch))
	for n, i := range batch {
		texts[n] = materialText(ev.findings[i])
	}
	res, err := rc.call(ctx, s.Gateway, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptMaterial, System: systemPrompt,
		Prompt: materialPrompt(in.profile.Profile.Name, docTitle(in), texts, data(label, text)),
		Schema: materialSchema(len(batch)), MaxTokens: 4000,
	})
	if err != nil {
		return nil, err
	}
	return materialResults(res.JSON, len(batch))
}

// materialResults reads the judge's answers. An answer that breaks a rule of the material gate
// is left out: a material class with no two different options, or a pick that is not one of
// them.
func materialResults(reply []byte, n int) (map[int]material, error) {
	var out struct {
		Results []struct {
			N        int      `json:"n"`
			Class    string   `json:"class"`
			Options  []string `json:"options"`
			Pick     int      `json:"pick"`
			Question string   `json:"question"`
		} `json:"results"`
	}
	if err := json.Unmarshal(reply, &out); err != nil {
		return nil, err
	}
	got := map[int]material{}
	for _, r := range out.Results {
		if r.N < 1 || r.N > n {
			continue
		}
		if _, dup := got[r.N]; dup {
			continue
		}
		m := material{Class: r.Class}
		if m.Material() {
			if len(r.Options) != 2 {
				continue
			}
			a, b := strings.TrimSpace(r.Options[0]), strings.TrimSpace(r.Options[1])
			if a == "" || b == "" || strings.EqualFold(a, b) || (r.Pick != 1 && r.Pick != 2) || strings.TrimSpace(r.Question) == "" {
				continue
			}
			m.Options, m.Pick, m.Question = []string{a, b}, r.Pick, strings.TrimSpace(r.Question)
		}
		got[r.N] = m
	}
	return got, nil
}

// materialPrompt asks the judge whether each finding is a material gap.
func materialPrompt(docType, title string, findings []string, context string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "An engineer must build the system that this %s (%q) describes. The engineer cannot ask the author anything. A review found the problems below in the doc. For each one, decide whether it is a material gap.\n\n", docType, title)
	b.WriteString("A material gap is a place where the engineer must guess, and two plausible guesses give a different result in one of these ways:\n")
	b.WriteString("- data_model: a different stored entity, field, type, constraint or state;\n")
	b.WriteString("- interface: a different input, output, error, protocol or contract that another component or a client sees;\n")
	b.WriteString("- security: a different rule for who can do what, for secrets, or for untrusted input;\n")
	b.WriteString("- user_visible: a different result that a user of the system sees.\n")
	b.WriteString("A problem is not a material gap when every plausible guess builds the same thing, or when it is about wording, format, a heading, a missing ID, a template section, a process, a plan, or a team. Give \"none\" for it.\n\n")
	b.WriteString("For each problem, by its number in \"n\":\n")
	b.WriteString("- First, in \"analysis\", say what the engineer would have to guess, and name two plausible guesses. Use the section text, not your opinion of good design.\n")
	b.WriteString("- In \"class\", give the first of data_model, interface, security, user_visible that the two guesses differ in, or \"none\".\n")
	b.WriteString("- For a material gap, give \"options\": exactly two answers that the author could write into the doc, each one short sentence that settles the gap. Give \"pick\": 1 or 2, the option that fits the rest of the text better. Give \"question\": one question that asks the author to choose, which names the subject.\n")
	b.WriteString("- For \"none\", give empty options, pick 0, and an empty question.\n\n")
	for i, f := range findings {
		b.WriteString(data(fmt.Sprintf("Problem %d", i+1), strings.TrimRight(f, "\n")))
	}
	b.WriteString("\n")
	b.WriteString(context)
	return b.String()
}

// materialSchema puts "analysis" first and "class" before the options: the keys sort that way,
// so the judge reasons before it decides.
func materialSchema(n int) []byte {
	s := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"results"},
		"properties": map[string]any{
			"results": map[string]any{
				"type": "array", "minItems": n, "maxItems": n,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"analysis", "class", "n", "options", "pick", "question"},
					"properties": map[string]any{
						"analysis": map[string]any{"type": "string"},
						"class":    map[string]any{"type": "string", "enum": consequenceClasses},
						"n":        map[string]any{"type": "integer", "minimum": 1, "maximum": n},
						"options":  map[string]any{"type": "array", "maxItems": 2, "items": map[string]any{"type": "string"}},
						"pick":     map[string]any{"type": "integer", "minimum": 0, "maximum": 2},
						"question": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	out, _ := json.Marshal(s)
	return out
}
