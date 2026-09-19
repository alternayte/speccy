package review_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const divergenceProfile = `key: sdd
name: Test SDD
template: t.md
trace:
  prefixes: [REQ, DEC]
divergence:
  readers: 3
  questions: { min: 1, max: 5 }
checks:
  - slug: sdd.limits
    level: MUST
    stage: rubric
    question: Does the document state numeric limits for sizes and timeouts?
    pass_when: Each limit has a number.
`

const divergenceTemplate = "# SDD\n\n## Retries <!-- required -->\n\n## Notes\n"

const divergenceSDD = `---
type: sdd
title: Payments
---

# Payments

## Retries

- **REQ-001:** On a timeout, the client retries the request, and the server retries the call once. The service MUST NOT send more than three attempts.
- **REQ-002:** The service logs each attempt with the order ID.

## Notes

The team reviews the retry policy each quarter with the payments owner.
`

// newDivergence is a pipeline on the divergence fixture, with the given questions.
func newDivergence(t *testing.T, e storetest.Engine, qs ...fakeQuestion) *pipelineEnv {
	t.Helper()
	pe := newPipeline(t, e, map[string]string{
		"pay/SPEC.md":               divergenceSDD,
		".speccy/profiles/sdd.yaml": divergenceProfile,
		".speccy/profiles/t.md":     divergenceTemplate,
	}, "fake-1")
	pe.fake.questions = qs
	return pe
}

type divEvidence struct {
	Question string
	Result   string
	Groups   [][]int
	Answers  []struct {
		Reader   int
		Answer   string
		Answered bool
		Quotes   []struct {
			Text  string
			Found bool
		}
	}
}

func divergenceFindings(t *testing.T, fs []pgdb.Finding) map[string]struct {
	f  pgdb.Finding
	ev divEvidence
} {
	t.Helper()
	out := map[string]struct {
		f  pgdb.Finding
		ev divEvidence
	}{}
	for _, f := range fs {
		if f.Stage != review.StageDivergence {
			continue
		}
		var ev divEvidence
		if err := json.Unmarshal(f.Evidence, &ev); err != nil {
			t.Fatal(err)
		}
		out[ev.Question] = struct {
			f  pgdb.Finding
			ev divEvidence
		}{f, ev}
	}
	return out
}

// T-060
func TestDivergence_SplitIsFinding(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			pe := newDivergence(t, e,
				fakeQuestion{"Who retries a failed request?", "REQ-001"},
				fakeQuestion{"What does the service log?", "REQ-002"})
			_, fs, _ := pe.run(t, "pay")
			got := divergenceFindings(t, fs)
			split, ok := got["Who retries a failed request?"]
			if !ok {
				t.Fatalf("no finding for the split question; findings %v", got)
			}
			// REQ-041: REQ-001 is a MUST requirement, so the question is MUST.
			if split.f.CheckSlug != review.DivergenceAmbiguous || split.f.Level != "MUST" || split.ev.Result != "diverge" {
				t.Errorf("split: %s %s %s", split.f.CheckSlug, split.f.Level, split.ev.Result)
			}
			if len(split.ev.Groups) != 2 || len(split.ev.Answers) != 3 {
				t.Errorf("split evidence: groups %v, %d answers", split.ev.Groups, len(split.ev.Answers))
			}
			if !strings.Contains(string(split.f.Anchor), "REQ-001") {
				t.Errorf("split anchor %s, want the REQ-001 definition", string(split.f.Anchor))
			}
			if _, ok := got["What does the service log?"]; ok {
				t.Error("a question the readers agree on has a finding")
			}
		})
	}
}

// T-061
func TestDivergence_GapOnMust(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			pe := newDivergence(t, e,
				fakeQuestion{"What is the unknown timeout value?", "Retries"},
				fakeQuestion{"Who is the unknown reviewer?", "Notes"})
			_, fs, v := pe.run(t, "pay")
			got := divergenceFindings(t, fs)
			must, should := got["What is the unknown timeout value?"], got["Who is the unknown reviewer?"]
			// "Retries" is a required heading in the template, so its question is MUST.
			if must.f.CheckSlug != review.DivergenceGap || must.f.Level != "MUST" || must.ev.Result != "gap" {
				t.Errorf("required section gap: %s %s %s", must.f.CheckSlug, must.f.Level, must.ev.Result)
			}
			if should.f.CheckSlug != review.DivergenceGap || should.f.Level != "SHOULD" {
				t.Errorf("optional section gap: %s %s", should.f.CheckSlug, should.f.Level)
			}
			if !strings.Contains(string(v.BlockingFindingIds), must.f.ID.String()) {
				t.Errorf("the MUST gap does not block: %s", v.BlockingFindingIds)
			}
		})
	}
}

