package version

import (
	"testing"

	"github.com/alternayte/speccy/internal/http/api"
)

func TestLineDiff_AppendAtEnd(t *testing.T) {
	got := LineDiff("None.", "None.\n\nMore.\n")
	want := []api.LineOp{{Op: api.Equal, Text: "None.\n"}, {Op: api.Insert, Text: "\nMore.\n"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("op %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// REQ-006: sections pair by heading path, so an edit in one section leaves the others unchanged.
func TestDiffSections(t *testing.T) {
	from := []byte("# A\n\nOne.\n\n## B\n\nTwo.\n\n## C\n\nGone.\n")
	to := []byte("# A\n\nOne.\n\n## B\n\nTwo, edited.\n\n## D\n\nNew.\n")
	got := map[string]api.ChangeStatus{}
	for _, s := range DiffSections(from, to) {
		key := ""
		for _, p := range s.HeadingPath {
			key += "/" + p
		}
		got[key] = s.Status
	}
	want := map[string]api.ChangeStatus{"/A": api.Unchanged, "/A/B": api.Modified, "/A/C": api.Removed, "/A/D": api.Added}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("section %s = %s, want %s", k, got[k], v)
		}
	}
}
