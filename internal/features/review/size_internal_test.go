package review

import (
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/kernel"
)

// A size that is not valid is named in the note; the note says "names no size" only when the
// frontmatter has none.
func TestSizeNote_NamesInvalidSize(t *testing.T) {
	if n := sizeNote("", kernel.App); !strings.Contains(n, "names no size") {
		t.Errorf("no size: %q", n)
	}
	n := sizeNote("huge", kernel.App)
	if strings.Contains(n, "names no size") || !strings.Contains(n, `"huge"`) || !strings.Contains(n, "feature, app, initiative") {
		t.Errorf("invalid size: %q", n)
	}
}
