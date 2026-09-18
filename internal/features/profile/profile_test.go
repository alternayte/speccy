package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// REQ-011
func TestBuiltins(t *testing.T) {
	ls, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]Loaded{}
	for _, l := range ls {
		keys[l.Profile.Key] = l
	}
	sdd, ok := keys["sdd"]
	if !ok || keys["prd"].Profile.Key != "prd" {
		t.Fatalf("built-in keys = %v", keys)
	}
	if len(sdd.Profile.Checks) != 20 || len(keys["prd"].Profile.Checks) != 15 {
		t.Errorf("checks: sdd %d, prd %d; Appendix A has 20 and 15", len(sdd.Profile.Checks), len(keys["prd"].Profile.Checks))
	}
	if sdd.Profile.Waivers.Must.Name != "maintainer" || sdd.Profile.Links.Upstream == nil || !sdd.Profile.Links.Upstream.Required {
		t.Errorf("sdd policies = %+v", sdd.Profile)
	}
	req := RequiredHeadings(sdd.TemplateText)
	if len(req) == 0 || req[0].Title != "Context" || req[0].Level != 2 {
		t.Errorf("required headings = %+v", req)
	}
	if strings.Contains(string(StripMarks(sdd.TemplateText)), "required -->") {
		t.Error("StripMarks left a marker")
	}
}

// REQ-014: each schema error names its path.
func TestParse_ErrorsHavePaths(t *testing.T) {
	src := []byte("key: Bad Key\nname: x\ntemplate: t.md\nlimits: { max_words: 0 }\nwaivers: { must: sometimes }\nchecks:\n  - { slug: nodot, level: HIGH, stage: rubric }\n")
	_, err := Parse("p.yaml", src, func(string) ([]byte, error) { return []byte("# T\n"), nil })
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	joined := strings.Join(ve.Errors, "\n")
	for _, path := range []string{"/key", "/limits/max_words", "/waivers/must", "/checks/0/slug", "/checks/0/level"} {
		if !strings.Contains(joined, path+":") {
			t.Errorf("no error for %s in:\n%s", path, joined)
		}
	}
}

// REQ-013
func TestLoadLocal_Overrides(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("t.md", "# Doc\n\n## Scope <!-- required -->\n")
	write("sdd.yaml", "key: sdd\nname: Our SDD\ntemplate: t.md\nlimits: { max_sentence_words: 30 }\nchecks: []\n")
	write("adr.yaml", "key: adr\nname: ADR\ntemplate: t.md\nchecks: []\n")
	got, err := LoadLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["sdd"].Profile.Name != "Our SDD" || got["sdd"].Profile.Limits.MaxSentenceWords != 30 || got["sdd"].Profile.Limits.MaxWords != 8000 {
		t.Errorf("sdd override = %+v", got["sdd"].Profile)
	}
	if got["adr"].Profile.Key != "adr" || got["prd"].Origin != "built-in" {
		t.Errorf("profiles = %v", got)
	}
	write("bad.yaml", "key: x\n")
	if _, err := LoadLocal(dir); err == nil {
		t.Error("a bad profile file loaded without an error")
	}
}
