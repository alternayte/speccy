// Package verdict computes Build Ready or Not Build Ready (SDD §8.6) and the score (§8.7).
// REQ-070: one pure function with no I/O. It imports nothing outside kernel.
package verdict

import (
	"math"
	"sort"

	"github.com/alternayte/speccy/internal/kernel"
)

// Result is the verdict of a run.
type Result string

const (
	BuildReady    Result = "build_ready"
	NotBuildReady Result = "not_build_ready"
	// Stale is the verdict of a run on a version that is not the current one (T-005).
	Stale Result = "stale"
)

// Category is one radar axis (§8.7).
type Category string

const (
	Structure    Category = "structure"
	Clarity      Category = "clarity"
	Completeness Category = "completeness"
	Evidence     Category = "evidence"
	Precision    Category = "precision"
	Coherence    Category = "coherence"
)

// Categories lists the radar axes in display order.
var Categories = []Category{Structure, Clarity, Completeness, Evidence, Precision, Coherence}

// Finding is the part of a finding the verdict reads.
type Finding struct {
	ID     string
	Level  kernel.Level
	Waived bool // a valid waiver covers it
}

// Item is one scored item: a check result, a build question, or a coherence item.
type Item struct {
	Category   Category
	Level      kernel.Level
	Passed     bool
	Waived     bool
	Applicable bool
}

// Input is everything the verdict depends on.
type Input struct {
	Findings            []Finding
	Items               []Item
	OpenBlockingThreads int
	// UpstreamRequired is the profile's links.upstream.required. HasUpstream is true when a
	// link exists or a valid standalone acknowledgement exists.
	UpstreamRequired bool
	HasUpstream      bool
	// LinkedStale is true when the coherence stage used a linked bundle version that is no
	// longer current.
	LinkedStale bool
}

// Verdict is the output.
type Verdict struct {
	Result      Result
	Score       int
	Radar       map[Category]int
	WaiverCount int
	// BlockingFindingIDs are the open MUST findings, sorted.
	BlockingFindingIDs []string
}

// Decide applies §8.6: Build Ready if and only if no open MUST finding, no open blocking
// thread, the required upstream link or acknowledgement exists, and every linked bundle
// version used is current. SHOULD and INFO findings never change the result.
func Decide(in Input) Verdict {
	v := Verdict{Result: BuildReady, Radar: map[Category]int{}}
	for _, f := range in.Findings {
		if f.Waived {
			v.WaiverCount++
			continue
		}
		if f.Level == kernel.Must {
			v.BlockingFindingIDs = append(v.BlockingFindingIDs, f.ID)
		}
	}
	sort.Strings(v.BlockingFindingIDs)
	if len(v.BlockingFindingIDs) > 0 || in.OpenBlockingThreads > 0 ||
		(in.UpstreamRequired && !in.HasUpstream) || in.LinkedStale {
		v.Result = NotBuildReady
	}

	passed, applicable := map[Category]int{}, map[Category]int{}
	var allPassed, allApplicable int
	for _, it := range in.Items {
		if !it.Applicable || it.Level == kernel.Info {
			continue
		}
		applicable[it.Category]++
		allApplicable++
		if it.Passed || it.Waived {
			passed[it.Category]++
			allPassed++
		}
	}
	v.Score = score(allPassed, allApplicable)
	for _, c := range Categories {
		if applicable[c] > 0 {
			v.Radar[c] = score(passed[c], applicable[c])
		}
	}
	return v
}

// score is round(100 × passed / applicable). With no applicable item the score is 100.
func score(passed, applicable int) int {
	if applicable == 0 {
		return 100
	}
	return int(math.Round(100 * float64(passed) / float64(applicable)))
}

// For returns the verdict to show for a run: stale when the run's version is not current.
func For(result Result, runVersionIsCurrent bool) Result {
	if !runVersionIsCurrent {
		return Stale
	}
	return result
}