// T-062
func TestDivergence_InventedQuoteRejected(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			pe := newDivergence(t, e, fakeQuestion{"How often is the invented job run?", "Notes"})
			run, fs, _ := pe.run(t, "pay")
			f, ok := divergenceFindings(t, fs)["How often is the invented job run?"]
			if !ok || f.ev.Result != "gap" {
				t.Fatalf("answers with invented quotes: result %q, want gap", f.ev.Result)
			}
			for _, a := range f.ev.Answers {
				if a.Answered || len(a.Quotes) != 1 || a.Quotes[0].Found {
					t.Errorf("reader %d: answered %v, quotes %+v", a.Reader, a.Answered, a.Quotes)
				}
			}
			answers, err := pe.bundles.DB.Queries().ListAnswers(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(answers) != 3 {
				t.Fatalf("%d stored answers, want 3", len(answers))
			}
			for _, a := range answers {
				if a.QuotesFound {
					t.Errorf("%s: quotes_found is true", a.ReaderRole)
				}
			}
		})
	}
}

// T-063
func TestDivergence_LowDiversityFlagged(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			pe := newDivergence(t, e, fakeQuestion{"What does the service log?", "REQ-002"})
			pe.fake.rubricPass = true
			run, _, v := pe.run(t, "pay")
			if strings.Contains(string(run.Notes), "Low reader diversity") {
				t.Errorf("three reader models, but the run notes %s", run.Notes)
			}
			q := pe.bundles.DB.Queries()
			a, err := q.GetAssignment(ctx, pgdb.GetAssignmentParams{WorkspaceID: pe.bundles.Workspace, Role: model.RoleReader1})
			if err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{model.RoleReader2, model.RoleReader3} {
				if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: pe.bundles.Workspace, Role: role, BackendID: a.BackendID, Model: a.Model}); err != nil {
					t.Fatal(err)
				}
			}
			run2, _, v2 := pe.run(t, "pay")
			if !strings.Contains(string(run2.Notes), review.LowDiversityNote) {
				t.Errorf("one reader model, but the run notes %s", run2.Notes)
			}
			if v.Result != "build_ready" || v2.Result != v.Result {
				t.Errorf("verdicts: diverse %s, one model %s; want build_ready for both", v.Result, v2.Result)
			}
		})
	}
}

// T-064
func TestDivergence_ReaderIsolation(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			pe := newDivergence(t, e,
				fakeQuestion{"Who retries a failed request?", "REQ-001"},
				fakeQuestion{"What is the unknown timeout value?", "Retries"})
			_, fs, _ := pe.run(t, "pay")
			// A second run of a changed doc: readers must not see the first run's findings.
			pe.write(t, "pay/SPEC.md", strings.Replace(divergenceSDD, "each quarter", "each month", 1))
			pe.run(t, "pay")
			forbidden := []string{
				"numeric limits for sizes and timeouts", "Each limit has a number", "sdd.limits", "slug:", // the rubric
				"The server retries.", "The client retries.", // answers
			}
			for _, f := range fs {
				forbidden = append(forbidden, f.Message)
			}
			roles := []string{model.RoleReader1, model.RoleReader2, model.RoleReader3}
			for _, role := range roles {
				prompts := pe.fake.prompts[role]
				if len(prompts) == 0 {
					t.Fatalf("%s got no prompt", role)
				}
				for _, p := range prompts {
					for _, bad := range forbidden {
						if strings.Contains(p, bad) {
							t.Errorf("a %s prompt contains %q", role, bad)
						}
					}
				}
			}
		})
	}
}

// REQ-047: a run on the same version reuses the pinned questions; a new version gets new ones.
func TestDivergence_QuestionsPinned(t *testing.T) {
	ctx := context.Background()
	pe := newDivergence(t, storetest.Engines()[0], fakeQuestion{"What does the service log?", "REQ-002"})
	run1, _, _ := pe.run(t, "pay")
	pe.fake.questions = []fakeQuestion{{"Who retries a failed request?", "REQ-001"}}
	run2, _, _ := pe.run(t, "pay")
	if n := pe.fake.count(review.PromptQuestions); n != 1 {
		t.Errorf("two runs of one version wrote questions %d times, want 1", n)
	}
	q := pe.bundles.DB.Queries()
	r1, _ := q.ListQuestionResults(ctx, run1.ID)
	r2, _ := q.ListQuestionResults(ctx, run2.ID)
	if len(r1) != 1 || len(r2) != 1 || r1[0].QuestionID != r2[0].QuestionID {
		t.Errorf("question results: %+v and %+v, want the same question", r1, r2)
	}
	// A version that only adds a frontmatter waiver has the same input: it reuses the questions.
	withWaiver := strings.Replace(divergenceSDD, "title: Payments\n", "title: Payments\nwaivers:\n  - check: lint.placeholder\n    section: [Payments]\n    reason: A reason that is long enough.\n    section_hash: sha256:x\n", 1)
	pe.write(t, "pay/SPEC.md", withWaiver)
	pe.run(t, "pay")
	if n := pe.fake.count(review.PromptQuestions); n != 1 {
		t.Errorf("a version with only a new waiver wrote questions again (%d times in all)", n)
	}
	pe.write(t, "pay/SPEC.md", strings.Replace(divergenceSDD, "each quarter", "each month", 1))
	pe.run(t, "pay")
	if n := pe.fake.count(review.PromptQuestions); n != 2 {
		t.Errorf("a new version wrote questions %d times in all, want 2", n)
	}
}
