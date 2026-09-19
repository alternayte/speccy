package coherence

import "testing"

func TestRestated(t *testing.T) {
	up := []string{
		"The payment service retries a card payment up to three times when the provider returns a temporary error.",
		"Refunds are out of scope.",
	}
	down := []string{
		// The same sentence with two words changed at the end: most shingles match.
		"The payment service retries a card payment up to three times when the provider returns a timeout.",
		// Different text.
		"The retry worker reads attempts from the payment_attempt table and schedules the next one.",
		// Too short for a shingle.
		"Refunds are out of scope.",
	}
	got := Restated(down, up)
	if len(got) != 1 || got[0].Down != 0 || got[0].Up != 0 || got[0].Overlap <= RestateThreshold {
		t.Fatalf("Restated = %+v, want paragraph 0 matching upstream 0", got)
	}
}

func TestCoverage(t *testing.T) {
	got := Coverage([]string{"REQ-001", "REQ-002", "REQ-003", "REQ-004"},
		map[string]bool{"REQ-001": true},
		map[string]Ack{
			"REQ-002": {Status: "out_of_scope", Reason: "Phase two."},
			"REQ-003": {Status: "covered_by"}, // no target or reason: not valid
		})
	want := []string{"referenced", "acknowledged", "gap", "gap"}
	for i, c := range got {
		if c.State != want[i] {
			t.Errorf("%s: %s, want %s", c.ID, c.State, want[i])
		}
	}
}
