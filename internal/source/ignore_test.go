package source

import (
	"testing"
	"testing/fstest"
)

// Git ignore rules decide which files never leave the machine in a bundle. The cases are the
// forms that teams write: a name anywhere, a folder, a pattern from the root, a nested file,
// and a negation.
func TestIgnore_GitRules(t *testing.T) {
	g := NewIgnore(fstest.MapFS{
		".gitignore":         {Data: []byte("# secrets\n*.env.yaml\nlocal/\n/build\nconfig/prod-*.yaml\n!config/prod-public.yaml\n")},
		"docs/.gitignore":    {Data: []byte("drafts/**\n")},
		"config/prod-a.yaml": {Data: nil},
	})
	for p, want := range map[string]bool{
		"shared/bus.md":            false,
		"app/dev.env.yaml":         true,
		"local/notes.md":           true,
		"a/local/notes.md":         true,
		"build/out.txt":            true,
		"docs/build/out.txt":       false,
		"config/prod-secrets.yaml": true,
		"config/prod-public.yaml":  false,
		"docs/drafts/x/y.md":       true,
		"drafts/y.md":              false,
	} {
		if got := g.Ignored(p); got != want {
			t.Errorf("%s: ignored %v, want %v", p, got, want)
		}
	}
}
