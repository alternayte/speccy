package review_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// convergeProfile has one check that names a section, and one whole-doc check.
const convergeProfile = `key: sdd
name: Software Design Document
template: sdd.md
divergence:
  readers: 1
  questions: { min: 1, max: 2 }
checks:
  - slug: sdd.limits
    level: MUST
    stage: rubric
    section: Limits
    question: Do sizes, rates, and timeouts have numbers?
    pass_when: Sizes, rates, and timeouts have numbers.
  - slug: sdd.consistency
    level: MUST
    stage: rubric
    question: Do any two statements in the doc contradict each other?
    pass_when: No two statements in the doc contradict each other.
`

func convergeFiles() map[string]string {
	return map[string]string{
		"pay/SPEC.md":                 groundedSDD,
		".speccy/profiles/sdd.yaml":   convergeProfile,
		".speccy/profiles/sdd.md":     "# <Name>\n\n## Context\n\n## Limits\n\n## Risks\n",
	}
}

func rubricFindings(fs []pgdb.Finding, slug string) []pgdb.Finding {
	var out []pgdb.Finding
	for _, f := range fs {
		if f.Stage == review.StageRubric && f.CheckSlug == slug {
			out = append(out, f)
		}
	}
	return out
}

// A check that names a section reads that section only, so an edit to another section does
// not send it to the model again. An edit to its own section does. A whole-doc check goes to
// the model after every edit.
func TestRubric_NamedSectionKeepsItsAnswer(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	pe.run(t, "pay")
	var named string
	for _, p := range pe.fake.rubric {
		if strings.Contains(p, "slug: sdd.limits") {
			named = p
		}
	}
	if named == "" || strings.Contains(named, "slug: sdd.consistency") {
		t.Fatalf("sdd.limits did not get a call of its own")
	}
	if !strings.Contains(named, "999 kilobytes") || strings.Contains(named, "Stripe allows 100") || strings.Contains(named, "logs each retry") {
		t.Errorf("the call for the Limits section holds text of another section, or not its own:\n%s", named)
	}

	calls := func(slug string) int {
		n := 0
		for _, p := range pe.fake.rubric {
			if strings.Contains(p, "slug: "+slug) {
				n++
			}
		}
		return n
	}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", "with the order ID and the time", 1))
	pe.run(t, "pay")
	if l, c := calls("sdd.limits"), calls("sdd.consistency"); l != 1 || c != 2 {
		t.Errorf("after an edit to Risks: %d calls for sdd.limits and %d for sdd.consistency, want 1 and 2", l, c)
	}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "999 kilobytes", "998 kilobytes", 1))
	pe.run(t, "pay")
	if l := calls("sdd.limits"); l != 2 {
		t.Errorf("after an edit to Limits: %d calls for sdd.limits, want 2", l)
	}
}

// A failed check gives one finding for each shortfall, each on its own quote, so the author
// sees them all in one review.
func TestRubric_EachShortfallIsAFinding(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.limits": {
		{"reason": "The body cap has no unit of time.", "quote": "999 kilobytes"},
		{"reason": "The database has no size limit.", "quote": "Postgres 17"},
		{"reason": "No timeout is stated.", "quote": ""},
		{"reason": "The body cap has no unit of time.", "quote": "999 kilobytes"},
	}}
	_, fs, _ := pe.run(t, "pay")
	got := rubricFindings(fs, "sdd.limits")
	if len(got) != 3 {
		t.Fatalf("%d findings for sdd.limits, want 3: one for each shortfall, and none twice", len(got))
	}
	var quotes []string
	for _, f := range got {
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		quotes = append(quotes, an.Quote)
		if strings.Join(an.HeadingPath, "/") != "Payments/Limits" {
			t.Errorf("finding %q is at %v, want the Limits section", f.Message, an.HeadingPath)
		}
	}
	if quotes[0] != "999 kilobytes" || quotes[1] != "Postgres 17" {
		t.Errorf("quotes %q, want each shortfall on its own quote", quotes)
	}
}

// A whole-doc finding stays in the verdict after a save that changes another part of the doc:
// a finding of a whole-doc check, and a finding that says content is missing. Before, any save
// dropped them, and the doc could read Build Ready with a MUST finding unseen.
func TestCarry_WholeDocFindingsSurviveASave(t *testing.T) {
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	_, fs, _ := pe.run(t, "pay")
	whole, missing := rubricFindings(fs, "sdd.consistency"), rubricFindings(fs, "sdd.limits")
	if len(whole) != 1 || len(missing) != 1 {
		t.Fatalf("%d and %d rubric findings, want one of each check", len(whole), len(missing))
	}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", "with the order ID and the time", 1))
	ids, v := pe.current(t, "pay")
	for _, f := range []pgdb.Finding{whole[0], missing[0]} {
		found := false
		for _, id := range ids {
			found = found || id == f.ID.String()
		}
		if !found {
			t.Errorf("the finding of %s left the verdict after a save", f.CheckSlug)
		}
	}
	if v.Result != api.VerdictResult("not_build_ready") || v.Must < 2 {
		t.Errorf("verdict %s with %d MUST, want Not Build Ready with the two rubric findings", v.Result, v.Must)
	}
}

// A run with a stage subset keeps the findings of the stages it skips.
func TestStageSubsetKeepsTheOtherStages(t *testing.T) {
	ctx := context.Background()
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	_, fs, _ := pe.run(t, "pay")
	want := 0
	for _, f := range fs {
		if (f.Stage == review.StageRubric || f.Stage == review.StageGrounding) && f.Level == "MUST" {
			want++
		}
	}
	if want < 3 {
		t.Fatalf("the full review has %d MUST findings of the rubric and grounding stages, want 3 or more", want)
	}
	q := pe.bundles.DB.Queries()
	b, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	if _, err := pe.reviews.StartRun(ctx, b, review.Stages{review.StageDivergence}); err != nil {
		t.Fatal(err)
	}
	if ran, err := pe.reviews.RunNext(ctx); !ran || err != nil {
		t.Fatalf("run the job: ran %v, err %v", ran, err)
	}
	ids, v := pe.current(t, "pay")
	if v.Kind != api.BundleVerdictKindFull {
		t.Fatalf("the verdict is of a %s run, want the divergence run", v.Kind)
	}
	got := 0
	for _, f := range fs {
		for _, id := range ids {
			if id == f.ID.String() && (f.Stage == review.StageRubric || f.Stage == review.StageGrounding) && f.Level == "MUST" {
				got++
			}
		}
	}
	if got != want {
		t.Errorf("after a divergence-only run the verdict holds %d of the %d rubric and grounding MUST findings", got, want)
	}
}

// The author saves while a review runs. The review ends on the old version, and its findings
// reach the current verdict at once, not after the next save.
func TestAReviewOfAnOldVersionReachesTheCurrentVerdict(t *testing.T) {
	ctx := context.Background()
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	q := pe.bundles.DB.Queries()
	b, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	if _, err := pe.reviews.StartRun(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "with the order ID", "with the order ID and the time", 1))
	if ran, err := pe.reviews.RunNext(ctx); !ran || err != nil {
		t.Fatalf("run the job: ran %v, err %v", ran, err)
	}
	_, v := pe.current(t, "pay")
	if v.VersionNumber != 2 || v.AiVersionNumber == nil || *v.AiVersionNumber != 1 || v.Must < 2 {
		t.Errorf("verdict of v%d with AI version %v and %d MUST, want v2 with the findings of the review of v1", v.VersionNumber, v.AiVersionNumber, v.Must)
	}
}
