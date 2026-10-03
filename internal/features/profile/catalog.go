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

// DocsBase is the docs site. web/src/lib/docs.ts holds the same address.
const DocsBase = "https://speccy-docs.pages.dev"

// catalogSlugs are the checks with an entry in the Check catalog: the engine checks that the
// catalog lists and have not been removed, and the checks of the built-in profiles.
var catalogSlugs = func() map[string]bool {
	var file struct {
		Checks []struct {
			Slug    string `yaml:"slug"`
			Removed string `yaml:"removed"`
		} `yaml:"checks"`
	}
	if err := yaml.Unmarshal(catalogYAML, &file); err != nil {
		panic("profile: catalog.yaml does not parse: " + err.Error())
	}
	out := map[string]bool{}
	for _, c := range file.Checks {
		if c.Removed == "" {
			out[c.Slug] = true
		}
	}
	builtins, err := Builtins()
	if err != nil {
		panic("profile: the built-in profiles do not load: " + err.Error())
	}
	for _, l := range builtins {
		for _, c := range l.Profile.Checks {
			out[c.Slug] = true
		}
	}
	return out
}()

// InCatalog reports whether the Check catalog has an entry for slug. A check that only a custom
// profile defines has none.
func InCatalog(slug string) bool { return catalogSlugs[slug] }

// CatalogURL is the address of the entry of slug in the Check catalog.
func CatalogURL(slug string) string { return DocsBase + "/reference/checks/#" + slug }
