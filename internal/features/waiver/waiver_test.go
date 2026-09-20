package waiver

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/kernel"
)

func apply(s State, events []es.Event) State {
	for _, e := range events {
		s = Evolve(s, e)
	}
	return s
}

func requested(p profile.Policy) State {
	ev, err := DecideRequest(State{}, Request{ID: uuid.New(), Check: "sdd.limits", Level: kernel.Must, Section: []string{"Limits"},
		SectionHash: "sha256:a", Reason: "The provider sets these limits, see the asset.", By: "author", Policy: p})
	if err != nil {
		panic(err)
	}
	return apply(State{}, ev)
}

// T-011
func TestWaiverPolicy_Table(t *testing.T) {
	author := Approver{UserID: "author", Author: true}
	member := Approver{UserID: "member"}
	member2 := Approver{UserID: "member2"}
	maintainer := Approver{UserID: "keeper", Maintainer: true}
	admin := Approver{UserID: "admin", Admin: true}
	for _, c := range []struct {
		policy profile.Policy
		steps  []Approver // approvals in order
		ok     []bool     // whether each approval is accepted
		final  string     // status after the steps
	}{
		{profile.Policy{Name: "any_member"}, []Approver{author}, []bool{true}, StatusApproved},
		{profile.Policy{Name: "non_author"}, []Approver{author, member}, []bool{false, true}, StatusApproved},
		{profile.Policy{Name: "n_approvals", NApprovals: 2}, []Approver{member, member, author, member2}, []bool{true, false, false, true}, StatusApproved},
		{profile.Policy{Name: "n_approvals", NApprovals: 2}, []Approver{member}, []bool{true}, StatusRequested},
		{profile.Policy{Name: "maintainer"}, []Approver{member, author, maintainer}, []bool{false, false, true}, StatusApproved},
		{profile.Policy{Name: "maintainer"}, []Approver{admin}, []bool{true}, StatusApproved},
	} {
		s := requested(c.policy)
		for i, a := range c.steps {
			ev, err := DecideApprove(s, a)
			if (err == nil) != c.ok[i] {
				t.Errorf("%s: approval %d by %s: err %v, want accepted %v", c.policy.Name, i, a.UserID, err, c.ok[i])
			}
			s = apply(s, ev)
		}
		if s.Status != c.final {
			t.Errorf("%s: status %s, want %s", c.policy.Name, s.Status, c.final)
		}
	}
	// forbidden: nobody can even request it.
	if _, err := DecideRequest(State{}, Request{Reason: "A reason that is long enough to pass.", Policy: profile.Policy{Name: "forbidden"}}); err == nil {
		t.Error("a forbidden check took a waiver request")
	}
}

func TestWaiver_ReasonAndInvalidation(t *testing.T) {
	if _, err := DecideRequest(State{}, Request{Reason: "too short", Policy: profile.Policy{Name: "any_member"}}); err == nil {
		t.Error("a reason under 20 characters was accepted (REQ-072)")
	}
	s := requested(profile.Policy{Name: "any_member"})
	s = apply(s, must(DecideApprove(s, Approver{UserID: "m"})))
	if ev, _ := DecideInvalidate(s, "sha256:a"); len(ev) != 0 {
		t.Error("an unchanged section invalidated the waiver")
	}
	s = apply(s, must(DecideInvalidate(s, "sha256:b")))
	if s.Status != StatusInvalidated {
		t.Errorf("status %s after a section change, want invalidated", s.Status)
	}
}

func TestWaiver_RejectionCarriesAReason(t *testing.T) {
	s := requested(profile.Policy{Name: "any_member"})
	if _, err := DecideReject(s, Approver{UserID: "m"}, "too short"); err == nil {
		t.Error("a rejection reason under 20 characters was accepted")
	}
	const why = "The check applies here. Split the section instead."
	s = apply(s, must(DecideReject(s, Approver{UserID: "m"}, why)))
	if s.Status != StatusRejected || s.DecisionReason != why {
		t.Errorf("status %s, reason %q; want rejected and the reason", s.Status, s.DecisionReason)
	}
}

func must(ev []es.Event, err error) []es.Event {
	if err != nil {
		panic(err)
	}
	return ev
}
