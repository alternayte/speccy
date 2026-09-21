---
type: sdd
title: Audit log retention
---

# Audit log retention

## Context

The audit log keeps every entry forever. This design deletes entries older than 400 days.

## Non-goals

- A change to what the audit log records.

## Decisions

- **DEC-001:** A nightly job deletes entries older than 400 days. Rejected: table partitions by
  month, because the table is 3 GB and a job is enough.

## Components

The retention job runs once a night and deletes old entries in batches of 10,000.

## Data model

No new table. The job reads `audit_entry.created_at`, which has an index.

## Interfaces

The job has no API. It writes one log line per run with the count it deleted.

## Failure modes

When the database is down, the job fails and runs again the next night.

## Limits

One run deletes at most 5,000,000 entries.

## Security

The job uses a database role that can delete from `audit_entry` only.

## Testing

A test inserts entries 399 and 401 days old, runs the job, and expects only the newer entry.

## Open questions

None.
