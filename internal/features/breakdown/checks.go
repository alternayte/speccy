package breakdown

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/engine/ears"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/kernel"
)

// The breakdown checks (#155). A MUST finding refuses the push; a SHOULD finding informs.
const (
	CheckCoverage  = "breakdown.coverage"
	CheckOrphan    = "breakdown.orphan"
	CheckCycle     = "breakdown.cycle"
	CheckOneRepo   = "breakdown.one-repo"
	CheckTestable  = "breakdown.testable"
	CheckSize      = "breakdown.size"
	CheckReaders   = "breakdown.divergence"
	checkLevelMust = kernel.Must
)

// Finding is one failed breakdown check.
type Finding struct {
	Slug    string
	Level   kernel.Level
	Message string
	// Items are the work items the finding is about. A coverage finding names none.
	Items []string
	// TraceID is the uncovered trace ID of a coverage finding.
	TraceID string
}

// checks runs the deterministic checks, and reports the reader check that is current. traceIDs
// are the doc's trace IDs; grammar is true when the profile enables the requirement grammar
// check.
func checks(b Body, traceIDs []string, maxTraceIDs int, grammar bool) []Finding {
	var out []Finding
	covered := map[string]bool{}
	for _, it := range b.Items {
		for _, t := range it.TraceIDs {
			covered[t] = true
		}
	}
	for _, c := range b.Covered {
		covered[c.TraceID] = true
	}
	for _, t := range traceIDs {
		if !covered[t] {
			out = append(out, Finding{Slug: CheckCoverage, Level: kernel.Must, TraceID: t,
				Message: fmt.Sprintf("%s is in no work item. Add it to an item, or say why it needs none.", t)})
		}
	}
	for _, it := range b.Items {
		if len(it.TraceIDs) == 0 && len(it.HeadingPath) == 0 {
			out = append(out, Finding{Slug: CheckOrphan, Level: kernel.Must, Items: []string{it.ID},
				Message: fmt.Sprintf("%q has no trace ID and no section, so nothing in the doc says what it builds.", it.Title)})
		}
		if len(it.Repos) > 1 {
			out = append(out, Finding{Slug: CheckOneRepo, Level: kernel.Must, Items: []string{it.ID},
				Message: fmt.Sprintf("%q changes %s. Split it into one item per repo.", it.Title, strings.Join(it.Repos, " and "))})
		}
		if !testable(it, grammar) {
			msg := fmt.Sprintf("%q has no criterion a test can check: none states a MUST, a SHALL or a SHOULD.", it.Title)
			if grammar {
				msg = fmt.Sprintf("%q has no criterion a test can check: none states a trigger and a response.", it.Title)
			}
			out = append(out, Finding{Slug: CheckTestable, Level: kernel.Should, Items: []string{it.ID}, Message: msg})
		}
		if maxTraceIDs > 0 && len(it.TraceIDs) > maxTraceIDs {
			out = append(out, Finding{Slug: CheckSize, Level: kernel.Should, Items: []string{it.ID},
				Message: fmt.Sprintf("%q holds %d trace IDs. The profile's limit is %d. Split it.", it.Title, len(it.TraceIDs), maxTraceIDs)})
		}
		if r := it.Readers; r != nil && r.Diverge && r.Hash == hashItem(it) {
			out = append(out, Finding{Slug: CheckReaders, Level: kernel.Should, Items: []string{it.ID},
				Message: fmt.Sprintf("Two readers would build %q in different ways: %s", it.Title, r.Reason)})
		}
	}
	for _, cyc := range cycles(b.Items) {
		names := make([]string, len(cyc))
		for i, id := range cyc {
			names[i] = fmt.Sprintf("%q", title(b, id))
		}
		out = append(out, Finding{Slug: CheckCycle, Level: kernel.Must, Items: cyc,
			Message: fmt.Sprintf("These items depend on each other in a loop: %s. Remove one dependency.", strings.Join(names, " → "))})
	}
	return out
}

// testable reports whether an item has a criterion a test can check. With the requirement
// grammar on, only a criterion in an EARS shape counts. Otherwise a modal verb counts.
func testable(it Item, grammar bool) bool {
	for _, c := range it.Criteria {
		if grammar {
			if _, ok := ears.Parse(c.Quote); ok {
				return true
			}
			continue
		}
		for _, m := range []string{"MUST", "SHALL", "SHOULD", "must ", "shall ", "should "} {
			if strings.Contains(c.Quote, m) {
				return true
			}
		}
	}
	return false
}

// grammarOn reports whether a profile enables the requirement grammar check.
func grammarOn(overrides map[string]string) bool {
	l, ok := overrides[lint.RequirementGrammar]
	return ok && l != "" && !strings.EqualFold(l, "off")
}

// cycles finds the loops in the dependencies, each once, as the item IDs in the loop's order.
func cycles(items []Item) [][]string {
	deps := map[string][]string{}
	for _, it := range items {
		deps[it.ID] = it.DependsOn
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var out [][]string
	seen := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		color[id] = grey
		stack = append(stack, id)
		for _, d := range deps[id] {
			if _, ok := deps[d]; !ok {
				continue
			}
			switch color[d] {
			case white:
				visit(d)
			case grey:
				i := slices.Index(stack, d)
				loop := append([]string(nil), stack[i:]...)
				key := canonical(loop)
				if !seen[key] {
					seen[key] = true
					out = append(out, loop)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}
	for _, it := range items {
		if color[it.ID] == white {
			visit(it.ID)
		}
	}
	return out
}

func canonical(loop []string) string {
	s := append([]string(nil), loop...)
	slices.Sort(s)
	return strings.Join(s, ",")
}

func title(b Body, id string) string {
	if it := b.item(id); it != nil {
		return it.Title
	}
	return id
}

// mustCount counts the MUST findings, which refuse a push.
func mustCount(fs []Finding) int {
	n := 0
	for _, f := range fs {
		if f.Level == checkLevelMust {
			n++
		}
	}
	return n
}
