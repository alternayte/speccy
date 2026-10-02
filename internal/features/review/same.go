package review

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/model"
)

// PromptSame groups the shortfalls of one check that say the same thing (#119).
const PromptSame = "same-v1"

var sameSchema = []byte(`{"type":"object","additionalProperties":false,"required":["groups"],"properties":{"groups":{"type":"array","items":{"type":"array","items":{"type":"integer"}}}}}`)

// samePrompt asks which shortfalls of one check say the same thing. It is a call of its own:
// a rubric prompt that also asks this makes the model call a standing shortfall fixed (#119).
func samePrompt(c rubricCheck, falls []shortfall) string {
	var b strings.Builder
	b.WriteString("A review of a spec doc gave the shortfalls in the data part below for one check, each with a number. Some may say the same thing about the same text in other words. ")
	b.WriteString("Put the numbers in groups: two shortfalls are in one group only when a fix of one also fixes the other. Shortfalls about different text, or about different missing facts, are in different groups. ")
	b.WriteString("Put each number in exactly one group. Most lists have no two that are the same; then each number is a group of its own.\n\n")
	fmt.Fprintf(&b, "Check: %s\nPass when: %s\n\n", c.Question, c.PassWhen)
	var list strings.Builder
	for i, sf := range falls {
		fmt.Fprintf(&list, "%d: %s", i+1, sf.Reason)
		if sf.Quote != "" {
			fmt.Fprintf(&list, " Quote: %q", sf.Quote)
		}
		list.WriteString("\n")
	}
	b.WriteString(data("Shortfalls", strings.TrimRight(list.String(), "\n")))
	return b.String()
}

// oneOfEach returns the answer of a failed check with each shortfall one time (#119). The
// model gives a shortfall of the last review again in other words, and the words alone can not
// tell that two reasons say one thing, so the reviewer groups them. The answer for one list is
// cached, so a list that did not change costs no call.
func (s *Service) oneOfEach(ctx context.Context, rc *runCtx, c rubricCheck, a rubricAnswer, fingerprint string) (rubricAnswer, error) {
	if a.Result != "fail" || len(a.Shortfalls) < 2 {
		return a, nil
	}
	parts := []string{c.Question, c.PassWhen}
	for _, sf := range a.Shortfalls {
		parts = append(parts, sf.Reason, sf.Quote)
	}
	key := cacheKey{Step: "same:" + c.Slug, InputHash: hashOf(parts...), Fingerprint: fingerprint, PromptVersion: PromptSame}
	var out struct {
		Groups [][]int `json:"groups"`
	}
	ok, err := s.cached(ctx, key, &out)
	if err != nil {
		return a, err
	}
	if !ok {
		res, err := rc.call(ctx, s.Gateway, model.Call{
			Role: model.RoleReviewer, PromptVersion: PromptSame, System: systemPrompt,
			Prompt: samePrompt(c, a.Shortfalls), Schema: sameSchema, MaxTokens: 4000,
		})
		if err != nil {
			return a, err
		}
		if err := json.Unmarshal(res.JSON, &out); err != nil {
			return a, err
		}
		if err := s.putCache(ctx, key, out); err != nil {
			return a, err
		}
	}
	a.Shortfalls = merged(a.Shortfalls, out.Groups)
	return a, nil
}

// merged returns the shortfalls with one for each group of numbers: the first one of the
// group, which is the one of the last review when the group has one, with the quote and the
// question of the others where it has none. A number that is in no group, or in a second
// group, is a shortfall of its own.
func merged(falls []shortfall, groups [][]int) []shortfall {
	falls = slices.Clone(falls)
	stays := make([]bool, len(falls))
	for i := range stays {
		stays[i] = true
	}
	placed := map[int]bool{}
	for _, g := range groups {
		g = slices.Clone(g)
		slices.Sort(g)
		first := -1
		for _, n := range g {
			i := n - 1
			if i < 0 || i >= len(falls) || placed[i] {
				continue
			}
			placed[i] = true
			if first < 0 {
				first = i
				continue
			}
			stays[i] = false
			if falls[first].Quote == "" {
				falls[first].Quote = falls[i].Quote
			}
			if falls[first].Question == "" {
				falls[first].Question = falls[i].Question
			}
		}
	}
	var out []shortfall
	for i, sf := range falls {
		if stays[i] {
			out = append(out, sf)
		}
	}
	return out
}
