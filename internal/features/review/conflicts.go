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
const PromptConflicts = "conflicts-v2"

// conflictsSchema makes the model judge each check, and then each pair, one by one: a model that
// answers with a list of conflicts alone finds one conflict and stops (#114).
var conflictsSchema = []byte(`{"type":"object","additionalProperties":false,"required":["checks","pairs"],"properties":{` +
	`"checks":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["slug","demands","all_can_hold","reason","accepts_empty","needs_content"],"properties":{"slug":{"type":"string"},"demands":{"type":"array","items":{"type":"string"}},"all_can_hold":{"type":"boolean"},"reason":{"type":"string"},"accepts_empty":{"type":"boolean"},"needs_content":{"type":"boolean"}}}},` +
	`"pairs":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["checks","analysis","both_can_hold","reason"],"properties":{"checks":{"type":"array","items":{"type":"string"}},"analysis":{"type":"string"},"both_can_hold":{"type":"boolean"},"reason":{"type":"string"}}}}}}`)

// FindCheckConflicts asks the reviewer model, in one call, for the rubric checks of a profile
// whose pass conditions cannot both hold: one check that asks for two things that exclude each
// other, or two checks of which one asks for what the other forbids. An author who fixes a
// finding of such a check gets the opposite finding, so the review of the doc never converges.
// The cause is in the profile, and the maintainer of the profile fixes it.
func (a *API) FindCheckConflicts(ctx context.Context, req api.FindCheckConflictsRequestObject) (api.FindCheckConflictsResponseObject, error) {
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	checks := make([]conflictCheck, len(req.Body.Checks))
	for i, c := range req.Body.Checks {
		checks[i] = conflictCheck{Slug: c.Slug, Question: c.Question, PassWhen: c.PassWhen}
		if c.Section != nil {
			checks[i].Section = *c.Section
		}
	}
	res, err := a.Service.Gateway.Call(ctx, model.Call{Role: model.RoleReviewer, PromptVersion: PromptConflicts, System: systemPrompt,
		Prompt: conflictsPrompt(name, checks), Schema: conflictsSchema, MaxTokens: 12000})
	if err != nil {
		if _, ok := kernel.AsError(err); ok {
			return nil, err
		}
		return nil, kernel.Invalid("conflicts_failed", "The reviewer model did not answer: %s.", strings.TrimRight(err.Error(), "."))
	}
	resp := api.FindCheckConflicts200JSONResponse{}
	resp.Conflicts = []struct {
		Checks []string `json:"checks"`
		Reason string   `json:"reason"`
	}{}
	found, err := conflictsOf(res.JSON, checks)
	if err != nil {
		return nil, err
	}
	for _, c := range found {
		resp.Conflicts = append(resp.Conflicts, struct {
			Checks []string `json:"checks"`
			Reason string   `json:"reason"`
		}{Checks: c.Checks, Reason: c.Reason})
	}
	return resp, nil
}

// conflictCheck is one rubric check as the conflicts prompt lists it.
type conflictCheck struct {
	Slug, Question, PassWhen, Section string
}

// checkConflict is one check, or one group of checks, whose pass conditions cannot all hold.
type checkConflict struct {
	Checks []string
	Reason string
}

// conflictsOf reads the answer of the model about checks: a check whose own demands cannot
// all hold, then each pair that cannot both hold. The pairs of a check that accepts a section
// with no content and a check that fails one are made here, from what the model says of each
// check: a model that lists those pairs itself leaves some out (#114).
func conflictsOf(answer json.RawMessage, checks []conflictCheck) ([]checkConflict, error) {
	var out struct {
		Checks []struct {
			Slug         string `json:"slug"`
			AllCanHold   bool   `json:"all_can_hold"`
			Reason       string `json:"reason"`
			AcceptsEmpty bool   `json:"accepts_empty"`
			NeedsContent bool   `json:"needs_content"`
		} `json:"checks"`
		Pairs []struct {
			Checks      []string `json:"checks"`
			BothCanHold bool     `json:"both_can_hold"`
			Reason      string   `json:"reason"`
		} `json:"pairs"`
	}
	if err := json.Unmarshal(answer, &out); err != nil {
		return nil, err
	}
	section := map[string]string{}
	for _, c := range checks {
		section[c.Slug] = normSection(c.Section)
	}
	var found []checkConflict
	paired := map[[2]string]bool{}
	add := func(slugs []string, reason string) {
		// A slug that the profile does not have is the model's invention.
		slugs = slices.DeleteFunc(slices.Clone(slugs), func(s string) bool { _, ok := section[s]; return !ok })
		if len(slugs) == 0 || strings.TrimSpace(reason) == "" {
			return
		}
		if len(slugs) == 2 {
			key := [2]string{min(slugs[0], slugs[1]), max(slugs[0], slugs[1])}
			if paired[key] {
				return
			}
			paired[key] = true
		}
		found = append(found, checkConflict{Checks: slugs, Reason: strings.TrimSpace(reason)})
	}
	for _, c := range out.Checks {
		if !c.AllCanHold {
			add([]string{c.Slug}, c.Reason)
		}
	}
	for _, a := range out.Checks {
		for _, b := range out.Checks {
			sa, known := section[a.Slug]
			sb, knownB := section[b.Slug]
			if !a.AcceptsEmpty || !b.NeedsContent || a.Slug == b.Slug || !known || !knownB {
				continue
			}
			// The two read the same text when they name the same section, or when the check
			// that needs content reads every section.
			if sb == "" || sa == sb {
				add([]string{a.Slug, b.Slug}, fmt.Sprintf("%s accepts a section that says only \"n/a\" or \"none\", and %s fails a section that gives no content, so an author of such a section cannot pass both.", a.Slug, b.Slug))
			}
		}
	}
	for _, p := range out.Pairs {
		if !p.BothCanHold && len(p.Checks) > 1 {
			add(p.Checks, p.Reason)
		}
	}
	return found, nil
}

