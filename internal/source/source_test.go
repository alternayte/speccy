package source

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanPath(t *testing.T) {
	ok := map[string]string{"SPEC.md": "SPEC.md", "assets/a.png": "assets/a.png", "a/./b.md": "a/b.md", "a//b": "a/b"}
	for in, want := range ok {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "/etc/passwd", "../x.md", "a/../../x", "a/../b", `a\b.md`, ".git/config", "a/.env", "x\x00y"} {
		if got, err := CleanPath(in); err == nil {
			t.Errorf("CleanPath(%q) = %q; want an error", in, got)
		}
	}
}

func TestApply(t *testing.T) {
	files := []File{{Path: "SPEC.md", Content: []byte("a")}, {Path: "assets/x.png", Content: []byte("p")}}
	got, err := Apply(files, Op{Kind: OpWrite, Path: "assets/y.sql", Content: []byte("s")})
	if err != nil || len(got) != 3 || got[2].Path != "assets/y.sql" {
		t.Fatalf("write new: %v %v", got, err)
	}
	if len(files) != 2 {
		t.Fatal("Apply changed its input")
	}
	if _, err := Apply(files, Op{Kind: OpWrite, Path: "assets", Content: nil}); err == nil {
		t.Error("writing a file over a folder succeeded")
	}
	if _, err := Apply(files, Op{Kind: OpWrite, Path: "SPEC.md/x", Content: nil}); err == nil {
		t.Error("writing a file under a file succeeded")
	}
	if _, err := Apply(files, Op{Kind: OpDelete, Path: "nope.md"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}
	if _, err := Apply(files, Op{Kind: OpRename, Path: "assets/x.png", To: "SPEC.md"}); !errors.Is(err, ErrExists) {
		t.Errorf("rename onto an existing file: %v", err)
	}
	got, err = Apply(files, Op{Kind: OpRename, Path: "assets/x.png", To: "img/x.png"})
	if err != nil || got[1].Path != "img/x.png" || string(got[1].Content) != "p" {
		t.Errorf("rename: %v %v", got, err)
	}
	if _, err := Apply(files, Op{Kind: OpRename, Path: "SPEC.md", To: "../SPEC.md"}); err == nil {
		t.Error("rename out of the bundle succeeded")
	}
}

// REQ-001
func TestFindMainDoc(t *testing.T) {
	main := []byte("---\ntype: sdd\n---\n# Payments\n")
	cases := []struct {
		name    string
		files   []File
		want    string
		wantErr string
	}{
		{"one main doc", []File{{"SPEC.md", main}, {"notes.md", []byte("# Notes\n")}}, "SPEC.md", ""},
		{"main doc in a subfolder is an asset", []File{{"a/SPEC.md", main}}, "", "no main doc"},
		{"none", []File{{"notes.md", []byte("# Notes\n")}}, "", "no main doc"},
		{"two", []File{{"a.md", main}, {"b.md", main}}, "", "a.md, b.md"},
		{"empty type", []File{{"a.md", []byte("---\ntype: ''\n---\n")}}, "", "no main doc"},
		{"bad yaml", []File{{"a.md", []byte("---\ntype: [\n---\n")}}, "", "a.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := FindMainDoc(c.files)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil || m.Path != c.want {
				t.Fatalf("got %q, %v; want %q", m.Path, err, c.want)
			}
			if m.Title != "Payments" || m.Frontmatter.Type != "sdd" {
				t.Errorf("main doc = %+v", m)
			}
		})
	}
}

func TestLinkRule(t *testing.T) {
	r, err := ParseLinkRule("docs/sdd-{name}.md implements docs/prd-{name}.md")
	if err != nil {
		t.Fatal(err)
	}
	if to, ok := r.Target("docs/sdd-payments.md"); !ok || to != "docs/prd-payments.md" {
		t.Errorf("Target = %q, %v", to, ok)
	}
	if _, ok := r.Target("docs/sub/sdd-payments.md"); ok {
		t.Error("{name} matched across a folder")
	}
	for _, bad := range []string{"a implements", "a owns b", "a implements b/{x}", "{x}/{x} refines b"} {
		if _, err := ParseLinkRule(bad); err == nil {
			t.Errorf("ParseLinkRule(%q) gave no error", bad)
		}
	}
}
