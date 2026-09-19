package inbox

import (
	"testing"

	"github.com/alternayte/speccy/internal/kernel"
)

func TestMentions(t *testing.T) {
	ada := kernel.Person{Email: "ada@example.test"}
	for body, want := range map[string]bool{
		"@ada please check":             true,
		"Thanks @ada.":                  true,
		"ask @ada@example.test":         true,
		"@adam knows":                   false,
		"mail ada@example.test":         false,
		"@ada.lovelace is someone else": false,
	} {
		if got := mentions(body, ada); got != want {
			t.Errorf("mentions(%q) = %v, want %v", body, got, want)
		}
	}
}
