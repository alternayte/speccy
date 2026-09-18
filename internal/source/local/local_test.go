package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/source"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/prd/PRD.md", "---\ntype: prd\n---\n# P\n")
	write(t, dir, "docs/prd/assets/a.png", "png")
	write(t, dir, "docs/prd/child/SDD.md", "---\ntype: sdd\n---\n# Child\n")
	write(t, dir, "docs/prd/.hidden", "x")
	write(t, dir, "docs/dup/a.md", "---\ntype: prd\n---\n")
	write(t, dir, "docs/dup/b.md", "---\ntype: sdd\n---\n")
	write(t, dir, "README.md", "# Not a bundle\n")
	write(t, dir, "node_modules/x/SPEC.md", "---\ntype: prd\n---\n")
	write(t, dir, ".speccy/state/SPEC.md", "---\ntype: prd\n---\n")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Scan()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range s.Bundles {
		var paths []string
		for _, f := range b.Files {
			paths = append(paths, f.Path)
		}
		got = append(got, b.Dir+"="+strings.Join(paths, ","))
	}
	want := []string{"docs/prd=PRD.md,assets/a.png", "docs/prd/child=SDD.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("bundles = %v, want %v", got, want)
	}
	if len(s.Problems) != 1 || s.Problems[0].Path != "docs/dup" || !strings.Contains(s.Problems[0].Message, "a.md, b.md") {
		t.Errorf("problems = %+v, want one for docs/dup naming a.md and b.md", s.Problems)
	}
}

func TestPersist_StaysInsideRoot(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	write(t, dir, "b/SPEC.md", "---\ntype: prd\n---\n")
	write(t, dir, "b/nested/SPEC.md", "---\ntype: sdd\n---\n")
	if err := os.Symlink(outside, filepath.Join(dir, "b", "link")); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Scan()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"link/x.md", "nested/x.md"} {
		if err := r.Persist(s, "b", source.Op{Kind: source.OpWrite, Path: p, Content: []byte("x")}); err == nil {
			t.Errorf("write to %s succeeded", p)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("a file was written outside the root: %v", entries)
	}
	if err := r.Persist(s, "b", source.Op{Kind: source.OpWrite, Path: "assets/new/x.sql", Content: []byte("x")}); err != nil {
		t.Errorf("write to a new folder: %v", err)
	}
}
