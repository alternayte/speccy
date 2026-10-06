package handoff

import (
	"strings"
	"testing"
)

// #130: a doc that defines an ID in a table row and again in a heading lists the ID once in
// the build packet, with the text of the table row.
func TestTraceIDs_OncePerID(t *testing.T) {
	main := []byte("# Refunds\n\n## Requirements\n\n| ID | Requirement |\n|---|---|\n| REQ-001 | The system MUST refund a card payment. |\n\n" +
		"### REQ-001 — Card refunds\n\nThe refund goes to the card.\n")
	got := traceIDs(main, []string{"REQ"})
	if len(got) != 1 || got[0].Id != "REQ-001" || !strings.Contains(got[0].Text, "refund a card payment") {
		t.Errorf("trace IDs = %+v, want REQ-001 once, with the text of the table row", got)
	}
}
