package source

import (
	"strings"
	"testing"
)

// AddLink adds a link once, keeps the rest of the frontmatter and the body, and gives a doc
// with no frontmatter a block.
func TestAddLink(t *testing.T) {
	doc := []byte("---\ntype: sdd\ntitle: Pay\n---\n\n# Pay\n")
	got, err := AddLink(doc, "implements", "PRD - X.md")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = AddLink(got, "implements", "PRD - X.md")
	fm, _, _ := ReadFrontmatter(got)
	if fm.Type != "sdd" || fm.Title != "Pay" || len(fm.Links) != 1 || fm.Links[0].Target != "PRD - X.md" {
		t.Errorf("AddLink gave %q", got)
	}
	if !strings.HasSuffix(string(got), "\n# Pay\n") {
		t.Errorf("the body changed: %q", got)
	}
	bare, _ := AddLink([]byte("# Notes\n"), "implements", "prd")
	if fm, ok, _ := ReadFrontmatter(bare); !ok || len(fm.Links) != 1 || !strings.HasSuffix(string(bare), "# Notes\n") {
		t.Errorf("AddLink on a doc with no frontmatter gave %q", bare)
	}
}
