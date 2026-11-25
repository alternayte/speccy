package findingkey

import "testing"

// A comment of the App or the Action keys a finding with the doc's slug on its branch. A pull
// request source adds -pr-<n> to its slugs, and its keys drop it, or the GitHub review would
// post a second comment for a finding the App already commented on.
func TestDocSlug(t *testing.T) {
	for in, want := range map[string]string{
		"docs/prd-pr-42":    "docs/prd",
		"docs-pr-42":        "docs",
		"pr-7":              ".",
		"docs/prd":          "docs/prd",
		"docs/sprint-pr":    "docs/sprint-pr",
		"docs/pr-review":    "docs/pr-review",
		"docs/my-pr-1-pr-9": "docs/my-pr-1",
	} {
		if got := DocSlug(in); got != want {
			t.Errorf("DocSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
