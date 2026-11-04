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
	if ev, _ := DecideInvalidate(s, true, EndedSectionChanged); len(ev) != 0 {
		t.Error("an unchanged section invalidated the waiver")
	}
	s = apply(s, must(DecideInvalidate(s, false, EndedSectionChanged)))
	if s.Status != StatusInvalidated || s.EndedBecause != EndedSectionChanged {
		t.Errorf("status %s because %q after a section change, want invalidated because the section changed", s.Status, s.EndedBecause)
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

// Two ways out: an Acknowledgement and a request decided alone close at once; a waiver in
// hosted mode, an upstream request and a send-back wait for an approver.
func TestDecideRequest_AloneClosesAtOnce(t *testing.T) {
	maint := profile.Policy{Name: "maintainer"}
	reason := "The checkout UI doc owns this requirement."
	for _, c := range []struct {
		name  string
		scope string
		alone bool
		want  string
	}{
		{"waiver, hosted", ScopeCheck, false, StatusRequested},
		{"waiver, local", ScopeCheck, true, StatusApproved},
		{"acknowledgement", ScopeTrace, true, StatusApproved},
		{"standalone", ScopeStandalone, true, StatusApproved},
		{"upstream request", ScopeUpstream, true, StatusRequested},
		{"send-back", ScopeSendBack, true, StatusRequested},
	} {
		ev, err := DecideRequest(State{}, Request{ID: uuid.New(), Check: "trace.coverage", Scope: c.scope, Level: kernel.Must,
			Reason: reason, By: "author", Policy: maint, Alone: c.alone})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s := apply(State{}, ev); s.Status != c.want {
			t.Errorf("%s: status %s, want %s", c.name, s.Status, c.want)
		}
	}
	if _, err := DecideRequest(State{}, Request{ID: uuid.New(), Check: "sdd.limits", Scope: ScopeCheck, Level: kernel.Must,
		Reason: reason, By: "author", Policy: profile.Policy{Name: "forbidden"}, Alone: true}); err == nil {
		t.Error("a forbidden policy took a waiver decided alone")
	}
}

// A request that waits from before an Acknowledgement closed at once gets Speccy's approval.
func TestDecideNoLongerNeeded(t *testing.T) {
	ack, _ := DecideRequest(State{}, Request{ID: uuid.New(), Check: "trace.coverage", Scope: ScopeTrace, Level: kernel.Must,
		Reason: "The checkout UI doc owns this requirement.", By: "author", Policy: profile.Policy{Name: "maintainer"}})
	s := apply(State{}, ack)
	ev, err := DecideNoLongerNeeded(s)
	if err != nil {
		t.Fatal(err)
	}
	if s = apply(s, ev); s.Status != StatusApproved || s.DecidedBy != SpeccyActor {
		t.Errorf("status %s by %s, want approved by %s", s.Status, s.DecidedBy, SpeccyActor)
	}
	if _, err := DecideNoLongerNeeded(requested(profile.Policy{Name: "maintainer"})); err == nil {
		t.Error("Speccy approved a check waiver, which still needs an approver")
	}
}
