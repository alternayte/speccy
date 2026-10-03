package review

import (
	"testing"

	"github.com/alternayte/speccy/internal/engine/section"
)

// The model copies a quote as one line, and the file breaks it over two. The question still
// goes on the line where the quote starts, which the pull request may change, and not on the
// heading.
func TestQuoteLine_QuoteOverALineBreak(t *testing.T) {
	src := []byte("# Pay\n\n## Components\n\nThe payment service calls the provider. The checkout service blocks and waits\nfor the result.\n")
	sec := section.Parse(src).Sections[1]
	if got := quoteLine(src, sec, "The checkout service blocks and waits for the result."); got != 5 {
		t.Errorf("line %d, want 5", got)
	}
	if got := quoteLine(src, sec, "Text that is not there."); got != 3 {
		t.Errorf("a quote that is not in the section: line %d, want the heading, 3", got)
	}
}
