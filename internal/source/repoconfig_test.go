package source

import (
	"strings"
	"testing"
)

func TestAddMappings(t *testing.T) {
	was := []byte("# the repo's own comment\nmode: standalone\nmap:\n  - glob: docs/prd-*.md\n    profile: prd\n")
	out, err := AddMappings(was, []Mapping{{Glob: "specs/*.md", Profile: "sdd"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "the repo's own comment") {
		t.Errorf("the comment was dropped:\n%s", out)
	}
	cfg, err := ParseRepoConfig(out)
	if err != nil {
		t.Fatalf("the result does not parse: %v\n%s", err, out)
	}
	if len(cfg.Map) != 2 || cfg.Map[1].Glob != "specs/*.md" || cfg.Map[1].Profile != "sdd" || cfg.Mode != "standalone" {
		t.Errorf("config = %+v", cfg)
	}
	// A repo with no file gets one that holds the mappings only.
	out, err = AddMappings(nil, []Mapping{{Glob: "specs/*.md", Profile: "sdd"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err = ParseRepoConfig(out); err != nil || len(cfg.Map) != 1 {
		t.Errorf("new file = %q, %+v, %v", out, cfg, err)
	}
}
