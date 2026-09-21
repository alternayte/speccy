---
type: sdd
title: Bank transfer retries — design
links:
  - kind: implements
    target: payments-prd
---

# Bank transfer retries — design

## Context

This design adds retries to the payment service for failed payments. It covers REQ-001 and
REQ-002 of the PRD.

## Non-goals

- A queue for payments that fail after every retry.

## Decisions

- **DEC-001:** The payment service retries bank transfers as well as card payments. Rejected:
  card payments only, because transfers fail as often.
- **DEC-002:** Each attempt uses a new idempotency key. Rejected: the order ID as the key,
  because the bank rejects a reused key.

## Retries

The service retries a timeout or an HTTP 503 up to 3 times (REQ-001), with waits of 200 ms,
400 ms, and 800 ms.

## Failure modes

When the provider is down, each payment fails after 4 attempts, and checkout shows an error.

## Open questions

None.
