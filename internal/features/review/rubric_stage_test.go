package review_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// #134: one answer of a batch that does not match the schema costs that check one more call,
// not the run. A pass with no "shortfalls" has none, and needs no other call.
func TestRubric_InvalidAnswerIsAskedAgain(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	pe.fake.passes = map[string]bool{"sdd.non-goals": true}
	var mu sync.Mutex
	asked := map[string]int{}
	pe.gateway.Fake = model.BackendFunc(func(ctx context.Context, m string, c model.Call) (model.Raw, error) {
		raw, err := pe.fake.Call(ctx, m, c)
		if err != nil || c.PromptVersion != review.PromptRubric {
			return raw, err
		}
		slugs := slugsRe.FindAllStringSubmatch(outsideData(c.Prompt), -1)
		mu.Lock()
		for _, s := range slugs {
			asked[s[1]]++
		}
		mu.Unlock()
		if len(slugs) < 2 {
			return raw, nil
		}
		var out struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal([]byte(raw.Text), &out); err != nil {
			return raw, err
		}
		for _, r := range out.Results {
			if r["slug"] == "sdd.consistency" || r["slug"] == "sdd.non-goals" {
				delete(r, "shortfalls")
			}
		}
		b, _ := json.Marshal(out)
		raw.Text = string(b)
		return raw, nil
	})
	run, fs, _ := pe.run(t, "pay")
	if asked["sdd.consistency"] != 2 {
		t.Errorf("the failed check with no shortfalls was asked %d times, want 2", asked["sdd.consistency"])
	}
	if asked["sdd.non-goals"] != 1 {
		t.Errorf("the passed check with no shortfalls was asked %d times, want 1", asked["sdd.non-goals"])
	}
	found := false
	for _, f := range fs {
		found = found || f.CheckSlug == "sdd.consistency"
	}
	if !found || strings.Contains(string(run.Notes), "sdd.consistency") {
		t.Errorf("the answer asked again is not in the run: finding %v, notes %s", found, run.Notes)
	}
}

