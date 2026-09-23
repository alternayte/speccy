// Package nextaction names the one thing a person must do next on a bundle. The server owns
// the rule, so the web app, the TUI and the MCP server all say the same sentence.
package nextaction

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/http/api"
)

// Inputs are the signals the rule reads. The bundles list fills the cheap ones. One bundle
// fills the rest, so its next action also carries a target.
type Inputs struct {
	// WaiverWaiting is a waiver that waits for this person's decision, if there is one.
	WaiverWaiting *api.Waiver
	Verdict       *api.BundleVerdict
	// CurrentVersion is the version number of the bundle now.
	CurrentVersion int64
	RunError       *string
	Status         api.ReviewStatus
	// Adopt is the frontmatter the main doc does not name. One bundle only.
	Adopt *api.Adopt
	// Point is the first tour point. One bundle only.
	Point *api.TourPoint
	// Finding is the first MUST finding that is not waived. One bundle only.
	Finding *api.Finding
	// CanEdit is false for a person who only reads the bundle: they get no author action.
	CanEdit bool
	// Hosted is true when a team uses this server. Local mode has one person, so it asks for
	// no approval.
	Hosted bool
	// Approvals is how many approvals the profile needs, and how many the bundle has.
	ApprovalsNeeded, ApprovalsGiven int
	// Handoffs is how many builders took the packet of the current version.
	Handoffs int
}

// Of names the next action, or nil when nothing is open. The order runs from what blocks other
// people to what only this person can start (SDD §13.4).
func Of(in Inputs) *api.NextAction {
	if w := in.WaiverWaiting; w != nil {
		id := w.Id
		return &api.NextAction{
			Kind:     api.NextActionKindWaiver,
			Sentence: fmt.Sprintf("Decide the waiver of %s", w.CheckSlug),
			WaiverId: &id,
		}
	}
	if !in.CanEdit {
		return nil
	}
	if p := in.Point; p != nil {
		key := p.Key
		out := &api.NextAction{Kind: api.NextActionKindDecide, Sentence: short(p.Ask, 80), TourKey: &key}
		if p.FindingId != nil {
			out.FindingId = p.FindingId
		}
		return out
	}
	v := in.Verdict
	// The list has no tour. The verdict's counts name the same work in the same order.
	if in.Point == nil && in.Finding == nil && v != nil && !stale(v, in.CurrentVersion) {
		if n := deref(v.BlockingThreads); n > 0 {
			return &api.NextAction{
				Kind:     api.NextActionKindDecide,
				Sentence: fmt.Sprintf("Answer %d blocking thread%s", n, plural(n)),
			}
		}
	}
	if f := in.Finding; f != nil {
		id := f.Id
		return &api.NextAction{
			Kind:      api.NextActionKindFix,
			Sentence:  "Fix: " + short(f.Message, 80),
			FindingId: &id,
		}
	}
	if v == nil || in.RunError != nil || stale(v, in.CurrentVersion) {
		return &api.NextAction{Kind: api.NextActionKindReview, Sentence: "Check this doc"}
	}
	if v.Must > 0 {
		return &api.NextAction{
			Kind:     api.NextActionKindFix,
			Sentence: fmt.Sprintf("Fix %d MUST finding%s", v.Must, plural(v.Must)),
		}
	}
	if a := in.Adopt; a != nil && (a.Type != nil || a.Size != nil) {
		return &api.NextAction{Kind: api.NextActionKindAdopt, Sentence: "Write the type and the size into the doc"}
	}
	if v.Result != api.VerdictResultBuildReady {
		return nil
	}
	if in.Hosted && in.ApprovalsNeeded > in.ApprovalsGiven {
		// A doc in review waits for other people: waiting is not an action for this person.
		if in.Status != api.ReviewStatusDraft {
			return nil
		}
		return &api.NextAction{Kind: api.NextActionKindRequestReview, Sentence: "Ask for the reviews this doc needs"}
	}
	if in.Handoffs == 0 {
		return &api.NextAction{Kind: api.NextActionKindHandoff, Sentence: "Hand it to a builder"}
	}
	return nil
}

func stale(v *api.BundleVerdict, current int64) bool {
	return v.Result == api.VerdictResultStale || v.VersionNumber != current
}

func deref(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// short cuts a sentence to fit a button or a status line.
func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// WaiverFor returns the waiver of this bundle that waits for the caller, if there is one.
func WaiverFor(waiting []api.Waiver, bundleID uuid.UUID) *api.Waiver {
	for i, w := range waiting {
		if w.DocId == bundleID {
			return &waiting[i]
		}
	}
	return nil
}
