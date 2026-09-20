package kernel

import "strings"

// Size is the scale one main doc covers (REQ-134). It selects the checks that apply: a
// one-feature design does not answer the questions a whole application must answer.
type Size string

const (
	Feature    Size = "feature"
	App        Size = "app"
	Initiative Size = "initiative"
)

// Sizes lists the sizes from the smallest to the largest.
var Sizes = []Size{Feature, App, Initiative}

// ParseSize reads a size. ok is false for any other text.
func ParseSize(s string) (Size, bool) {
	switch Size(strings.ToLower(strings.TrimSpace(s))) {
	case Feature:
		return Feature, true
	case App:
		return App, true
	case Initiative:
		return Initiative, true
	}
	return "", false
}

// rank orders the sizes. An unknown size ranks as feature, the smallest.
func (s Size) rank() int {
	for i, x := range Sizes {
		if x == s {
			return i
		}
	}
	return 0
}

// AtLeast reports whether s is min or larger. A doc at size app is at least feature.
func (s Size) AtLeast(min Size) bool { return s.rank() >= min.rank() }
