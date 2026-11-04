// Package nextaction names the one thing a person must do next on a bundle. The server owns
// the rule, so the web app, the TUI and the MCP server all say the same sentence.
package nextaction

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/http/api"
)

// Inputs are the signals the rule reads. The bundles list fills the cheap ones. One bundle
// fills the rest, so its next action counts the questions screen exactly.
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
	// Questions is the count of items on the questions screen: everything that blocks Build
	// Ready and needs the author. The list counts the verdict's MUST findings and blocking
	// threads instead.
	Questions int
	// Recheck is true when a MUST check of the verdict's run has no answer. One bundle only.
	Recheck bool
	// CanEdit is false for a person who only reads the bundle: they get no author action.
	CanEdit bool
	// Hosted is true when a team uses this server. Local mode has one person, so it asks for
	// no approval.
	Hosted bool
	// Approvals is how many approvals the profile needs, and how many the bundle has.
	ApprovalsNeeded, ApprovalsGiven int
	// Handoffs is how many builders took the packet of the current version.
	Handoffs int
	// VerifyDue is true when the current version can be verified and its newest verification
	// run is absent, failed or stale. One bundle only.
	VerifyDue bool
}

// ListQuestions is the count of the questions screen that a list row can name without building
// the screen: the verdict's open MUST findings that have an answer, and its blocking threads.
// recheck is true when a MUST check came back with no answer.
func ListQuestions(v *api.BundleVerdict) (n int, recheck bool) {
	if v == nil {
		return 0, false
	}
	u := deref(v.Unanswered)
	return v.Must - u + deref(v.BlockingThreads), u > 0
}

// Of names the next action, or nil when nothing is open. The order runs from what blocks other
// people to what only this person can start (SDD §13.4).
func Of(in Inputs) *api.NextAction {
	if w := in.WaiverWaiting; w != nil {
		id := w.Id
		// An acknowledgement never waits: it closes at once.
		sentence := fmt.Sprintf("Decide the waiver of %s", w.CheckSlug)
		if w.Verification != nil {
			sentence = fmt.Sprintf("Decide the waiver of %s in %s", w.Verification.TraceId, w.Verification.Repo)
		}
		return &api.NextAction{Kind: api.NextActionKindWaiver, Sentence: sentence, WaiverId: &id}
	}
	if !in.CanEdit {
		return nil
	}
	v := in.Verdict
	if v == nil || in.RunError != nil || stale(v, in.CurrentVersion) {
		return &api.NextAction{Kind: api.NextActionKindReview, Sentence: "Check this doc"}
	}
	// The questions screen holds every blocker the author can act on, in one pass.
	if in.Questions > 0 {
		return &api.NextAction{Kind: api.NextActionKindQuestions, Sentence: fmt.Sprintf("Answer %d question%s", in.Questions, plural(in.Questions))}
	}
	// A MUST check with no answer needs another review, not an edit.
	if in.Recheck {
		return &api.NextAction{Kind: api.NextActionKindReview, Sentence: "Check this doc again"}
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
	// The builder's agent verifies when it finishes. When it has not, or a person built, the
	// author verifies. A run that is queued or running is waiting, not an action.
	if in.VerifyDue {
		return &api.NextAction{Kind: api.NextActionKindVerify, Sentence: "Verify the build"}
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

// WaiverFor returns the waiver of this bundle that waits for the caller, if there is one.
func WaiverFor(waiting []api.Waiver, bundleID uuid.UUID) *api.Waiver {
	for i, w := range waiting {
		if w.DocId == bundleID {
			return &waiting[i]
		}
	}
	return nil
}
