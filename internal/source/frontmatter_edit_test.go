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

// RemoveLink takes out only the named link, drops the links key with the last one, and leaves
// the body and the other keys as they are.
func TestRemoveLink(t *testing.T) {
	doc := []byte("---\ntype: sdd\nlinks:\n  - kind: implements\n    target: prd\n  - kind: references\n    target: github:o/r#1\n---\n\n# Pay\n")
	got, ok, err := RemoveLink(doc, "implements", "prd")
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	fm, _, _ := ReadFrontmatter(got)
	if fm.Type != "sdd" || len(fm.Links) != 1 || fm.Links[0].Kind != "references" || !strings.HasSuffix(string(got), "\n# Pay\n") {
		t.Errorf("RemoveLink gave %q", got)
	}
	got, _, _ = RemoveLink(got, "references", "github:o/r#1")
	if strings.Contains(string(got), "links") {
		t.Errorf("the empty links key stayed: %q", got)
	}
	if same, ok, _ := RemoveLink(doc, "implements", "other"); ok || string(same) != string(doc) {
		t.Errorf("a link the doc does not name changed the doc: %q", same)
	}
}

// A link written into a JSON block inside an HTML comment keeps the comment, the JSON, the
// indent and the key order, so the wiki still hides the block (#76).
func TestAddLink_KeepsWrappedJSON(t *testing.T) {
	doc := []byte("<!--\n---\n{\n    \"title\": \"SDD - X\",\n    \"sdd_level\": \"initiative\"\n}\n---\n-->\n\n# SDD - X\n")
	got, err := AddLink(AddTypeLine(doc, "sdd"), "implements", "PRD - X.md")
	if err != nil {
		t.Fatal(err)
	}
	want := "<!--\n---\n{\n    \"type\": \"sdd\",\n    \"title\": \"SDD - X\",\n    \"sdd_level\": \"initiative\",\n" +
		"    \"links\": [\n        {\n            \"kind\": \"implements\",\n            \"target\": \"PRD - X.md\"\n        }\n    ]\n}\n---\n-->\n\n# SDD - X\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	fm, _, err := ReadFrontmatter(got)
	if err != nil || fm.Type != "sdd" || len(fm.Links) != 1 {
		t.Errorf("read back %+v, %v", fm, err)
	}
}

// A links value Speccy does not read keeps the type readable, and the problem names the form
// Speccy expects (#76).
func TestFrontmatterProblem(t *testing.T) {
	doc := []byte("---\n{\"type\": \"sdd\", \"links\": {\"implements\": [\"PRD - X.md\"]}}\n---\n# X\n")
	if fm, _, err := ReadFrontmatter(doc); err != nil || fm.Type != "sdd" || fm.Links != nil {
		t.Errorf("ReadFrontmatter = %+v, %v", fm, err)
	}
	if p, fix := FrontmatterProblem(doc); !strings.Contains(p, "links is a map, not a list") || !strings.Contains(fix, `"kind": "implements"`) {
		t.Errorf("problem %q, fix %q", p, fix)
	}
	typed := []byte("---\ntype: sdd\nlinks:\n  - type: implements\n    target: prd\n---\n")
	if p, _ := FrontmatterProblem(typed); !strings.Contains(p, "has type and no kind") {
		t.Errorf("problem %q", p)
	}
	late := []byte("# X\n\n<!--\n---\ntype: sdd\n---\n-->\n")
	if p, _ := FrontmatterProblem(late); p == "" {
		t.Error("a block inside a comment after the heading gave no problem")
	}
	fenced := []byte("# X\n\n```\n<!--\n---\ntype: sdd\n---\n-->\n```\n")
	if p, _ := FrontmatterProblem(fenced); p != "" {
		t.Errorf("a comment in a code block gave %q", p)
	}
}
