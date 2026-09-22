package verify

import (
	"testing"

	"github.com/alternayte/speccy/internal/kernel"
)

func TestCheckNeedsExactlyOneMatch(t *testing.T) {
	content := []byte("package a\n\nfunc One() {}\n\nfunc Two() {}\n")
	got := Check(Target{Kind: Code, Path: "a.go", Quote: "func One() {}"}, content, true)
	if !got.Holds {
		t.Fatalf("a quote that appears once holds: %s", got.Fault)
	}
	if got.Line != 3 {
		t.Errorf("Line = %d, want 3", got.Line)
	}
	twice := Check(Target{Kind: Code, Path: "a.go", Quote: "func "}, content, true)
	if twice.Holds || twice.Fault != FaultNotUnique {
		t.Errorf("a quote that appears twice names no one place, got %+v", twice)
	}
	absent := Check(Target{Kind: Code, Path: "a.go", Quote: "func Three() {}"}, content, true)
	if absent.Holds || absent.Fault != FaultNotFound {
		t.Errorf("an absent quote fails, got %+v", absent)
	}
	noFile := Check(Target{Kind: Code, Path: "b.go", Quote: "x"}, nil, false)
	if noFile.Holds || noFile.Fault != FaultNoFile {
		t.Errorf("a file Speccy did not read fails, got %+v", noFile)
	}
}

func codeAndTest() []Target {
	return []Target{{Kind: Code, Holds: true}, {Kind: Test, Holds: true}}
}

func TestDecideNeedsAnAffirmation(t *testing.T) {
	// A judge that says nothing is not evidence: the outcome is unproven, and it never blocks.
	silent := Decide(Item{ID: "REQ-001", Level: kernel.Must, Parsed: true, Targets: codeAndTest(), Judgement: Silent})
	if silent.Outcome != Unproven || silent.Blocks || silent.Level != kernel.Should {
		t.Errorf("a silent judge gives %+v, want an unproven SHOULD that does not block", silent)
	}
	ok := Decide(Item{ID: "REQ-001", Level: kernel.Must, Parsed: true, Targets: codeAndTest(), Judgement: Affirmed})
	if ok.Outcome != Implemented || ok.Blocks {
		t.Errorf("an affirmed requirement with a test cited is implemented, got %+v", ok)
	}
	noTest := Decide(Item{ID: "REQ-001", Level: kernel.Must, Parsed: true,
		Targets: []Target{{Kind: Code, Holds: true}}, Judgement: Affirmed})
	if noTest.Outcome != Untested {
		t.Errorf("Outcome = %q, want untested", noTest.Outcome)
	}
}

func TestDecideBreachNeedsTwoJudges(t *testing.T) {
	one := Decide(Item{ID: "REQ-002", Level: kernel.Must, Parsed: true, Targets: codeAndTest(), Judgement: Contradicted})
	if one.Outcome == Breached || one.Blocks {
		t.Errorf("one judge must not turn a build red, got %+v", one)
	}
	if one.Note == "" {
		t.Error("a disagreement says so")
	}
	two := Decide(Item{ID: "REQ-002", Level: kernel.Must, Parsed: true, Targets: codeAndTest(),
		Judgement: Contradicted, Confirmed: true})
	if two.Outcome != Breached || !two.Blocks {
		t.Errorf("two judges that agree block, got %+v", two)
	}
}

func TestDecideCapsABreachOnAnUnparsedDefinition(t *testing.T) {
	got := Decide(Item{ID: "REQ-003", Level: kernel.Must, Parsed: false, Targets: codeAndTest(),
		Judgement: Contradicted, Confirmed: true})
	if got.Outcome != Breached {
		t.Fatalf("Outcome = %q", got.Outcome)
	}
	if got.Level != kernel.Should || got.Blocks {
		t.Errorf("a breach on a definition that does not parse reports at SHOULD, got %+v", got)
	}
}

func TestDecideMissingAndWaived(t *testing.T) {
	missing := Decide(Item{ID: "REQ-004", Level: kernel.Must})
	if missing.Outcome != Missing || !missing.Blocks {
		t.Errorf("a MUST with no target blocks, got %+v", missing)
	}
	waived := Decide(Item{ID: "REQ-004", Level: kernel.Must, Waived: true})
	if waived.Outcome != Missing || waived.Blocks {
		t.Errorf("a waiver stops the block, got %+v", waived)
	}
}

func TestVerdictAndBreachRate(t *testing.T) {
	rs := []Result{
		{Outcome: Implemented},
		{Outcome: Missing, Level: kernel.Must, Blocks: true},
		{Outcome: Unproven},
	}
	c := Tally(rs)
	if c.Verdict() != NotVerified {
		t.Errorf("a blocking outcome is not verified")
	}
	if got := c.BreachRate(); got < 0.33 || got > 0.34 {
		t.Errorf("BreachRate = %v, want a third", got)
	}
	if Tally([]Result{{Outcome: Implemented}}).Verdict() != Verified {
		t.Error("a run with nothing blocking is verified")
	}
}

func TestVerifiable(t *testing.T) {
	p := []string{"REQ", "NFR"}
	for _, id := range []string{"REQ-012", "NFR-003"} {
		if !Verifiable(id, p) {
			t.Errorf("%s is verifiable", id)
		}
	}
	for _, id := range []string{"DEC-004", "G-1", "nonsense"} {
		if Verifiable(id, p) {
			t.Errorf("%s names no code, so the gate skips it", id)
		}
	}
}

func TestRankPutsChangedFilesFirst(t *testing.T) {
	got := Rank([]string{"z.go", "a.go", "m.go"}, map[string]bool{"m.go": true})
	want := []string{"m.go", "a.go", "z.go"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rank = %v, want %v", got, want)
		}
	}
}
