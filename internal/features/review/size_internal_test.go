package review

import (
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// #93: the size of a doc comes from the first place that names one: the size key of the
// frontmatter, the key of the team's template, then the map entry. With none, it is inferred.
func TestDocSize(t *testing.T) {
	repo, err := source.ParseRepoConfig([]byte("frontmatter:\n  keys:\n    size: sdd_level\nmap:\n  - glob: \"**/SDD - *.md\"\n    profile: sdd\n    size: app\n"))
	if err != nil {
		t.Fatal(err)
	}
	const rel = "pay/SDD - Pay.md"
	for _, c := range []struct {
		name     string
		repo     source.RepoConfig
		doc      string
		want     kernel.Size
		inferred bool
		note     string // a part of the run note, or "" for no note
	}{
		{"the size key wins", repo, "---\nsize: feature\nsdd_level: initiative\n---\n# T\n", kernel.Feature, false, ""},
		{"the template key, in JSON inside a comment", repo, "<!--\n---\n{\"type\": \"sdd\", \"sdd_level\": \"initiative\"}\n---\n-->\n\n# T\n", kernel.Initiative, false, ""},
		{"the map entry, for a doc with no frontmatter", repo, "# T\n", kernel.App, false, ""},
		{"a template value that is not a size falls back to the map entry, with a note", repo, "---\nsdd_level: epic\n---\n# T\n", kernel.App, false, `"epic" in the frontmatter key sdd_level`},
		{"no place names a size", source.RepoConfig{}, "# T\n", kernel.Feature, true, "names no size"},
		{"a size that is not a size, and no other place", source.RepoConfig{}, "---\nsize: huge\n---\n# T\n", kernel.Feature, true, `"huge" in the frontmatter key size`},
	} {
		sz, inferred, note := DocSize(c.repo, rel, []byte(c.doc))
		if sz != c.want || inferred != c.inferred || (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
			t.Errorf("%s: size %s, inferred %v, note %q; want %s, %v, note with %q", c.name, sz, inferred, note, c.want, c.inferred, c.note)
		}
	}
	for _, bad := range []string{"map:\n  - glob: \"*.md\"\n    profile: sdd\n    size: huge\n", "frontmatter:\n  keys:\n    type: doc_type\n"} {
		if _, err := source.ParseRepoConfig([]byte(bad)); err == nil {
			t.Errorf("the config %q parsed", bad)
		}
	}
}
