package review

import (
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
)

// REQ-052: IDs for unnumbered requirements and decisions continue the doc's numbering, and
// only the accepted ones are inserted.
func TestSuggestIDs(t *testing.T) {
	src := []byte("---\ntype: prd\n---\n# Doc\n\n## Requirements\n\n- **REQ-002:** Has an ID.\n- Refund within 5 days.\n  - A nested item.\n- The page shows the refund.\n\n" +
		"## Non-functional requirements\n\n- Pages load in 1 second.\n\n## Notes\n\n- Not a requirement.\n")
	got := suggestIDs("PRD.md", src, section.Parse(src), []string{"REQ", "NFR"})
	want := []string{"REQ-003", "REQ-004", "NFR-001"}
	if len(got) != len(want) {
		t.Fatalf("suggestions %+v, want %v", got, want)
	}
	for i, g := range got {
		if g.ID != want[i] {
			t.Errorf("suggestion %d: %s for %q, want %s", i, g.ID, g.Text, want[i])
		}
	}
	out := string(applyIDs(src, []idSuggestion{got[0], got[2]}))
	for _, line := range []string{"- **REQ-003:** Refund within 5 days.", "- The page shows the refund.", "- **NFR-001:** Pages load in 1 second."} {
		if !strings.Contains(out, line) {
			t.Errorf("the doc after applying lacks %q:\n%s", line, out)
		}
	}
}
