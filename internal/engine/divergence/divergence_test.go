package divergence

import (
	"fmt"
	"testing"
)

// The §8.5 classification table.
func TestClassify(t *testing.T) {
	for _, c := range []struct {
		answered []bool
		groups   int
		want     Result
	}{
		{[]bool{false, false, false}, 0, Gap},
		{[]bool{true, false, false}, 1, Diverge},
		{[]bool{true, true, false}, 1, Diverge},
		{[]bool{true, true, true}, 1, Agree},
		{[]bool{true, true, true}, 2, Diverge},
		{[]bool{true}, 1, Agree},
		{[]bool{false}, 0, Gap},
	} {
		if got := Classify(c.answered, c.groups); got != c.want {
			t.Errorf("Classify(%v, %d) = %s, want %s", c.answered, c.groups, got, c.want)
		}
	}
}

func TestGroups(t *testing.T) {
	for _, c := range []struct {
		n    int
		raw  [][]int
		want string
	}{
		{3, [][]int{{0, 1, 2}}, "[[0 1 2]]"},
		{3, [][]int{{0, 1}}, "[[0 1] [2]]"},
		{3, [][]int{{0, 0, 7}, {}, {0, 1}, {2}}, "[[0] [1] [2]]"},
		{2, nil, "[[0] [1]]"},
	} {
		if got := fmt.Sprint(Groups(c.n, c.raw)); got != c.want {
			t.Errorf("Groups(%d, %v) = %s, want %s", c.n, c.raw, got, c.want)
		}
	}
}

func TestIsNotSpecified(t *testing.T) {
	for s, want := range map[string]bool{"NOT SPECIFIED": true, " not specified. ": true, "": true, "Three retries.": false} {
		if IsNotSpecified(s) != want {
			t.Errorf("IsNotSpecified(%q) != %v", s, want)
		}
	}
}
