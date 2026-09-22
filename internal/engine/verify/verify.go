// Package verify holds the deterministic part of the verification gate: it finds a target in
// a file by its anchor quote, turns the verified evidence into one outcome for each trace ID,
// and computes the run verdict. Nothing here calls a model, and nothing here does I/O.
package verify

import (
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/kernel"
)

// Outcome is the verification result of one trace ID.
type Outcome string

const (
	// Implemented: a code target holds, a test target holds, and the judge affirmed.
	Implemented Outcome = "implemented"
	// Untested: a code target holds and the judge affirmed, with no test target.
	Untested Outcome = "untested"
	// Unproven: a code target holds, and the judge neither affirmed the requirement nor
	// found a contradiction.
	Unproven Outcome = "unproven"
	// Missing: no target holds.
	Missing Outcome = "missing"
	// Breached: the judge found the cited code contradicts the requirement.
	Breached Outcome = "breached"
)

// Kind says whether a target points at code or at a test.
type Kind string

const (
	Code Kind = "code"
	Test Kind = "test"
)

// Provenance says where a target came from. A trace ID has one provenance: a builder claim
// replaces the derived set, and Speccy never merges the two.
type Provenance string

const (
	// FromClaim is a target a builder stated.
	FromClaim Provenance = "claim"
	// FromLiteral is a target Speccy found by a literal occurrence of the trace ID.
	FromLiteral Provenance = "literal"
	// FromMapper is a target the AI mapper proposed, with a quote Speccy checked.
	FromMapper Provenance = "mapper"
)

// Target is one place a trace ID is implemented or tested.
type Target struct {
	Kind Kind `json:"kind"`
	// Path is the file, relative to the repo root.
	Path string `json:"path"`
	// Quote is the verbatim anchor. It must match the file exactly once.
	Quote string `json:"quote"`
	// Offset is the byte offset Speccy found the quote at, so the UI can link to the line.
	Offset int `json:"offset"`
	// Line is the 1-based line of Offset.
	Line int `json:"line"`
	// Holds says the quote matched exactly once at the run's SHA.
	Holds bool `json:"holds"`
	// Fault says why a target does not hold.
	Fault      string     `json:"fault,omitempty"`
	Provenance Provenance `json:"provenance"`
}

// Fault values a target can carry.
const (
	FaultNoFile    = "Speccy did not read that file at this commit."
	FaultNotFound  = "The anchor quote is not in that file."
	FaultNotUnique = "The anchor quote is in that file more than once, so it names no one place."
	FaultEmpty     = "The target names no anchor quote."
)

// Check locates a target's quote in a file. content is the file at the run's SHA; ok is
// false when Speccy did not read the file.
func Check(t Target, content []byte, ok bool) Target {
	switch {
	case strings.TrimSpace(t.Quote) == "":
		t.Holds, t.Fault = false, FaultEmpty
		return t
	case !ok:
		t.Holds, t.Fault = false, FaultNoFile
		return t
	}
	first := strings.Index(string(content), t.Quote)
	if first < 0 {
		t.Holds, t.Fault = false, FaultNotFound
		return t
	}
	if strings.Contains(string(content[first+len(t.Quote):]), t.Quote) {
		t.Holds, t.Fault = false, FaultNotUnique
		return t
	}
	t.Holds, t.Fault = true, ""
	t.Offset = first
	t.Line = 1 + strings.Count(string(content[:first]), "\n")
	return t
}

// Judgement is what a judge said about a requirement and the code cited for it.
type Judgement string

const (
	// Affirmed: the judge found the requirement's response in the cited code.
	Affirmed Judgement = "affirmed"
	// Contradicted: the judge found the cited code contradicts the requirement.
	Contradicted Judgement = "contradicted"
	// Silent: the judge neither affirmed nor contradicted.
	Silent Judgement = "silent"
)

// Item is one trace ID with its evidence, ready for an outcome.
type Item struct {
	ID string
	// Level is the trace ID's own level: MUST when its definition uses the keyword.
	Level kernel.Level
	// Parsed says the requirement grammar parsed the definition. A breach on a definition
	// that does not parse is capped at SHOULD, because the doc never promised a shape.
	Parsed bool
	// Targets are every target Speccy checked.
	Targets []Target
	// Provenance is where the targets came from.
	Provenance Provenance
	// Judgement is the first judge's verdict, and Confirmed says a second, independent judge
	// agreed with a contradiction.
	Judgement Judgement
	Confirmed bool
	// Waived says an approved verification waiver covers this trace ID.
	Waived bool
}

