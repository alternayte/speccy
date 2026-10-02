package profile

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var catalogYAML []byte

// rewordChecks are the checks the Check catalog names as reword: a finding of one needs no
// fact from the author, so the bulk fix or an agent may fix it alone.
var rewordChecks = func() map[string]bool {
	var file struct {
		Checks []struct {
			Slug    string `yaml:"slug"`
			FixKind string `yaml:"fix_kind"`
		} `yaml:"checks"`
	}
	if err := yaml.Unmarshal(catalogYAML, &file); err != nil {
		panic("profile: catalog.yaml does not parse: " + err.Error())
	}
	out := map[string]bool{}
	for _, c := range file.Checks {
		if c.FixKind == "reword" {
			out[c.Slug] = true
		}
	}
	return out
}()

// Reword reports whether the Check catalog names slug as a reword check.
func Reword(slug string) bool { return rewordChecks[slug] }
