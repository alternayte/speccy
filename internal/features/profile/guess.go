package profile

import (
	"strings"

	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
)

// Guess picks the profile of a doc that names no type (REQ-135). It counts how many of each
// profile's required headings the doc has, and takes the best match. It reads no model, so the
// first review of a doc costs nothing and always states why it chose what it chose.
//
// ok is false when no profile matches a single heading: the caller then asks the author.
func Guess(profiles map[string]Versioned, content []byte) (key string, ok bool) {
	have := map[string]bool{}
	for _, s := range section.Parse(content).Sections {
		if s.Level > 0 {
			have[lint.NormTitle(s.Title)] = true
		}
	}
	best, bestScore := "", 0.0
	for k, p := range profiles {
		required := RequiredHeadings(p.TemplateText)
		if len(required) == 0 {
			continue
		}
		hits := 0
		for _, h := range required {
			if have[lint.NormTitle(h.Title)] {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		// The share of the profile's headings that the doc has. A share, not a count, so a
		// profile with many headings does not win only because it has many.
		score := float64(hits) / float64(len(required))
		if score > bestScore || (score == bestScore && k < best) {
			best, bestScore = k, score
		}
	}
	return best, best != ""
}

// GuessNote is the line a review adds when it chose the profile itself.
func GuessNote(key string, keys []string) string {
	return "The doc names no type, so the review used the " + key + " profile. Add \"type: " + key +
		"\" to the frontmatter to fix it. The types are: " + strings.Join(keys, ", ") + "."
}
