---
type: sdd
title: Rate limits
standalone:
  reason: Internal change. No product change.
  acknowledged_by: nathan
---

# Rate limits

## Context

The gateway calls the Stripe API for each payment. Stripe allows 100 read requests per second in live mode, and the gateway stays under that limit with a token bucket. The request log lives in Postgres 17 on the shared cluster.

## Non-goals

- A change to the payment flow.
