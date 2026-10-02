package review

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
)

// PromptConflicts is the prompt that finds checks whose pass conditions pull against each
// other (#107).
const PromptConflicts = "conflicts-v1"

var conflictsSchema = []byte(`{"type":"object","additionalProperties":false,"required":["conflicts"],"properties":{"conflicts":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["checks","reason"],"properties":{"checks":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"}}}}}}`)

// FindCheckConflicts asks the reviewer model, in one call, for the rubric checks of a profile
// whose pass conditions cannot both hold: one check that asks for two things that exclude each
// other, or two checks of which one asks for what the other forbids. An author who fixes a
// finding of such a check gets the opposite finding, so the review of the doc never converges.
// The cause is in the profile, and the maintainer of the profile fixes it.
func (a *API) FindCheckConflicts(ctx context.Context, req api.FindCheckConflictsRequestObject) (api.FindCheckConflictsResponseObject, error) {
	known := map[string]bool{}
	var p strings.Builder
	name := "a spec doc"
	if req.Body.Name != nil && strings.TrimSpace(*req.Body.Name) != "" {
		name = "a " + strings.TrimSpace(*req.Body.Name)
	}
	fmt.Fprintf(&p, "These are the checks that a reviewer applies to %s. Each check has a question and a pass condition, and some name the one section they read.\n", name)
	p.WriteString("Find the conflicts: a check whose own pass condition asks for two things that cannot both hold, and two or more checks where a doc that passes one must fail another. ")
	p.WriteString("Typical conflicts: one check wants a section to stay short or high level and another wants that section to name every detail; one check allows a section to say \"not applicable\" and another fails a section that gives no content.\n")
	p.WriteString("Do not report checks that only overlap, or that ask for different things about the same section. Report a conflict only when an author who satisfies one check then fails the other. ")
	p.WriteString("For each conflict, give the slugs of the checks in \"checks\", and one sentence in \"reason\" that names what each check asks and why both cannot hold. Give an empty list when there is no conflict.\n\n")
	var list strings.Builder
	for _, c := range req.Body.Checks {
		known[c.Slug] = true
		fmt.Fprintf(&list, "- slug: %s\n  question: %s\n  pass when: %s\n", c.Slug, c.Question, c.PassWhen)
		if c.Section != nil && *c.Section != "" {
			fmt.Fprintf(&list, "  section: %s\n", *c.Section)
		}
	}
	p.WriteString(data("Checks", strings.TrimRight(list.String(), "\n")))
	res, err := a.Service.Gateway.Call(ctx, model.Call{Role: model.RoleReviewer, PromptVersion: PromptConflicts, System: systemPrompt,
		Prompt: p.String(), Schema: conflictsSchema, MaxTokens: 4000})
	if err != nil {
		if _, ok := kernel.AsError(err); ok {
			return nil, err
		}
		return nil, kernel.Invalid("conflicts_failed", "The reviewer model did not answer: %s.", strings.TrimRight(err.Error(), "."))
	}
	var out struct {
		Conflicts []struct {
			Checks []string `json:"checks"`
			Reason string   `json:"reason"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		return nil, err
	}
	resp := api.FindCheckConflicts200JSONResponse{}
	resp.Conflicts = []struct {
		Checks []string `json:"checks"`
		Reason string   `json:"reason"`
	}{}
	for _, c := range out.Conflicts {
		// A slug that the profile does not have is the model's invention.
		slugs := slices.DeleteFunc(slices.Clone(c.Checks), func(s string) bool { return !known[s] })
		if len(slugs) == 0 || strings.TrimSpace(c.Reason) == "" {
			continue
		}
		resp.Conflicts = append(resp.Conflicts, struct {
			Checks []string `json:"checks"`
			Reason string   `json:"reason"`
		}{Checks: slugs, Reason: strings.TrimSpace(c.Reason)})
	}
	return resp, nil
}
