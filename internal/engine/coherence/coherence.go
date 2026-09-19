// Package coherence holds the deterministic checks between linked docs (SDD §6.6): trace
// coverage (REQ-053) and restatement (REQ-055). It is pure: no I/O and no model calls.
package coherence

import (
	"strings"
	"unicode"
)

// ShingleWords and RestateThreshold are REQ-055: 8-word shingles, more than 50% overlap.
const (
	ShingleWords     = 8
	RestateThreshold = 0.5
)

// Restatement is a downstream paragraph that repeats an upstream paragraph.
type Restatement struct {
	Down    int     // index into the downstream paragraphs
	Up      int     // index into the upstream paragraphs
	Overlap float64 // the share of the downstream paragraph's shingles found in the upstream one
}

// Restated compares each downstream paragraph with each upstream paragraph. A downstream
// paragraph restates an upstream one when more than RestateThreshold of its 8-word
// shingles are in the upstream paragraph. Each downstream paragraph gives at most one
// result: the upstream paragraph with the largest overlap. A paragraph shorter than 8
// words has no shingles and never matches.
func Restated(down, up []string) []Restatement {
	ups := make([]map[string]bool, len(up))
	for i, u := range up {
		ups[i] = map[string]bool{}
		for _, s := range Shingles(u) {
			ups[i][s] = true
		}
	}
	var out []Restatement
	for di, d := range down {
		sh := unique(Shingles(d))
		if len(sh) == 0 {
			continue
		}
		best, bestUp := 0.0, -1
		for ui, set := range ups {
			n := 0
			for _, s := range sh {
				if set[s] {
					n++
				}
			}
			if o := float64(n) / float64(len(sh)); o > best {
				best, bestUp = o, ui
			}
		}
		if best > RestateThreshold {
			out = append(out, Restatement{Down: di, Up: bestUp, Overlap: best})
		}
	}
	return out
}

// Shingles returns the 8-word shingles of text: lower-case words, with punctuation removed.
func Shingles(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '\''
	})
	if len(words) < ShingleWords {
		return nil
	}
	out := make([]string, 0, len(words)-ShingleWords+1)
	for i := 0; i+ShingleWords <= len(words); i++ {
		out = append(out, strings.Join(words[i:i+ShingleWords], " "))
	}
	return out
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0:0]
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Ack is a frontmatter trace acknowledgement (SDD §10.2).
type Ack struct {
	Status string // covered_by | out_of_scope
	Target string // required for covered_by
	Reason string
}

// Valid reports whether an acknowledgement is complete: a known status, a reason, and a
// target for covered_by.
func (a Ack) Valid() bool {
	switch a.Status {
	case "covered_by":
		return strings.TrimSpace(a.Target) != "" && strings.TrimSpace(a.Reason) != ""
	case "out_of_scope":
		return strings.TrimSpace(a.Reason) != ""
	}
	return false
}

// Cover is the coverage of one upstream ID (REQ-053).
type Cover struct {
	ID    string
	State string // referenced | acknowledged | gap
}

// Coverage returns, for each upstream ID in order, whether the downstream doc references it,
// acknowledges it with a valid acknowledgement, or leaves a gap.
func Coverage(upstreamIDs []string, referenced map[string]bool, acks map[string]Ack) []Cover {
	out := make([]Cover, len(upstreamIDs))
	for i, id := range upstreamIDs {
		state := "gap"
		if referenced[id] {
			state = "referenced"
		} else if a, ok := acks[id]; ok && a.Valid() {
			state = "acknowledged"
		}
		out[i] = Cover{ID: id, State: state}
	}
	return out
}
