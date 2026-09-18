package verdict

import (
	"reflect"
	"testing"

	"github.com/alternayte/speccy/internal/kernel"
)

// T-001
func TestVerdict_OpenMustBlocks(t *testing.T) {
	v := Decide(Input{Findings: []Finding{{ID: "f1", Level: kernel.Must}}})
	if v.Result != NotBuildReady || !reflect.DeepEqual(v.BlockingFindingIDs, []string{"f1"}) {
		t.Errorf("verdict = %+v, want not_build_ready blocked by f1", v)
	}
}

// T-002 (the rule part; waiver validity arrives with waivers)
func TestVerdict_WaivedMustPasses(t *testing.T) {
	v := Decide(Input{Findings: []Finding{{ID: "f1", Level: kernel.Must, Waived: true}}})
	if v.Result != BuildReady || v.WaiverCount != 1 {
		t.Errorf("verdict = %+v, want build_ready with 1 waiver", v)
	}
}

// T-003
func TestVerdict_ShouldNeverBlocks(t *testing.T) {
	var fs []Finding
	for range 50 {
		fs = append(fs, Finding{ID: "s", Level: kernel.Should}, Finding{ID: "i", Level: kernel.Info})
	}
	items := []Item{{Category: Clarity, Level: kernel.Should, Applicable: true}}
	v := Decide(Input{Findings: fs, Items: items})
	if v.Result != BuildReady {
		t.Errorf("SHOULD and INFO findings gave %s", v.Result)
	}
	if v.Score != 0 {
		t.Errorf("score = %d, want 0: the failed SHOULD item still counts in the score", v.Score)
	}
}

// T-004 (the rule part; threads arrive at M9)
func TestVerdict_BlockingThreadBlocks(t *testing.T) {
	if v := Decide(Input{OpenBlockingThreads: 1}); v.Result != NotBuildReady {
		t.Errorf("an open blocking thread gave %s", v.Result)
	}
}

// T-005 (the rule part; versions of a reviewed bundle stale on every new version)
func TestVerdict_OldVersionIsStale(t *testing.T) {
	if For(BuildReady, false) != Stale || For(NotBuildReady, true) != NotBuildReady {
		t.Error("For does not mark an old version stale")
	}
}

func TestVerdict_UpstreamAndLinks(t *testing.T) {
	if v := Decide(Input{UpstreamRequired: true}); v.Result != NotBuildReady {
		t.Error("a required upstream link that is missing did not block")
	}
	if v := Decide(Input{UpstreamRequired: true, HasUpstream: true}); v.Result != BuildReady {
		t.Error("a present upstream link blocked")
	}
	if v := Decide(Input{LinkedStale: true}); v.Result != NotBuildReady {
		t.Error("a stale linked version did not block")
	}
}

func TestScore(t *testing.T) {
	items := []Item{
		{Category: Structure, Level: kernel.Must, Passed: true, Applicable: true},
		{Category: Structure, Level: kernel.Must, Passed: false, Applicable: true},
		{Category: Clarity, Level: kernel.Should, Passed: false, Waived: true, Applicable: true},
		{Category: Clarity, Level: kernel.Info, Passed: false, Applicable: true},
		{Category: Completeness, Level: kernel.Must, Applicable: false},
	}
	v := Decide(Input{Items: items})
	if v.Score != 67 || v.Radar[Structure] != 50 || v.Radar[Clarity] != 100 {
		t.Errorf("score %d, radar %v; want 67, structure 50, clarity 100", v.Score, v.Radar)
	}
	if _, ok := v.Radar[Completeness]; ok {
		t.Error("an axis with no applicable item has a value")
	}
}

// T-006
func TestVerdict_Deterministic(t *testing.T) {
	a := Input{Findings: []Finding{{ID: "b", Level: kernel.Must}, {ID: "a", Level: kernel.Must}, {ID: "c", Level: kernel.Should}},
		Items: []Item{{Category: Structure, Level: kernel.Must, Applicable: true}, {Category: Clarity, Level: kernel.Should, Passed: true, Applicable: true}}}
	b := Input{Findings: []Finding{a.Findings[2], a.Findings[1], a.Findings[0]}, Items: []Item{a.Items[1], a.Items[0]}}
	first := Decide(a)
	for range 20 {
		if got := Decide(b); !reflect.DeepEqual(got, first) {
			t.Fatalf("Decide gave %+v, then %+v", first, got)
		}
	}
}