// normSection names a section of a check without the case and the outer spaces.
func normSection(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// conflictsPrompt lists the rubric checks of the profile with the name, and asks for the ones
// that pull against each other.
func conflictsPrompt(name string, checks []conflictCheck) string {
	var p strings.Builder
	doc := "a spec doc"
	if strings.TrimSpace(name) != "" {
		doc = "a " + strings.TrimSpace(name)
	}
	fmt.Fprintf(&p, "These are the checks that a reviewer applies to %s. Each check has a question and a pass condition. A check with a section reads that one section. A check with no section reads the whole doc, and a pass condition about \"each section\" holds for every section of the doc.\n", doc)
	p.WriteString("A conflict makes a review that never ends: the author fixes a finding of one check and gets the opposite finding. Find the conflicts in two steps.\n\n")
	p.WriteString("Step 1, in \"checks\": give one entry for every check, in the order of the list.\n")
	p.WriteString("- \"demands\": split the pass condition into the separate things it asks of the doc, one in each item. Alternatives joined by \"or\" are one demand: the doc meets it with any one of them.\n")
	p.WriteString("- \"all_can_hold\": think of a large doc, with many components and flows. Give false when its author cannot meet every demand at once, and true in every other case. ")
	p.WriteString("The usual conflict: one demand limits the text (short, high level, no detail) and another demand asks it to be complete (name each one, leave nothing out, no later section adds something it does not name). More names make the text longer and more detailed, so the two pull against each other.\n")
	p.WriteString("- \"reason\": for false, name the two demands and say why both cannot hold. For true, give an empty text.\n")
	p.WriteString("- \"accepts_empty\": true when the pass condition says in words that the section may hold no content: it may say \"n/a\", \"not applicable\", \"none\", or \"nothing to add\". False when the pass condition does not say so.\n")
	p.WriteString("- \"needs_content\": true only when the pass condition judges a section as a whole on whether it gives the reader something of use, such as content, a decision, a signal, or detail: it speaks of \"each section\" or \"every section\", or of the one section that the check names. A section that says only \"n/a\" fails such a check. False for a check about facts in the doc, such as each entity, each decision, each limit, or statements that contradict each other: a section with no such fact does not fail it.\n\n")
	p.WriteString("Step 2, in \"pairs\": give an entry for two checks that read the same section when no text of that section can pass both: what one check asks for is what the other forbids. Example: one check wants a section short or high level, and another wants the same section to hold every detail. ")
	p.WriteString("Do not list a pair for \"n/a\" sections here: step 1 covers those. ")
	p.WriteString("In \"analysis\", state what each check asks of the section. Set \"both_can_hold\" to true when an author can write one text that passes both checks, also when it takes more work, and also when a text can pass one and fail the other. Set it to false only when passing one check makes the other fail, and say why in one sentence in \"reason\". ")
	p.WriteString("Two checks that overlap, that ask for different things about the same section, or that read different sections, can both hold: leave them out. Most profiles have no such pair; then give an empty list.\n\n")
	var list strings.Builder
	for _, c := range checks {
		fmt.Fprintf(&list, "- slug: %s\n  question: %s\n  pass when: %s\n", c.Slug, c.Question, c.PassWhen)
		if c.Section != "" {
			fmt.Fprintf(&list, "  section: %s\n", c.Section)
		}
	}
	p.WriteString(data("Checks", strings.TrimRight(list.String(), "\n")))
	return p.String()
}
