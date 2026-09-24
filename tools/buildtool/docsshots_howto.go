package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alternayte/speccy/internal/source"
)

// linkPatterns is the .speccy.yaml of the served folder: one link pattern, so a jira: link in
// the external links picture has a URL.
const linkPatterns = "link_patterns:\n  jira: https://acme.atlassian.net/browse/{key}\n"

// refundCode is the code folder that the verification picture reads: the refund of REQ-001
// with its test, and no customer email, so the run has more than one outcome.
var refundCode = map[string]string{
	"refund/refund.go": `package refund

import "errors"

// ErrNotPaid is the answer for an order that has no payment to refund.
var ErrNotPaid = errors.New("the order is not paid")

// Order is a customer order with its payment.
type Order struct {
	ID        string
	PaymentID string
	Status    string
}

// Provider is the payment provider's refund API.
type Provider interface {
	Refund(paymentID string) error
}

// RefundOrder refunds a paid order. The Refund button on the order page calls it.
func RefundOrder(p Provider, o *Order) error {
	if o.Status != "paid" {
		return ErrNotPaid
	}
	if err := p.Refund(o.PaymentID); err != nil {
		return err
	}
	o.Status = "refunding"
	return nil
}
`,
	"refund/refund_test.go": `package refund

import "testing"

type fakeProvider struct{ refunded []string }

func (f *fakeProvider) Refund(id string) error { f.refunded = append(f.refunded, id); return nil }

func TestRefundOrder(t *testing.T) {
	p := &fakeProvider{}
	o := &Order{ID: "o-1", PaymentID: "pay-1", Status: "paid"}
	if err := RefundOrder(p, o); err != nil {
		t.Fatal(err)
	}
	if o.Status != "refunding" || len(p.refunded) != 1 {
		t.Fatalf("order %+v, refunds %v", o, p.refunded)
	}
}
`,
}

// howto captures the pictures of the how-to guides that need a state of their own: the
// external links of a doc, a waiver request as an approver sees it, and a verification run.
// It runs after linked, whose Refunds PRD has trace IDs by then.
func (d *shots) howto(s *server, dir, work string) error {
	u := func(p string) string { return s.base + p }
	ds, err := s.docs()
	if err != nil {
		return err
	}
	prd, sdd, draft := ds["refunds/PRD - Refunds"], ds["refunds/SDD - Refunds"], ds["draft-prd"]
	if prd.ID == "" || sdd.ID == "" || draft.ID == "" {
		return errors.New("howto needs the Refunds docs of linked and draft-prd")
	}

	// 1. External links: a code target, an issue through a link pattern, and a page. A review
	// reads each one and gives it a state.
	file := filepath.Join(dir, "refunds", "SDD - Refunds.md")
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	for _, l := range [][2]string{
		{"implemented-by", "github:alternayte/speccy#internal/features/review"},
		{"references", "jira:PAY-412"},
		{"references", "https://docs.stripe.com/refunds"},
	} {
		if src, err = source.AddLink(src, l[0], l[1]); err != nil {
			return err
		}
	}
	if err := os.WriteFile(file, src, 0o644); err != nil {
		return err
	}
	if err := s.waitVersion(sdd.ID, sdd.Version.ID); err != nil {
		return err
	}
	var run struct{ ID string }
	if err := s.call("POST", "/docs/"+sdd.ID+"/runs", nil, &run); err != nil {
		return err
	}
	if err := s.waitRun(run.ID); err != nil {
		return err
	}
	if err := d.png("link-to-an-issue-a-page-or-the-code-external-links", u(sdd.page()+"/trace"), func() error {
		_, err := d.ab("eval", `[...document.querySelectorAll("h2")].find(h=>h.textContent.trim()==="External links")?.scrollIntoView({block:"start"})`)
		return err
	}); err != nil {
		return err
	}

	// 2. A waiver request on a MUST finding of the draft, as the rail shows it to an approver.
	fresh, err := s.doc(draft.ID)
	if err != nil {
		return err
	}
	if fresh.Verdict == nil {
		return errors.New("draft-prd has no verdict")
	}
	var l struct {
		Items []struct {
			ID      string `json:"id"`
			Level   string `json:"level"`
			TraceID string `json:"trace_id"`
		}
	}
	if err := s.call("GET", "/runs/"+fresh.Verdict.RunID+"/findings", nil, &l); err != nil {
		return err
	}
	finding := ""
	for _, f := range l.Items {
		if f.Level == "MUST" && f.TraceID == "" {
			finding = f.ID
			break
		}
	}
	if finding == "" {
		return errors.New("draft-prd has no MUST finding to waive")
	}
	var w struct{ ID string }
	if err := s.call("POST", "/docs/"+draft.ID+"/waivers", map[string]any{
		"finding_id": finding,
		"reason":     "The first release covers one market, so this section waits for the second release.",
	}, &w); err != nil {
		return err
	}
	if err := d.png("ask-for-and-approve-a-waiver-card", u(draft.page()+"?view=preview&waiver="+w.ID), nil); err != nil {
		return err
	}

	// 3. A verification run of the Refunds PRD against a folder: REQ-001 has code and a test,
	// and REQ-002 has no code.
	code := filepath.Join(work, "refund-service")
	for name, body := range refundCode {
		p := filepath.Join(code, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	var v struct{ ID string }
	if err := s.call("POST", "/docs/"+prd.ID+"/verifications", map[string]any{"path": code}, &v); err != nil {
		return err
	}
	if err := s.waitVerification(v.ID); err != nil {
		return err
	}
	return d.png("verify-a-build-panel", u(prd.page()+"?view=preview"), func() error {
		if err := d.tab("History"); err != nil {
			return err
		}
		_, err := d.ab("wait", "800")
		return err
	})
}

// waitVerification waits until a verification run is done.
func (s *server) waitVerification(id string) error {
	for deadline := time.Now().Add(15 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		var r struct{ Status, Error string }
		if err := s.call("GET", "/verifications/"+id, nil, &r); err != nil {
			return err
		}
		switch r.Status {
		case "done":
			return nil
		case "failed":
			return fmt.Errorf("verification %s failed: %s", id, r.Error)
		}
	}
	return errors.New("the verification did not end in 15 minutes")
}
