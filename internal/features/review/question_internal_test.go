package review

import (
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/engine/lint"
)

// #110: an answer finding states the question that the author must answer. A rubric finding
// has the question its reviewer wrote; the other findings get one from their own data. A
// reword finding has none.
func TestAnswerQuestion(t *testing.T) {
	for _, c := range []struct {
		slug, message, quote, written, evidence, want string
	}{
		{lint.Placeholder, "The text has a placeholder.", "TBD", "", `{}`, `What is the real content in place of "TBD"?`},
		{lint.RequiredHeadings, `The template requires the "Non-goals" section. The doc has none.`, "# Pay", "", `{}`, `What does this doc say under the heading "Non-goals"?`},
		{lint.UndefinedAcronym, "CDC is not defined in the doc.", "CDC", "", `{}`, "What does CDC stand for?"},
		{DivergenceGap, "The doc does not answer this build question: What is the limit?", "", "", `{"question":"What is the limit?"}`, "What is the limit?"},
		{GroundingUnverified, "No source confirms this claim.", "", "", `{"claim":"Stripe allows 100 reads a second."}`, `Which source confirms this claim, or is it an assumption: "Stripe allows 100 reads a second."?`},
		{"sdd.interfaces", "The propagate endpoint has no outputs.", "", "What does the propagate endpoint return, and which errors can it give?", `{"question":"Does each interface have inputs?"}`, "What does the propagate endpoint return, and which errors can it give?"},
		{"sdd.interfaces", "The propagate endpoint has no outputs.", "", "", `{}`, "Which fact fixes this? The propagate endpoint has no outputs."},
		{lint.PassiveVoice, "This sentence uses the passive voice.", "is retried", "", `{}`, ""},
		{lint.BrokenLink, "The link points to a.png. The bundle has assets/a.png.", "the diagram", "", `{"candidate":"assets/a.png"}`, ""},
		{lint.BrokenLink, "The link points to a.png, which is not in the bundle.", "the diagram", "", `{}`, `Which file does the link "the diagram" point to?`},
	} {
		if got := answerQuestion(c.slug, c.message, c.quote, c.written, []byte(c.evidence)); got != c.want {
			t.Errorf("%s: question %q, want %q", c.slug, got, c.want)
		}
	}
	// The reviewer is asked for the question of each shortfall.
	if p := rubricPrompt("SDD", []rubricCheck{{Slug: "x", Question: "q", PassWhen: "p"}}, "", ""); !strings.Contains(p, "Give each shortfall a question") {
		t.Error("the rubric prompt does not ask for the question of a shortfall")
	}
}
