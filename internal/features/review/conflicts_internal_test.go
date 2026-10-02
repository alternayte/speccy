package review

import (
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/features/profile"
)

// fixtureChecks are the rubric checks of testdata/conflicts-profile.yaml.
func fixtureChecks(t *testing.T) []conflictCheck {
	t.Helper()
	l, err := profile.ParseFile("testdata/conflicts-profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out []conflictCheck
	for _, c := range l.Profile.Checks {
		if c.Stage == StageRubric {
			out = append(out, conflictCheck{Slug: c.Slug, Question: c.Question, PassWhen: c.PassWhen, Section: c.Section})
		}
	}
	return out
}

// #114: the pairs of a check that accepts an "n/a" section and a check that fails one come
// from what the model says of each check, for the checks that read the same section. A check
// that pulls against itself is a conflict of one. A pair that the model also lists is not
// there twice, and a slug that the profile does not have is left out.
func TestConflicts_PairsFromEachCheck(t *testing.T) {
	answer := `{"checks":[
		{"slug":"sdd.solution","demands":["short","names each flow"],"all_can_hold":false,"reason":"Short and complete pull against each other.","accepts_empty":false,"needs_content":false},
		{"slug":"sdd.migration","demands":[],"all_can_hold":true,"reason":"","accepts_empty":true,"needs_content":false},
		{"slug":"sdd.monitoring","demands":[],"all_can_hold":true,"reason":"","accepts_empty":true,"needs_content":false},
		{"slug":"sdd.security","demands":[],"all_can_hold":true,"reason":"","accepts_empty":false,"needs_content":true},
		{"slug":"sdd.altitude","demands":[],"all_can_hold":true,"reason":"","accepts_empty":false,"needs_content":true},
		{"slug":"sdd.invented","demands":[],"all_can_hold":false,"reason":"Not in the profile.","accepts_empty":true,"needs_content":true}],
		"pairs":[
		{"checks":["sdd.altitude","sdd.monitoring"],"analysis":"","both_can_hold":false,"reason":"The same pair again."},
		{"checks":["sdd.limits","sdd.security"],"analysis":"","both_can_hold":true,"reason":"They overlap."},
		{"checks":["sdd.decisions.ids","sdd.decisions.alternatives"],"analysis":"","both_can_hold":false,"reason":"One forbids what the other asks."}]}`
	found, err := conflictsOf([]byte(answer), fixtureChecks(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range found {
		got = append(got, strings.Join(c.Checks, "+"))
	}
	// sdd.security names the Security section, which no check with an "n/a" answer reads.
	want := "sdd.solution sdd.migration+sdd.altitude sdd.monitoring+sdd.altitude sdd.decisions.ids+sdd.decisions.alternatives"
	if strings.Join(got, " ") != want {
		t.Errorf("conflicts %q, want %q", strings.Join(got, " "), want)
	}
}

// #120: a check with no section key that asks about one section judges that section alone, so
// an "n/a" in another section does not fail it. A check that accepts "n/a" does not also fail
// a section with no content. A pair of the model about two different sections is no conflict.
func TestConflicts_ChecksOfDifferentSections(t *testing.T) {
	checks := []conflictCheck{
		{Slug: "sdd.structure"}, {Slug: "sdd.introduction"}, {Slug: "sdd.monitoring"},
		{Slug: "sdd.security", Section: "Security"}, {Slug: "sdd.altitude"},
	}
	answer := `{"checks":[
		{"slug":"sdd.structure","demands":[],"all_can_hold":true,"reason":"","section":"","accepts_empty":true,"needs_content":false},
		{"slug":"sdd.introduction","demands":[],"all_can_hold":true,"reason":"","section":"Introduction","accepts_empty":false,"needs_content":true},
		{"slug":"sdd.monitoring","demands":[],"all_can_hold":true,"reason":"","section":"The Monitoring section","accepts_empty":true,"needs_content":true},
		{"slug":"sdd.security","demands":[],"all_can_hold":true,"reason":"","section":"Security","accepts_empty":true,"needs_content":false},
		{"slug":"sdd.altitude","demands":[],"all_can_hold":true,"reason":"","section":"","accepts_empty":false,"needs_content":true}],
		"pairs":[
		{"checks":["sdd.introduction","sdd.security"],"analysis":"","both_can_hold":false,"reason":"They read different sections."}]}`
	found, err := conflictsOf([]byte(answer), checks)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range found {
		got = append(got, strings.Join(c.Checks, "+"))
	}
	want := "sdd.structure+sdd.altitude sdd.monitoring+sdd.altitude sdd.security+sdd.altitude"
	if strings.Join(got, " ") != want {
		t.Errorf("conflicts %q, want %q", strings.Join(got, " "), want)
	}
}
