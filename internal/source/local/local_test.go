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
	write(t, dir, "docs/dup/notes.md", "# Notes, not a spec\n")
	write(t, dir, "README.md", "# Not a bundle\n")
	write(t, dir, "node_modules/x/SPEC.md", "---\ntype: prd\n---\n")
	write(t, dir, ".speccy/state/SPEC.md", "---\ntype: prd\n---\n")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Scan(source.RepoConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range s.Bundles {
		var paths []string
		for _, f := range b.Files {
			paths = append(paths, f.Path)
		}
		got = append(got, b.Slug+"="+strings.Join(paths, ","))
	}
	// A folder with two spec docs gives one bundle with both: each spec doc's version holds
	// the doc and the folder's assets, and never the other spec doc. A subfolder with its own
	// spec doc is its own bundle.
	want := []string{"docs/dup/a=a.md,notes.md", "docs/dup/b=b.md,notes.md", "docs/prd=PRD.md,assets/a.png", "docs/prd/child=SDD.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("bundles = %v, want %v", got, want)
	}
	if len(s.Problems) != 0 {
		t.Errorf("problems = %+v, want none", s.Problems)
	}
	for _, b := range s.Bundles {
		if b.Slug == "docs/dup/a" && b.Folder != "docs/dup" {
			t.Errorf("docs/dup/a is in bundle %q, want docs/dup", b.Folder)
		}
	}
	// Untyped markdown in a bundle is an asset, and it stays a skipped doc until a person
	// decides on it, so an Accept on one doc does not take the others off the list (#73).
	if strings.Join(s.Skipped, ",") != "docs/dup/notes.md" {
		t.Errorf("skipped = %v, want docs/dup/notes.md", s.Skipped)
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
	s, err := r.Scan(source.RepoConfig{})
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

// REQ-130, REQ-131
func TestScan_SingleFileBundles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/prd-pay.md", "# Pay\n\n![flow](prd-pay.assets/flow.png)\n")
	write(t, dir, "docs/prd-pay.assets/flow.png", "png")
	write(t, dir, "docs/sdd-pay.md", "---\ntype: sdd\n---\n# Pay design\n")
	write(t, dir, "docs/notes.md", "# Notes\n")
	write(t, dir, "specs/a/SPEC.md", "---\ntype: sdd\n---\n# A\n")
	write(t, dir, "other/b/SPEC.md", "---\ntype: sdd\n---\n# B\n")
	cfg, err := source.ParseRepoConfig([]byte("bundles:\n  - path: specs/*\nmap:\n  - glob: docs/**/prd-*.md\n    profile: prd\n  - glob: docs/**/sdd-*.md\n    profile: prd\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Scan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range s.Bundles {
		var paths []string
		for _, f := range b.Files {
			paths = append(paths, f.Path)
		}
		got = append(got, b.Slug+":"+b.Main.Frontmatter.Type+"="+strings.Join(paths, ","))
	}
	// The mapped files are spec docs of the docs bundle; the frontmatter type wins over the
	// mapping. other/b is not under a bundles glob, so it is not a bundle.
	want := []string{"docs/prd-pay:prd=notes.md,prd-pay.assets/flow.png,prd-pay.md", "docs/sdd-pay:sdd=notes.md,prd-pay.assets/flow.png,sdd-pay.md", "specs/a:sdd=SPEC.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("bundles = %v, want %v", got, want)
	}
	for p, ok := range map[string]bool{"prd-pay.assets/new.sql": true, "other.md": true, "sdd-pay.md": false, "prd-pay.assets/../x.md": false} {
		err := r.Persist(s, "docs/prd-pay", source.Op{Kind: source.OpWrite, Path: p, Content: []byte("x")})
		if (err == nil) != ok {
			t.Errorf("write %s: err = %v, want allowed %v", p, err, ok)
		}
	}
}

// docs/specs/carry-repo-files.md: a bundle carries the files that its doc references above its
// folder, at @repo/<path from the root>, and the files those reference in turn. A link to a
// spec doc of another bundle is a doc ref and is not carried. A file that git ignores is a
// refused ref. A link above the root, and a real @repo folder, carry nothing.
func TestScan_CarriesRepoFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "config/prod-*.yaml\n")
	write(t, dir, "pay/SPEC.md", "---\ntype: sdd\n---\n# Pay\n\n[bus](../shared/bus.md) [prd](../prd/PRD.md) "+
		"[secrets](../config/prod-secrets.yaml) [out](../../outside.md) [hidden](../.env)\n")
	write(t, dir, "pay/@repo/x.md", "a file in the reserved folder")
	write(t, dir, "shared/bus.md", "# Bus\n\n![diagram](img/flow.png)\n")
	write(t, dir, "shared/img/flow.png", "png")
	write(t, dir, "prd/PRD.md", "---\ntype: prd\n---\n# PRD\n")
	write(t, dir, "config/prod-secrets.yaml", "key: secret")
	write(t, dir, ".env", "KEY=1")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Scan(source.RepoConfig{})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := s.Bundle("pay")
	if !ok {
		t.Fatal("no bundle pay")
	}
	var paths []string
	for _, f := range b.Files {
		paths = append(paths, f.Path+"<"+f.CarriedBy)
	}
	if want := "@repo/shared/bus.md<SPEC.md @repo/shared/img/flow.png<@repo/shared/bus.md SPEC.md<"; strings.Join(paths, " ") != want {
		t.Errorf("files %q, want %q", strings.Join(paths, " "), want)
	}
	var refs []string
	for _, ref := range b.Refs {
		refs = append(refs, ref.Target+"="+ref.State+":"+ref.Path)
	}
	if want := "../config/prod-secrets.yaml=ignored: ../prd/PRD.md=doc: ../shared/bus.md=carried:@repo/shared/bus.md"; strings.Join(refs, " ") != want {
		t.Errorf("refs %q, want %q", strings.Join(refs, " "), want)
	}
	reserved := false
	for _, p := range s.Problems {
		reserved = reserved || p.Path == "pay/@repo/x.md"
	}
	if !reserved {
		t.Errorf("a real @repo folder in the bundle is not a problem: %v", s.Problems)
	}
	if files, err := r.Load(s, "pay"); err != nil || len(files) != len(b.Files) {
		t.Errorf("load gives %d files (%v), want the %d of the scan", len(files), err, len(b.Files))
	}
}
