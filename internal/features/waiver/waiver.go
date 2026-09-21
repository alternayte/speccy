// Package waiver is the waiver aggregate (SDD §9, DEC-008): an approved exception for one check
// in one section. decide and evolve are pure; the service stores the stream, writes approved
// waivers to the doc's sidecar (DEC-009), and invalidates a waiver when its section
// changes (REQ-074).
package waiver

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/kernel"
)

// StreamType is the stream type of a waiver.
const StreamType = "waiver"

// Event types (SDD §11.3).
const (
	Requested   = "WaiverRequested"
	Approved    = "WaiverApproved"
	Rejected    = "WaiverRejected"
	Invalidated = "WaiverInvalidated"
)

// Status values (§9.2).
const (
	StatusRequested   = "requested"
	StatusApproved    = "approved"
	StatusRejected    = "rejected"
	StatusInvalidated = "invalidated"
)

// MinReason is REQ-072's shortest reason.
const MinReason = 20

// State is a waiver.
type State struct {
	ID          uuid.UUID      `json:"id"`
	BundleID    uuid.UUID      `json:"bundle_id"`
	Check       string         `json:"check"`
	Level       kernel.Level   `json:"level"`
	Section     []string       `json:"section"`
	SectionHash string         `json:"section_hash"`
	Reason      string         `json:"reason"`
	Policy      profile.Policy `json:"policy"`
	Status      string         `json:"status"`
	RequestedBy string         `json:"requested_by"`
	Approvals   []string       `json:"approvals"`
	DecidedBy   string         `json:"decided_by"`
	// DecisionReason is why the waiver is rejected. Only a rejection has one.
	DecisionReason string `json:"decision_reason"`
}

// Approver is who acts on a waiver, and their relation to the bundle and its profile.
type Approver struct {
	UserID     string
	Author     bool // an author of the bundle
	Maintainer bool // a maintainer of the bundle's profile
	Admin      bool
}

// Request is the command to ask for a waiver (REQ-072).
type Request struct {
	ID          uuid.UUID
	BundleID    uuid.UUID
	Check       string
	Level       kernel.Level
	Section     []string
	SectionHash string
	Reason      string
	By          string
	Policy      profile.Policy
}

type requestedV1 struct {
	V int `json:"v"`
	Request
}

type approvedV1 struct {
	V     int    `json:"v"`
	By    string `json:"by"`
	Final bool   `json:"final"` // the approvals reached the policy
}

type byV1 struct {
	V      int    `json:"v"`
	By     string `json:"by"`
	Hash   string `json:"hash,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// DecideRequest checks a new request.
func DecideRequest(s State, r Request) ([]es.Event, error) {
	if s.Status != "" {
		return nil, kernel.Conflict("waiver_exists", "This waiver exists already.")
	}
	if len([]rune(strings.TrimSpace(r.Reason))) < MinReason {
		return nil, kernel.Invalid("reason_too_short", "Give a reason of at least %d characters, so a reviewer can judge it.", MinReason)
	}
	if r.Policy.Name == "forbidden" {
		return nil, kernel.Forbidden("waiver_forbidden", "The profile does not allow a waiver for %s. Fix the doc instead.", r.Check)
	}
	return []es.Event{es.NewEvent(Requested, requestedV1{V: 1, Request: r})}, nil
}

// CanApprove is §9.1: whether a approves or rejects a waiver with policy p. It does not count
// earlier approvals; n_approvals counts in DecideApprove.
func CanApprove(p profile.Policy, a Approver) (bool, string) {
	switch p.Name {
	case "any_member":
		return true, ""
	case "non_author", "n_approvals":
		if a.Author {
			return false, "An author cannot approve a waiver of their own bundle."
		}
		return true, ""
	case "maintainer":
		if a.Maintainer || a.Admin {
			return true, ""
		}
		return false, "Only a maintainer of this doc type's profile can approve this waiver."
	case "forbidden":
		return false, "Nobody can approve a waiver for this check."
	}
	return false, "The profile names an unknown waiver policy."
}

// DecideApprove adds an approval. It is final when the policy is met: one approval, or N
// distinct non-author approvals for n_approvals.
func DecideApprove(s State, a Approver) ([]es.Event, error) {
	if s.Status != StatusRequested {
		return nil, kernel.Conflict("waiver_not_requested", "This waiver is %s, so it takes no approval.", orNone(s.Status))
	}
	if ok, why := CanApprove(s.Policy, a); !ok {
		return nil, kernel.Forbidden("waiver_policy", "%s", why)
	}
	if slices.Contains(s.Approvals, a.UserID) {
		return nil, kernel.Conflict("waiver_approved_by_you", "You approved this waiver already.")
	}
	need := 1
	if s.Policy.Name == "n_approvals" && s.Policy.NApprovals > 1 {
		need = s.Policy.NApprovals
	}
	final := len(s.Approvals)+1 >= need
	return []es.Event{es.NewEvent(Approved, approvedV1{V: 1, By: a.UserID, Final: final})}, nil
}

// DecideReject rejects a requested waiver, with a reason the requester reads. The people who
// can approve it can reject it.
func DecideReject(s State, a Approver, reason string) ([]es.Event, error) {
	if s.Status != StatusRequested {
		return nil, kernel.Conflict("waiver_not_requested", "This waiver is %s, so it cannot be rejected.", orNone(s.Status))
	}
	if ok, why := CanApprove(s.Policy, a); !ok {
		return nil, kernel.Forbidden("waiver_policy", "%s", why)
	}
	if len([]rune(strings.TrimSpace(reason))) < MinReason {
		return nil, kernel.Invalid("reason_too_short", "Give a reason of at least %d characters, so the author knows what to change.", MinReason)
	}
	return []es.Event{es.NewEvent(Rejected, byV1{V: 1, By: a.UserID, Reason: strings.TrimSpace(reason)})}, nil
}

// DecideInvalidate ends an approved waiver when its section hash changed (REQ-074).
func DecideInvalidate(s State, currentHash string) ([]es.Event, error) {
	if s.Status != StatusApproved || s.SectionHash == currentHash {
		return nil, nil
	}
	return []es.Event{es.NewEvent(Invalidated, byV1{V: 1, By: "system", Hash: currentHash})}, nil
}

// Evolve applies one event.
func Evolve(s State, e es.Event) State {
	switch e.Type {
	case Requested:
		var p requestedV1
		_ = json.Unmarshal(e.Payload, &p)
		r := p.Request
		return State{ID: r.ID, BundleID: r.BundleID, Check: r.Check, Level: r.Level, Section: r.Section, SectionHash: r.SectionHash,
			Reason: strings.TrimSpace(r.Reason), Policy: r.Policy, Status: StatusRequested, RequestedBy: r.By, Approvals: []string{}}
	case Approved:
		var p approvedV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Approvals = append(append([]string{}, s.Approvals...), p.By)
		if p.Final {
			s.Status, s.DecidedBy = StatusApproved, p.By
		}
	case Rejected:
		var p byV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Status, s.DecidedBy, s.DecisionReason = StatusRejected, p.By, p.Reason
	case Invalidated:
		s.Status = StatusInvalidated
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "not requested"
	}
	return s
}
