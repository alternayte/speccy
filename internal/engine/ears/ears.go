// Package ears parses a requirement into a trigger and a response, in the EARS shapes. The
// parse is deterministic and free of I/O. A doc that does not use the shapes still reviews:
// the profile decides whether the grammar check runs at all.
package ears

import (
	"regexp"
	"strings"
)

// Shape is one EARS requirement shape.
type Shape string

const (
	// Ubiquitous holds always: "The parser MUST reject an unknown field."
	Ubiquitous Shape = "ubiquitous"
	// EventDriven starts at an event: "When a run finishes, Speccy MUST store the verdict."
	EventDriven Shape = "event-driven"
	// StateDriven holds while a state lasts: "While a run is queued, the page MUST show it."
	StateDriven Shape = "state-driven"
	// OptionalFeature holds where a feature exists: "Where Postgres is the store, ..."
	OptionalFeature Shape = "optional-feature"
	// Unwanted answers an unwanted condition: "If the token is refused, Speccy MUST ..."
	Unwanted Shape = "unwanted"
)

// Requirement is a parsed definition.
type Requirement struct {
	Shape Shape
	// Trigger is the condition, without its keyword. It is empty for a ubiquitous
	// requirement.
	Trigger string
	// Actor is who or what acts.
	Actor string
	// Keyword is the normative word the requirement used, such as MUST or shall.
	Keyword string
	// Response is what the actor does.
	Response string
}

// keywordRe finds the normative word that splits the actor from the response. The RFC 2119
// words come first, because a doc that uses them means them.
var keywordRe = regexp.MustCompile(`(?:^|\s)(MUST NOT|MUST|SHALL NOT|SHALL|SHOULD NOT|SHOULD|MAY|must not|must|shall not|shall|should not|should|may)(?:\s|$)`)

// leadRe strips a trace ID and its colon from the front of a definition.
var leadRe = regexp.MustCompile(`^\**\s*[A-Z]{2,6}-\d+\**\s*[:.\-—]?\s*`)

// triggers maps a leading keyword to its shape.
var triggers = []struct {
	word  string
	shape Shape
}{
	{"when", EventDriven},
	{"while", StateDriven},
	{"where", OptionalFeature},
	{"if", Unwanted},
}

// Parse returns the requirement a definition states. ok is false when the text does not fit
// a shape, and the caller then uses the raw definition.
func Parse(text string) (Requirement, bool) {
	s := strings.TrimSpace(leadRe.ReplaceAllString(strings.TrimSpace(text), ""))
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return Requirement{}, false
	}
	// One sentence only. A definition that runs on says more than one thing, and a check
	// that guesses which one is the requirement is a check nobody can predict.
	if body, _, found := strings.Cut(s, ". "); found {
		s = body
	}
	s = strings.TrimRight(s, ".")

	r := Requirement{Shape: Ubiquitous}
	rest := s
	for _, t := range triggers {
		lead := t.word + " "
		if !strings.HasPrefix(strings.ToLower(rest), lead) {
			continue
		}
		trigger, tail, found := cutTrigger(rest[len(lead):])
		if !found {
			return Requirement{}, false
		}
		r.Shape, r.Trigger, rest = t.shape, strings.TrimSpace(trigger), strings.TrimSpace(tail)
		break
	}
	// "If X, then Y" and "While X, when Y" both leave a word in front of the actor.
	if lower := strings.ToLower(rest); strings.HasPrefix(lower, "then ") {
		rest = strings.TrimSpace(rest[len("then "):])
	}
	m := keywordRe.FindStringSubmatchIndex(rest)
	if m == nil {
		return Requirement{}, false
	}
	r.Actor = strings.TrimSpace(rest[:m[2]])
	r.Keyword = rest[m[2]:m[3]]
	r.Response = strings.TrimSpace(rest[m[3]:])
	if r.Actor == "" || r.Response == "" {
		return Requirement{}, false
	}
	if r.Shape != Ubiquitous && r.Trigger == "" {
		return Requirement{}, false
	}
	return r, true
}

// cutTrigger splits the trigger from the rest at the comma that ends it. A trigger with no
// comma does not parse, because the boundary is then a guess.
func cutTrigger(s string) (trigger, rest string, ok bool) {
	depth := 0
	for i, c := range s {
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

// Fix is the message that tells an author how to write a definition the grammar accepts.
const Fix = "Write one sentence in an EARS shape: \"The <actor> MUST <response>\", or lead with When, While, Where or If and a comma."