// #134: a batch that fails for good does not lose the answers of the other batches. The next
// run asks only for the checks of the batch that failed.
func TestRubric_FailedBatchKeepsTheOthers(t *testing.T) {
	ctx := context.Background()
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	sdd := pe.reviews.Profiles()["sdd"]
	checks := append([]profile.Check{}, sdd.Profile.Checks...)
	for i := range 12 {
		checks = append(checks, profile.Check{Slug: fmt.Sprintf("sdd.extra-%02d", i), Level: "SHOULD", Stage: review.StageRubric,
			Scope: "doc", Question: "Does the doc say it?", PassWhen: "The doc says it."})
	}
	sdd.Profile.Checks = checks
	versions := map[string]profile.Versioned{"sdd": sdd}
	pe.reviews.Profiles = func() map[string]profile.Versioned { return versions }

	// Run 1: every call that asks about sdd.extra-00 gets text that is not JSON.
	broken := true
	var mu sync.Mutex
	var prompts []string
	pe.gateway.Fake = model.BackendFunc(func(ctx context.Context, m string, c model.Call) (model.Raw, error) {
		if c.PromptVersion == review.PromptRubric {
			mu.Lock()
			prompts = append(prompts, outsideData(c.Prompt))
			mu.Unlock()
			if broken && strings.Contains(outsideData(c.Prompt), "slug: sdd.extra-00\n") {
				return model.Raw{Text: "no JSON here"}, nil
			}
		}
		return pe.fake.Call(ctx, m, c)
	})
	q := pe.bundles.DB.Queries()
	b, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	started, err := pe.reviews.StartRun(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pe.reviews.RunNext(ctx); err != nil {
		t.Fatal(err)
	}
	if run, _ := q.GetRunByID(ctx, started.ID); run.Status != "failed" || !strings.Contains(run.Error, "rubric stage failed") {
		t.Fatalf("run 1 %s: %q", run.Status, run.Error)
	}

	// Run 2: only the batch with sdd.extra-00 goes to the model.
	broken = false
	prompts = nil
	pe.run(t, "pay")
	if len(prompts) != 1 || !strings.Contains(prompts[0], "slug: sdd.extra-00\n") {
		t.Fatalf("run 2 made %d rubric calls, want 1 for the batch that failed", len(prompts))
	}
	if strings.Contains(prompts[0], "slug: sdd.extra-11\n") {
		t.Error("run 2 asked again for a check of a batch that run 1 answered")
	}
}

// #143: a check with reads: [upstream] reads the PRD that the doc implements, and a change to
// the PRD's text sends it to the model again. The other checks do not read the PRD.
func TestRubric_ReadsUpstream(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"refunds-prd/PRD.md": upstreamPRD, "refunds-sdd/SPEC.md": sdd("", ""), ackPath("refunds-sdd/SPEC.md"): traceAck}, "fake-1")
	sdd := pe.reviews.Profiles()["sdd"]
	sdd.Profile.Checks = append(append([]profile.Check{}, sdd.Profile.Checks...), profile.Check{Slug: "sdd.delta", Level: "MUST", Stage: review.StageRubric,
		Scope: "doc", Reads: []string{profile.ReadsUpstream}, Question: "Is the doc a delta on the PRD?", PassWhen: "The doc does not reprint the PRD."})
	versions := map[string]profile.Versioned{"sdd": sdd}
	pe.reviews.Profiles = func() map[string]profile.Versioned { return versions }
	var mu sync.Mutex
	var prompts []string
	pe.gateway.Fake = model.BackendFunc(func(ctx context.Context, m string, c model.Call) (model.Raw, error) {
		if c.PromptVersion == review.PromptRubric {
			mu.Lock()
			prompts = append(prompts, c.Prompt)
			mu.Unlock()
		}
		return pe.fake.Call(ctx, m, c)
	})
	// asked returns the prompts that ask about sdd.delta and the others, and empties the list.
	asked := func() (delta, other []string) {
		for _, p := range prompts {
			if strings.Contains(outsideData(p), "slug: sdd.delta\n") {
				delta = append(delta, p)
			} else {
				other = append(other, p)
			}
		}
		prompts = nil
		return delta, other
	}
	pe.run(t, "refunds-sdd")
	delta, other := asked()
	if len(delta) != 1 || !strings.Contains(insideData(delta[0]), "within 5 working days") {
		t.Fatalf("the check that reads upstream was asked %d times, or without the PRD", len(delta))
	}
	for _, p := range other {
		if strings.Contains(p, "within 5 working days") {
			t.Error("a check without reads: [upstream] read the PRD")
		}
	}
	pe.write(t, "refunds-prd/PRD.md", strings.Replace(upstreamPRD, "5 working days", "3 working days", 1))
	pe.run(t, "refunds-sdd")
	delta, other = asked()
	if len(delta) != 1 || !strings.Contains(insideData(delta[0]), "within 3 working days") || len(other) != 0 {
		t.Errorf("after a PRD edit: %d calls for the check that reads upstream, %d for the others; want 1 and 0", len(delta), len(other))
	}
}

// A MUST check that the reviewer gives no valid answer for, after one more call, blocks the
// verdict with a finding on the check, and the run does not fail. The next run asks only for
// that check, and the verdict follows its answer.
func TestRubric_UnansweredMustBlocks(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	pe.fake.rubricPass = true
	drop := true
	var mu sync.Mutex
	var asked []string
	pe.gateway.Fake = model.BackendFunc(func(ctx context.Context, m string, c model.Call) (model.Raw, error) {
		raw, err := pe.fake.Call(ctx, m, c)
		if err != nil || c.PromptVersion != review.PromptRubric {
			return raw, err
		}
		mu.Lock()
		for _, s := range slugsRe.FindAllStringSubmatch(outsideData(c.Prompt), -1) {
			asked = append(asked, s[1])
		}
		mu.Unlock()
		if !drop {
			return raw, nil
		}
		var out struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal([]byte(raw.Text), &out); err != nil {
			return raw, err
		}
		kept := []map[string]any{}
		for _, r := range out.Results {
			if r["slug"] != "sdd.consistency" {
				kept = append(kept, r)
			}
		}
		b, _ := json.Marshal(map[string]any{"results": kept})
		raw.Text = string(b)
		return raw, nil
	})
	item := func(v pgdb.Verdict) (passed, applicable bool) {
		var items []struct {
			Slug       string `json:"slug"`
			Passed     bool   `json:"passed"`
			Applicable bool   `json:"applicable"`
		}
		_ = json.Unmarshal(v.Items, &items)
		for _, it := range items {
			if it.Slug == "sdd.consistency" {
				return it.Passed, it.Applicable
			}
		}
		t.Fatal("the verdict has no item for sdd.consistency")
		return false, false
	}

	_, fs, v := pe.run(t, "pay")
	var unanswered []pgdb.Finding
	for _, f := range fs {
		if f.CheckSlug == "sdd.consistency" {
			unanswered = append(unanswered, f)
		}
	}
	if len(unanswered) != 1 || unanswered[0].Level != "MUST" || unanswered[0].Message != "The reviewer gave no answer for this check. Run the review again." {
		t.Fatalf("findings of the unanswered check: %+v", unanswered)
	}
	if passed, applicable := item(v); passed || !applicable || v.Result != "not_build_ready" {
		t.Errorf("verdict %s, item passed %v applicable %v; want a MUST that blocks", v.Result, passed, applicable)
	}

	drop = false
	asked = nil
	_, fs, v = pe.run(t, "pay")
	if len(asked) != 1 || asked[0] != "sdd.consistency" {
		t.Errorf("run 2 asked for %v, want only sdd.consistency", asked)
	}
	for _, f := range fs {
		if f.CheckSlug == "sdd.consistency" {
			t.Errorf("the check that has an answer now still has a finding: %s", f.Message)
		}
	}
	if passed, _ := item(v); !passed {
		t.Error("the verdict does not follow the answer of run 2")
	}
}

