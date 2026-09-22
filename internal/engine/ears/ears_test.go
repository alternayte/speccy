package ears

import "testing"

func TestParseShapes(t *testing.T) {
	cases := []struct {
		in       string
		shape    Shape
		trigger  string
		response string
	}{
		{"**REQ-012:** The parser MUST reject an unknown field.", Ubiquitous, "", "reject an unknown field"},
		{"REQ-013: When a run finishes, Speccy MUST store the verdict.", EventDriven, "a run finishes", "store the verdict"},
		{"While a run is queued, the page MUST show its position.", StateDriven, "a run is queued", "show its position"},
		{"Where Postgres is the store, the worker MUST use SKIP LOCKED.", OptionalFeature, "Postgres is the store", "use SKIP LOCKED"},
		{"If the token is refused, then Speccy MUST keep the last versions.", Unwanted, "the token is refused", "keep the last versions"},
		{"The gateway shall record the tokens of each call.", Ubiquitous, "", "record the tokens of each call"},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if !ok {
			t.Errorf("Parse(%q) did not parse", c.in)
			continue
		}
		if got.Shape != c.shape || got.Trigger != c.trigger || got.Response != c.response {
			t.Errorf("Parse(%q) = %+v, want shape %q trigger %q response %q", c.in, got, c.shape, c.trigger, c.response)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"REQ-020: The review pipeline.",
		"When the run finishes Speccy stores the verdict.",
		"Speccy stores the verdict when a run finishes.",
		"",
		"MUST reject an unknown field.",
	} {
		if r, ok := Parse(in); ok {
			t.Errorf("Parse(%q) parsed as %+v; it states no trigger and response", in, r)
		}
	}
}

func TestParseTakesTheFirstSentence(t *testing.T) {
	got, ok := Parse("When a waiver is approved, Speccy MUST store the section hash. The hash ends the waiver later.")
	if !ok {
		t.Fatal("the first sentence parses")
	}
	if got.Response != "store the section hash" {
		t.Errorf("Response = %q", got.Response)
	}
}
