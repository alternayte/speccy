// Package approval is the bundle status aggregate (SDD §9.5, DEC-024, DEC-008): draft,
// in_review, approved, superseded. Status is a human workflow; the verdict is computed. decide
// and evolve are pure.
package approval

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/kernel"
)

// StreamType is the stream type of a bundle's status. The stream ID is the bundle ID.
const StreamType = "bundle_status"

// Event types (SDD §11.3).
const (
	ReviewRequested  = "ReviewRequested"
	ReviewerAssigned = "ReviewerAssigned"
	ApprovalGiven    = "ApprovalGiven"
	ApprovalsRevoked = "ApprovalsRevoked"
	Superseded       = "Superseded"
)

// Statuses (§9.5). A bundle with no stream is a draft.
const (
	Draft          = "draft"
	InReview       = "in_review"
	StatusApproved = "approved"
	StatusSuper    = "superseded"
)

// Approval is one person's approval of one version.
type Approval struct {
	By      string    `json:"by"`
	Version uuid.UUID `json:"version"`
	At      time.Time `json:"at"`
}

// State is a bundle's status.
type State struct {
	Status            string     `json:"status"`
	Reviewers         []string   `json:"reviewers"`
	Approvals         []Approval `json:"approvals"`
	ApprovedVersion   *uuid.UUID `json:"approved_version,omitempty"`
	ReviewRequestedAt *time.Time `json:"review_requested_at,omitempty"`
	ApprovedAt        *time.Time `json:"approved_at,omitempty"`
	SupersededBy      *uuid.UUID `json:"superseded_by,omitempty"`
}

// Current returns the status, draft for a new bundle.
func (s State) Current() string {
	if s.Status == "" {
		return Draft
	}
	return s.Status
}

type requestedV1 struct {
	V  int       `json:"v"`
	By string    `json:"by"`
	At time.Time `json:"at"`
}

type userV1 struct {
	V    int    `json:"v"`
	User string `json:"user"`
}

type approvalV1 struct {
	V        int       `json:"v"`
	By       string    `json:"by"`
	Version  uuid.UUID `json:"version"`
	At       time.Time `json:"at"`
	Approved bool      `json:"approved"` // the approvals reached approvals.required
}

type revokedV1 struct {
	V       int       `json:"v"`
	Version uuid.UUID `json:"version"` // the new version that revoked them
}

type supersededV1 struct {
	V  int       `json:"v"`
	By uuid.UUID `json:"by"`
}

// RequestReview is the author's request for review, with the reviewers to assign (REQ-090).
type RequestReview struct {
	By        string
	CanEdit   bool // an author or an admin
	Reviewers []string
	At        time.Time
}

// DecideRequestReview moves a draft to in_review and assigns reviewers. In review, it assigns
// more reviewers.
func DecideRequestReview(s State, c RequestReview) ([]es.Event, error) {
	if !c.CanEdit {
		return nil, kernel.Forbidden("not_author", "Only an author of this bundle or an admin can request a review.")
	}
	var out []es.Event
	switch s.Current() {
	case Draft:
		out = append(out, es.NewEvent(ReviewRequested, requestedV1{V: 1, By: c.By, At: c.At}))
	case InReview:
	case StatusApproved:
		return nil, kernel.Conflict("already_approved", "This bundle is approved. A change to it starts a new review.")
	default:
		return nil, kernel.Conflict("superseded", "This bundle is superseded by another bundle.")
	}
	for _, r := range c.Reviewers {
		if r != "" && !slices.Contains(s.Reviewers, r) {
			out = append(out, es.NewEvent(ReviewerAssigned, userV1{V: 1, User: r}))
		}
	}
	return out, nil
}

// Approve is one approval of the current version (REQ-076).
type Approve struct {
	By      string
	Author  bool
	Version uuid.UUID
	// BuildReady is true when the current version has a current build_ready verdict.
	BuildReady bool
	Required   int
	At         time.Time
}

// DecideApprove records an approval. The author cannot approve (T-012). Approval needs a
// current Build Ready verdict. The bundle is approved when the approvals of the current
// version reach approvals.required.
func DecideApprove(s State, c Approve) ([]es.Event, error) {
	if c.Author {
		return nil, kernel.Forbidden("author_cannot_approve", "An author cannot approve their own bundle. Ask a reviewer.")
	}
	if s.Current() != InReview {
		return nil, kernel.Conflict("not_in_review", "Only a bundle in review takes approvals. It is %s.", s.Current())
	}
	if !c.BuildReady {
		return nil, kernel.Conflict("not_build_ready", "Approval needs a current Build Ready verdict. Run the review, and fix the MUST findings first.")
	}
	n := 1
	for _, a := range s.Approvals {
		if a.Version == c.Version {
			if a.By == c.By {
				return nil, kernel.Conflict("approved_by_you", "You approved this version already.")
			}
			n++
		}
	}
	required := max(c.Required, 1)
	return []es.Event{es.NewEvent(ApprovalGiven, approvalV1{V: 1, By: c.By, Version: c.Version, At: c.At, Approved: n >= required})}, nil
}

// DecideContentChanged revokes the approvals of older versions when the bundle gets a new
// version (REQ-077). An approved bundle goes back to in_review (T-013).
func DecideContentChanged(s State, version uuid.UUID) ([]es.Event, error) {
	stale := false
	for _, a := range s.Approvals {
		if a.Version != version {
			stale = true
		}
	}
	if !stale && s.Current() != StatusApproved {
		return nil, nil
	}
	if s.ApprovedVersion != nil && *s.ApprovedVersion == version {
		return nil, nil
	}
	return []es.Event{es.NewEvent(ApprovalsRevoked, revokedV1{V: 1, Version: version})}, nil
}

// DecideSupersede marks the bundle superseded by another bundle (§9.5).
func DecideSupersede(s State, by uuid.UUID) ([]es.Event, error) {
	if s.Current() == StatusSuper && s.SupersededBy != nil && *s.SupersededBy == by {
		return nil, nil
	}
	return []es.Event{es.NewEvent(Superseded, supersededV1{V: 1, By: by})}, nil
}

// Evolve applies one event.
func Evolve(s State, e es.Event) State {
	switch e.Type {
	case ReviewRequested:
		var p requestedV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Status = InReview
		at := p.At
		s.ReviewRequestedAt = &at
	case ReviewerAssigned:
		var p userV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Reviewers = append(append([]string{}, s.Reviewers...), p.User)
	case ApprovalGiven:
		var p approvalV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Approvals = append(append([]Approval{}, s.Approvals...), Approval{By: p.By, Version: p.Version, At: p.At})
		if p.Approved {
			s.Status = StatusApproved
			v, at := p.Version, p.At
			s.ApprovedVersion, s.ApprovedAt = &v, &at
		}
	case ApprovalsRevoked:
		var p revokedV1
		_ = json.Unmarshal(e.Payload, &p)
		var keep []Approval
		for _, a := range s.Approvals {
			if a.Version == p.Version {
				keep = append(keep, a)
			}
		}
		s.Approvals = keep
		if s.Current() == StatusApproved {
			s.Status = InReview
		}
		s.ApprovedVersion, s.ApprovedAt = nil, nil
	case Superseded:
		var p supersededV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Status = StatusSuper
		by := p.By
		s.SupersededBy = &by
	}
	return s
}
