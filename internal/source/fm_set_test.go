package source

import "testing"

// SetKeys writes the type and the size that a review used, keeps the other keys, and puts a
// new key after the type (REQ-135).
func TestSetKeys_Order(t *testing.T) {
	out, err := SetKeys([]byte("---\ntitle: T\n---\n\n# T\n"), [][2]string{{"type", "sdd"}, {"size", "feature"}})
	if err != nil {
		t.Fatal(err)
	}
	fm, _, err := ReadFrontmatter(out)
	if err != nil || fm.Type != "sdd" || fm.Size != "feature" || fm.Title != "T" {
		t.Fatalf("type %q, size %q, title %q, err %v", fm.Type, fm.Size, fm.Title, err)
	}
	again, err := SetKeys(out, [][2]string{{"size", "app"}})
	if err != nil {
		t.Fatal(err)
	}
	fm, _, _ = ReadFrontmatter(again)
	if fm.Size != "app" || fm.Type != "sdd" {
		t.Errorf("a second write gave type %q size %q, want sdd and app", fm.Type, fm.Size)
	}
}
