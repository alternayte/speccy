// Package divergence classifies the reader answers to one build question (SDD §8.5).
// It is pure: no I/O and no model calls.
package divergence

import "strings"

// NotSpecified is the answer a reader gives when the bundle does not answer the question
// (REQ-043).
const NotSpecified = "NOT SPECIFIED"

// Result is the classification of one question (REQ-044).
type Result string

const (
	Agree   Result = "agree"
	Diverge Result = "diverge"
	Gap     Result = "gap"
)

// IsNotSpecified reports whether a reader's answer text means NOT SPECIFIED.
func IsNotSpecified(answer string) bool {
	a := strings.Trim(strings.TrimSpace(answer), ".")
	return a == "" || strings.EqualFold(a, NotSpecified)
}

// Classify applies the table in §8.5 step 5. answered has one entry per reader: false for
// NOT SPECIFIED. groups is the number of meaning groups among the answered readers.
func Classify(answered []bool, groups int) Result {
	n := 0
	for _, a := range answered {
		if a {
			n++
		}
	}
	switch {
	case n == 0:
		return Gap
	case n < len(answered):
		return Diverge
	case groups <= 1:
		return Agree
	default:
		return Diverge
	}
}

// Groups repairs a judge's grouping of n answers, numbered 0 to n-1: an index out of range
// or seen before is dropped, empty groups are dropped, and an answer the judge left out
// gets a group of its own. The judge cannot merge answers by omission.
func Groups(n int, raw [][]int) [][]int {
	seen := make([]bool, n)
	var out [][]int
	for _, g := range raw {
		var keep []int
		for _, i := range g {
			if i < 0 || i >= n || seen[i] {
				continue
			}
			seen[i] = true
			keep = append(keep, i)
		}
		if len(keep) > 0 {
			out = append(out, keep)
		}
	}
	for i, ok := range seen {
		if !ok {
			out = append(out, []int{i})
		}
	}
	return out
}
