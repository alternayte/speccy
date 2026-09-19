---
type: sdd
title: Payment retries — restated design
links:
  - kind: implements
    target: payments-prd
trace:
  - id: REQ-003
    status: out_of_scope
    reason: The checkout page shows this message. This design covers the payment service only.
    acknowledged_by: nathan
---

# Payment retries — restated design

## Context

Card payments fail on a temporary provider error. In June, 2.1% of payment attempts failed
with a timeout or an HTTP 503 from the provider (ticket PAY-231). Each failure asks the
customer to pay again, and 38% of them leave the checkout.

## Non-goals

- Retries for bank transfers.

## Retries

The system MUST retry a card payment that fails with a timeout or an HTTP 503 (REQ-001). The
system MUST NOT charge a customer twice for one order (REQ-002).

## Failure modes

When the provider is down, each payment fails after 4 attempts, and checkout shows an error.

## Open questions

None.
