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
	// A folder with two spec docs gives one single-file bundle per doc, and its untyped
	// markdown is a skipped doc, not a bundle.
	want := []string{"docs/dup/a=a.md", "docs/dup/b=b.md", "docs/prd=PRD.md,assets/a.png", "docs/prd/child=SDD.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("bundles = %v, want %v", got, want)
	}
	if len(s.Problems) != 0 {
		t.Errorf("problems = %+v, want none", s.Problems)
	}
	if !strings.Contains(strings.Join(s.Skipped, " "), "docs/dup/notes.md") {
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
	// The mapped files are single-file bundles; the frontmatter type wins over the mapping.
	// other/b is not under a bundles glob, so it is not a bundle.
	want := []string{"docs/prd-pay:prd=prd-pay.assets/flow.png,prd-pay.md", "docs/sdd-pay:sdd=sdd-pay.md", "specs/a:sdd=SPEC.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("bundles = %v, want %v", got, want)
	}
	for p, ok := range map[string]bool{"prd-pay.assets/new.sql": true, "other.md": false, "prd-pay.assets/../x.md": false} {
		err := r.Persist(s, "docs/prd-pay", source.Op{Kind: source.OpWrite, Path: p, Content: []byte("x")})
		if (err == nil) != ok {
			t.Errorf("write %s: err = %v, want allowed %v", p, err, ok)
		}
	}
}

// A bundle carries the files its markdown points at, to closure, and stops at another
// bundle's doc and at the folder above it. Without this a doc's own image is a MUST finding
// for a file that exists.
func TestScan_CarriesReferencedFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "shared/logo.png", "png")
	write(t, dir, "docs/prd-pay.md", "# Pay\n\n"+
		"![flow](images/flow.png)\n[limits](limits.md)\n[other](sdd-pay.md)\n[up](../shared/logo.png)\n[gone](missing.png)\n")
	write(t, dir, "docs/images/flow.png", "png")
	write(t, dir, "docs/limits.md", "# Limits\n\n![limit](images/limit.png)\n")
	write(t, dir, "docs/images/limit.png", "png")
	write(t, dir, "docs/sdd-pay.md", "# Pay design\n")
	cfg, err := source.ParseRepoConfig([]byte("map:\n  - glob: docs/prd-*.md\n    profile: prd\n  - glob: docs/sdd-*.md\n    profile: sdd\n"))
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
	b, ok := s.Bundle("docs/prd-pay")
	if !ok {
		t.Fatal("the mapped doc is not a bundle")
	}
	got := map[string]string{}
	for _, f := range b.Files {
		got[f.Path] = f.CarriedBy
	}
	// The image, the reference doc, and the image that reference doc shows.
	for _, p := range []string{"images/flow.png", "limits.md", "images/limit.png"} {
		if _, ok := got[p]; !ok {
			t.Errorf("%s is not in the bundle: %v", p, got)
		}
	}
	if got["images/limit.png"] != "limits.md" {
		t.Errorf("images/limit.png was carried by %q, want limits.md", got["images/limit.png"])
	}
	if got["prd-pay.md"] != "" {
		t.Error("the main doc is not a carried file")
	}
	// Another bundle's doc, a file above the folder, and a file that is not there.
	for _, p := range []string{"sdd-pay.md", "../shared/logo.png", "shared/logo.png", "missing.png"} {
		if _, ok := got[p]; ok {
			t.Errorf("%s must not be in the bundle: %v", p, got)
		}
	}
}
