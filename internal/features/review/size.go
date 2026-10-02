package review

import (
	"strings"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// Size thresholds for the inference (REQ-134). A doc grows with what it covers: a feature
// design is short and links to one upstream doc, and an initiative names several children.
const (
	appWords        = 1200
	initiativeWords = 4000
	initiativeLinks = 3
)

// DocSize is the size of the main doc at rel, a path relative to the root. The first place
// that names a size decides: the size key of the frontmatter, the key of the team's template
// that frontmatter.keys maps to size, then the size of the map entry (#93). With none, the
// size comes from the doc's length and its links, and inferred is true. note is the line a
// run adds when the size is inferred, or when a place names a value that is not a size.
func DocSize(repo source.RepoConfig, rel string, main []byte) (sz kernel.Size, inferred bool, note string) {
	var bad *source.NamedSize
	for _, named := range repo.NamedSizes(rel, main) {
		if s, ok := kernel.ParseSize(named.Value); ok {
			if bad != nil {
				note = "The doc names the size \"" + bad.Value + "\" in " + bad.Where + ", which is not one of feature, app, initiative, so the review used size " +
					string(s) + " from " + named.Where + "."
			}
			return s, false, note
		}
		if bad == nil {
			bad = &named
		}
	}
	fm, _, _ := source.ReadFrontmatter(main)
	sz = InferSize(main, len(fm.Links))
	if bad != nil {
		return sz, true, "The doc names the size \"" + bad.Value + "\" in " + bad.Where + ", which is not one of feature, app, initiative, so the review used size " +
			string(sz) + ". Change it to " + string(sz) + " to fix it."
	}
	return sz, true, "The doc names no size, so the review used size " + string(sz) +
		". Add \"size: " + string(sz) + "\" to the frontmatter to fix it."
}

// InferSize reads the size from the doc's word count and its link count.
func InferSize(main []byte, links int) kernel.Size {
	words := len(strings.Fields(string(main)))
	switch {
	case links >= initiativeLinks || words >= initiativeWords:
		return kernel.Initiative
	case words >= appWords:
		return kernel.App
	}
	return kernel.Feature
}