// #139: a check that names a section reads the sections that its text points to. An edit to a
// pointed-to section asks the check again; an edit elsewhere takes it from the cache, also when
// its answer quotes the pointed-to section.
func TestRubric_ReadsPointedToSections(t *testing.T) {
	const doc = `---
type: sdd
title: Pay
---

# Pay

## Security

Each request carries a token. Governance and compliance records the deviation. The service keeps the data for one year.

## Governance and compliance

The break-glass account skips MFA, and the security team reviews each use within 24 hours.

## Data

Orders and refunds.

## Notes

Nothing else.
`
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": doc}, "fake-1")
	sdd := pe.reviews.Profiles()["sdd"]
	sdd.Profile.Checks = append(append([]profile.Check{}, sdd.Profile.Checks...), profile.Check{Slug: "sdd.pointer", Level: "MUST", Stage: review.StageRubric,
		Scope: "doc", Section: "Security", Question: "Is privileged access addressed?", PassWhen: "Privileged access is addressed."})
	versions := map[string]profile.Versioned{"sdd": sdd}
	pe.reviews.Profiles = func() map[string]profile.Versioned { return versions }
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.pointer": {{"reason": "The review is late.", "quote": "the security team reviews each use within 24 hours"}}}
	var mu sync.Mutex
	var prompts []string
	pe.gateway.Fake = model.BackendFunc(func(ctx context.Context, m string, c model.Call) (model.Raw, error) {
		if c.PromptVersion == review.PromptRubric && strings.Contains(outsideData(c.Prompt), "slug: sdd.pointer\n") {
			mu.Lock()
			prompts = append(prompts, c.Prompt)
			mu.Unlock()
		}
		return pe.fake.Call(ctx, m, c)
	})
	asked := func() []string {
		out := prompts
		prompts = nil
		return out
	}

	pe.run(t, "pay")
	got := asked()
	if len(got) != 1 {
		t.Fatalf("sdd.pointer was asked %d times, want 1", len(got))
	}
	if !strings.Contains(got[0], "Pointed-to section Pay > Governance and compliance:") || !strings.Contains(insideData(got[0]), "break-glass account") {
		t.Error("the check did not read the pointed-to section")
	}
	if strings.Contains(insideData(got[0]), "Orders and refunds") || strings.Contains(insideData(got[0]), "Nothing else") {
		t.Error("the check read a section that its section does not point to")
	}

	pe.write(t, "pay/SPEC.md", strings.Replace(doc, "Nothing else.", "Nothing more.", 1))
	pe.run(t, "pay")
	if n := len(asked()); n != 0 {
		t.Errorf("after an edit of another section, sdd.pointer was asked %d times, want 0", n)
	}

	pe.write(t, "pay/SPEC.md", strings.Replace(doc, "break-glass account", "emergency account", 1))
	pe.run(t, "pay")
	if n := len(asked()); n != 1 {
		t.Errorf("after an edit of the pointed-to section, sdd.pointer was asked %d times, want 1", n)
	}
}
