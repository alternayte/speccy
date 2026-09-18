---
type: prd
title: Payment retries
---

# Payment retries

## Problem

Card payments fail on a temporary provider error. In June, 2.1% of payment attempts failed
with a timeout or an HTTP 503 from the provider (ticket PAY-231). Each failure asks the
customer to pay again, and 38% of them leave the checkout.

## Users

Customers who pay by card at checkout.

## Goals

- **G-1:** Checkout abandonment after a provider error falls from 38% to 10% by the end of Q3.

## Non-goals

- Retries for bank transfers.
- A change to the checkout screens.

## Requirements

- **REQ-001:** The system MUST retry a card payment that fails with a timeout or an HTTP 503.
  Acceptance: a test provider that fails once with a 503 gives one successful payment.
- **REQ-002:** The system MUST NOT charge a customer twice for one order.
  Acceptance: a test provider that times out after it charges gives one charge.
- **REQ-003:** The system SHOULD show "Your payment is taking longer than usual" after 5 seconds.

## Dependencies

The card provider's idempotency keys.

## Open questions

None.
