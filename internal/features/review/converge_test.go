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
		"pay/SPEC.md":               groundedSDD,
		".speccy/profiles/sdd.yaml": convergeProfile,
		".speccy/profiles/sdd.md":   "# <Name>\n\n## Context\n\n## Limits\n\n## Risks\n",
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

// After a second full review the verdict says what is fixed, still open and new against the
// first. The unit is a check in a section: a shortfall that the model words another way is
// still open, not one fixed and one new.
func TestTrend_AgainstTheLastFullReview(t *testing.T) {
	ctx := context.Background()
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	pe.fake.passes = map[string]bool{"sdd.limits": true}
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.consistency": {{"reason": "Two limits differ.", "quote": "999 kilobytes"}}}
	pe.run(t, "pay")
	if _, v := pe.current(t, "pay"); v.Trend != nil {
		t.Fatalf("the first full review has the trend %+v, want none", v.Trend)
	}

	pe.fake.passes = nil
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.consistency": {{"reason": "The body cap is stated two ways.", "quote": "999 kilobytes"}}}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "in every region.", "in every region. The team owns it.", 1))
	run, _, _ := pe.run(t, "pay")
	_, v := pe.current(t, "pay")
	if v.Trend == nil || v.Trend.SinceVersion != 1 || v.Trend.New != 1 || v.Trend.Fixed != 0 || v.Trend.Open < 1 {
		t.Fatalf("trend %+v, want since v1: 1 new (sdd.limits), none fixed, and the rest still open", v.Trend)
	}
	a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews}
	res, err := a.ListFindings(ctx, api.ListFindingsRequestObject{RunId: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.(api.ListFindings200JSONResponse).Items {
		isNew := f.New != nil && *f.New
		if want := f.CheckSlug == "sdd.limits"; isNew != want {
			t.Errorf("the finding of %s is marked new = %v, want %v", f.CheckSlug, isNew, want)
		}
	}

	// The author fixes the limits: one fixed, none new.
	pe.fake.passes = map[string]bool{"sdd.limits": true}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "in every region.", "in every region. The team owns it and its limits.", 1))
	pe.run(t, "pay")
	if _, v := pe.current(t, "pay"); v.Trend == nil || v.Trend.SinceVersion != 2 || v.Trend.Fixed != 1 || v.Trend.New != 0 {
		t.Errorf("trend %+v, want since v2: 1 fixed and none new", v.Trend)
	}
}

// #107: a full review judges the shortfalls of the last review again, one by one. A shortfall
// on text that did not change stays when the model does not mention it, and leaves when the
// model says it is fixed or when its quoted text is gone. A fresh review asks about none.
func TestRubric_ShortfallsStayUntilFixed(t *testing.T) {
	ctx := context.Background()
	pe := newPipeline(t, storetest.Engines()[0], convergeFiles(), "fake-1")
	pe.fake.passes = map[string]bool{"sdd.limits": true}
	pe.fake.shortfalls = map[string][]map[string]string{"sdd.consistency": {
		{"reason": "The read limit and the retry limit do not agree.", "quote": "Stripe allows 100 read requests per second in live mode",
			"question": "Which limit holds for reads: 100 a second, or the retry limit?"},
		{"reason": "The body limit has two values.", "quote": "at 999 kilobytes for every endpoint"},
	}}
	messages := func(fs []pgdb.Finding) string {
		var out []string
		for _, f := range rubricFindings(fs, "sdd.consistency") {
			out = append(out, f.Message)
		}
		return strings.Join(out, " | ")
	}
	lastPrompt := func() string {
		for i := len(pe.fake.rubric) - 1; i >= 0; i-- {
			if strings.Contains(pe.fake.rubric[i], "slug: sdd.consistency") {
				return pe.fake.rubric[i]
			}
		}
		return ""
	}
	_, fs, _ := pe.run(t, "pay")
	if got := messages(fs); got != "The read limit and the retry limit do not agree. | The body limit has two values." {
		t.Fatalf("the first review: %s", got)
	}
	if strings.Contains(lastPrompt(), "Shortfalls of the last review") {
		t.Error("the first review asked about shortfalls of a review before it")
	}

	// The author fixes the second shortfall: its quoted text is gone. The model now lists no
	// shortfall at all, and says nothing about the first one. The first one stays.
	pe.fake.shortfalls = nil
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "at 999 kilobytes for every endpoint", "at 900 kilobytes for every endpoint", 1))
	_, fs, _ = pe.run(t, "pay")
	if got := messages(fs); got != "The read limit and the retry limit do not agree." {
		t.Errorf("after a fix of one shortfall and a silent model: %q; want the other shortfall alone, with its message", got)
	}
	prompt := lastPrompt()
	if !strings.Contains(prompt, "sdd.consistency 1: The read limit and the retry limit do not agree.") || strings.Contains(prompt, "The body limit has two values") {
		t.Errorf("the second review must ask about the shortfall whose text stands, and not about the one whose text is gone:\n%s", prompt)
	}

	if q := questionOf(t, pe, "The read limit and the retry limit do not agree."); q != "Which limit holds for reads: 100 a second, or the retry limit?" {
		t.Errorf("the question of the shortfall that stayed: %q", q)
	}

	// The model says that the first shortfall is fixed: it leaves.
	pe.fake.fixed = map[string][]int{"sdd.consistency": {1}}
	pe.write(t, "pay/SPEC.md", strings.Replace(groundedSDD, "at 999 kilobytes for every endpoint", "at 800 kilobytes for every endpoint", 1))
	_, fs, _ = pe.run(t, "pay")
	if got := messages(fs); strings.Contains(got, "The read limit and the retry limit do not agree") {
		t.Errorf("after the model said fixed: %q; the shortfall must leave", got)
	}

	// #110: the question that the reviewer wrote for the shortfall stays with the finding, also
	// in the review where the model did not mention the shortfall again.
	if q := questionOf(t, pe, "The read limit and the retry limit do not agree."); q != "" {
		t.Errorf("the finding left, and its question %q is still listed", q)
	}

	// A person asks for a fresh review: the next one asks about no shortfall of a review before it.
	pe.fake.fixed = nil
	q := pe.bundles.DB.Queries()
	b, _ := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews}
	if _, err := a.FreshQuestions(ctx, api.FreshQuestionsRequestObject{DocId: b.ID}); err != nil {
		t.Fatal(err)
	}
	before := len(pe.fake.rubric)
	pe.run(t, "pay")
	if len(pe.fake.rubric) == before {
		t.Fatal("a fresh review read the cached answers of the review before it")
	}
	if strings.Contains(lastPrompt(), "Shortfalls of the last review") {
		t.Error("a fresh review asked about the shortfalls of the review before it")
	}
}

// questionOf returns the question of the finding with a message in the current verdict of the
// pay doc, or "" when the verdict has no such finding.
func questionOf(t *testing.T, pe *pipelineEnv, message string) string {
	t.Helper()
	ctx := context.Background()
	q := pe.bundles.DB.Queries()
	b, err := q.GetSpecDocBySlug(ctx, pgdb.GetSpecDocBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := q.LatestRun(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := &review.API{DB: pe.bundles.DB, Workspace: pe.bundles.Workspace, Service: pe.reviews}
	res, err := a.ListFindings(ctx, api.ListFindingsRequestObject{RunId: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.(api.ListFindings200JSONResponse).Items {
		if f.Message == message && f.Question != nil {
			return *f.Question
		}
	}
	return ""
}
