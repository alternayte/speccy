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

// docSize is the size of the main doc: the frontmatter size when it names one, else the size
// inferred from the doc's length and its links. inferred reports which of the two it is.
func docSize(fm source.Frontmatter, main []byte) (sz kernel.Size, inferred bool) {
	if s, ok := kernel.ParseSize(fm.Size); ok {
		return s, false
	}
	return InferSize(main, len(fm.Links)), true
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

// sizeNote is the line a run adds when it inferred the size, so the author reads which size
// the checks ran at and can set it. named is the size the frontmatter names: empty, or a value
// that is not a size.
func sizeNote(named string, sz kernel.Size) string {
	if named = strings.TrimSpace(named); named != "" {
		return "The doc names the size \"" + named + "\", which is not one of feature, app, initiative, so the review used size " +
			string(sz) + ". Change it to \"size: " + string(sz) + "\" in the frontmatter to fix it."
	}
	return "The doc names no size, so the review used size " + string(sz) +
		". Add \"size: " + string(sz) + "\" to the frontmatter to fix it."
}
