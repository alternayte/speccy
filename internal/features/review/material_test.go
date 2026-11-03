package review_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// blockingIDs are the IDs of the findings that block a verdict.
func blockingIDs(t *testing.T, v pgdb.Verdict) map[string]bool {
	t.Helper()
	var ids []string
	_ = json.Unmarshal(v.BlockingFindingIds, &ids)
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// Material gate: a MUST finding that the judge finds no material gap in is advice. It never
// blocks, and the API shows it at SHOULD with its judgement.
func TestMaterial_AdviceDoesNotBlock(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	pe.fake.passes = map[string]bool{"sdd.limits": true}
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.consistency": {
		{"reason": "The body limit has two values.", "quote": "at 999 kilobytes for every endpoint"},
	}}
	pe.fake.advice = map[string]bool{"sdd.consistency": true}
	run, fs, v := pe.run(t, "pay")
	f := rubricFindings(fs, "sdd.consistency")
	if len(f) != 1 || f[0].Level != "MUST" {
		t.Fatalf("findings %+v, want one stored MUST finding", f)
	}
	if blockingIDs(t, v)[f[0].ID.String()] {
		t.Error("an advice finding blocks the verdict")
	}
	a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews}
	res, err := a.ListFindings(context.Background(), api.ListFindingsRequestObject{RunId: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, af := range res.(api.ListFindings200JSONResponse).Items {
		if af.Id == f[0].ID && (af.Level != api.FindingLevelSHOULD || af.Material == nil || af.Material.Class != api.MaterialClassNone) {
			t.Errorf("the API shows %s with %+v, want SHOULD with class none", af.Level, af.Material)
		}
	}

	// The judge finds a material gap: the same finding blocks, with its two options.
	pe.fake.advice = nil
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "every region", "each region", 1))
	_, fs, v = pe.run(t, "pay")
	f = rubricFindings(fs, "sdd.consistency")
	if len(f) != 1 || !blockingIDs(t, v)[f[0].ID.String()] {
		t.Fatalf("a material gap does not block: %+v", f)
	}
	if !strings.Contains(string(f[0].Evidence), `"options":["The service stores it.","The client stores it."]`) {
		t.Errorf("evidence %s, want the two options", f[0].Evidence)
	}
}

// Material gate: after a full review, a whole-doc check raises a new shortfall only in text
// that changed. Its shortfalls of the last review stay until they are fixed.
func TestMaterial_WholeDocCheckRaisesNewShortfallsOnlyInChangedText(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	pe.fake.passes = map[string]bool{"sdd.limits": true}
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.consistency": {
		{"reason": "The read limit is vague.", "quote": "Stripe allows 100 read requests per second in live mode"},
	}}
	pe.run(t, "pay")

	// The author edits Risks. The model now finds one shortfall in Limits, which did not
	// change, and one in Risks, which did.
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", "with the order ID and the account", 1))
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.consistency": {
		{"reason": "The body limit has two values.", "quote": "at 999 kilobytes for every endpoint"},
		{"reason": "The log line has no level.", "quote": "with the order ID and the account"},
	}}
	_, fs, _ := pe.run(t, "pay")
	var got []string
	for _, f := range rubricFindings(fs, "sdd.consistency") {
		got = append(got, f.Message)
	}
	if strings.Join(got, " | ") != "The read limit is vague. | The log line has no level." {
		t.Errorf("findings %q, want the old shortfall and the new one in changed text only", got)
	}
}

// Material gate: the built-in profiles leave grounding off, and a doc with no links runs no
// coherence call.
func TestGrounding_OffByDefault(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"pay/SPEC.md": groundedSDD}, "fake-1")
	on := pe.reviews.Profiles
	pe.reviews.Profiles = func() map[string]profile.Versioned {
		out := map[string]profile.Versioned{}
		for k, v := range on() {
			v.Profile.Grounding.Enabled = false
			out[k] = v
		}
		return out
	}
	pe.run(t, "pay")
	for _, p := range []string{review.PromptClaims, review.PromptVerify, review.PromptContradiction} {
		if n := pe.fake.count(p); n != 0 {
			t.Errorf("%d %s calls, want none", n, p)
		}
	}
	if pe.fake.count(review.PromptRubric) == 0 {
		t.Error("the rubric did not run")
	}
}
