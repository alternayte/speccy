---
type: sdd
title: <System or feature name>
links:
  - kind: implements
    target: <PRD bundle>
---

# <System or feature name>

## Context <!-- required -->

<What this design builds, in two or three sentences. Link to the PRD for the why.>

## Non-goals <!-- required -->

- <One thing this system will not do.>

## Decisions <!-- required -->

- **DEC-001:** <The decision.> Rejected: <an alternative, and why.>

## Components <!-- required: app -->

<Each component and its one responsibility.>

## Data model <!-- required: app -->

<Each stored entity with its fields, types, and constraints, or a link to an asset.>

## Interfaces <!-- required: app -->

<Each interface with its inputs, outputs, and errors, or a link to an asset such as an OpenAPI file.>

## Failure modes <!-- required: app -->

<For each external dependency: what happens when it fails.>

## Limits <!-- required: app -->

<Sizes, rates, and timeouts, each with a number.>

## Security <!-- required: app -->

<Authentication, authorisation, secrets, and untrusted input.>

## Observability

<Logs, metrics, and alerts.>

## Testing <!-- required: app -->

<What proves each MUST requirement.>

## Migration

<Data and rollout migration, or "Not applicable".>

## Delivery

<Milestones in order, each with exit criteria.>

## Open questions <!-- required: app -->

<Each open question, or "None".>