// Result is one trace ID's outcome.
type Result struct {
	ID      string
	Outcome Outcome
	// Level is the level the outcome reports at.
	Level kernel.Level
	// Blocks says the outcome opens a blocking thread.
	Blocks bool
	// Waived says a waiver covers it, so it does not block.
	Waived bool
	// Note explains an outcome that a reader would otherwise misread.
	Note string
}

// PossibleBreach is the finding name when the two judges disagree about a contradiction.
const PossibleBreach = "possible breach"

// Decide returns the outcome of one trace ID. Every input is either a deterministic target
// check or a judge's verdict that quotes both sources.
func Decide(it Item) Result {
	r := Result{ID: it.ID, Level: it.Level, Waived: it.Waived}
	code, test := held(it.Targets, Code), held(it.Targets, Test)

	switch {
	case !code && !test:
		r.Outcome = Missing
	case it.Judgement == Contradicted && it.Confirmed:
		r.Outcome = Breached
		if !it.Parsed && r.Level == kernel.Must {
			// The doc never promised a testable shape, so a breach on it is not a MUST.
			r.Level = kernel.Should
			r.Note = "The definition does not parse as a requirement, so this breach reports at SHOULD."
		}
	case it.Judgement == Contradicted && !it.Confirmed:
		r.Outcome = Unproven
		r.Level = kernel.Should
		r.Note = PossibleBreach + ": the second judge did not agree, so this does not block."
	case it.Judgement != Affirmed:
		r.Outcome = Unproven
		r.Level = kernel.Should
	case test:
		r.Outcome = Implemented
	default:
		r.Outcome = Untested
	}
	r.Blocks = !r.Waived && r.Level == kernel.Must && (r.Outcome == Missing || r.Outcome == Breached)
	return r
}

func held(ts []Target, k Kind) bool {
	for _, t := range ts {
		if t.Kind == k && t.Holds {
			return true
		}
	}
	return false
}

// Verdict is a verification run's verdict.
type Verdict string

const (
	Verified    Verdict = "verified"
	NotVerified Verdict = "not_verified"
)

// Counts is the tally of a run, for the verdict, the report and Insights.
type Counts struct {
	Implemented int `json:"implemented"`
	Untested    int `json:"untested"`
	Unproven    int `json:"unproven"`
	Missing     int `json:"missing"`
	Breached    int `json:"breached"`
	Waived      int `json:"waived"`
	// Blocking is the number of outcomes that open a blocking thread.
	Blocking int `json:"blocking"`
	// Skipped is the number of trace IDs outside the profile's verify prefixes.
	Skipped int `json:"skipped"`
}

// Tally counts the results.
func Tally(rs []Result) Counts {
	var c Counts
	for _, r := range rs {
		switch r.Outcome {
		case Implemented:
			c.Implemented++
		case Untested:
			c.Untested++
		case Unproven:
			c.Unproven++
		case Missing:
			c.Missing++
		case Breached:
			c.Breached++
		}
		if r.Waived {
			c.Waived++
		}
		if r.Blocks {
			c.Blocking++
		}
	}
	return c
}

// Decide the run verdict. It is a pure function of the counts: a run with a blocking outcome
// is not verified, and every other run is.
func (c Counts) Verdict() Verdict {
	if c.Blocking > 0 {
		return NotVerified
	}
	return Verified
}

// BreachRate is the share of verified trace IDs that came back breached or missing. It is
// zero when the run verified nothing.
func (c Counts) BreachRate() float64 {
	total := c.Implemented + c.Untested + c.Unproven + c.Missing + c.Breached
	if total == 0 {
		return 0
	}
	return float64(c.Missing+c.Breached) / float64(total)
}

// Verifiable reports whether a trace ID is one the gate verifies: its prefix is in the
// profile's verify list. A decision ID or a goal ID names no code.
func Verifiable(id string, prefixes []string) bool {
	p, _, ok := strings.Cut(id, "-")
	if !ok {
		return false
	}
	for _, want := range prefixes {
		if strings.EqualFold(p, want) {
			return true
		}
	}
	return false
}

// Rank orders candidate files for the mapper: a file the commit range changed comes first,
// then the rest, each group by path, so one repo always gives one order.
func Rank(files []string, changed map[string]bool) []string {
	out := append([]string(nil), files...)
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := changed[out[i]], changed[out[j]]
		if ci != cj {
			return ci
		}
		return out[i] < out[j]
	})
	return out
}
