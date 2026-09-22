# Profiles, templates, and size

A profile is the configuration of one doc type. It holds the template, the checks, the limits
and the policies that decide a verdict. This page says what each part does, and what to change
when a review says something you did not mean.

## Size: how much one doc covers

Every main doc declares a size in its frontmatter:

```markdown
---
type: sdd
title: Payment retries
size: feature
---
```

| Size         | What it covers                            | Example                               |
| ------------ | ----------------------------------------- | ------------------------------------- |
| `feature`    | One change a team ships together.         | Retry a refused card payment.         |
| `app`        | A system with parts that call each other. | The payments service.                 |
| `initiative` | Work that several systems share.          | Move every service to the new ledger. |

Size is not importance, and it is not effort. It is **how much of the world the doc has to
describe**. A doc with no size is a `feature`, the smallest.

Size decides two things.

**Which headings the template requires.** A heading marked `<!-- required -->` is required at
every size. A heading marked `<!-- required: app -->` is required at `app` and `initiative`, and
not at `feature`. The built-in SDD requires Context, Non-goals and Decisions from every doc, and
adds Components, Data model, Interfaces, Failure modes, Limits, Security, Testing and Open
questions from `app` upwards. The built-in PRD requires Problem, Users, Goals, Non-goals and
Requirements from every doc, and adds Dependencies and Open questions from `app` upwards.

**Which checks apply.** A check with `sizes: [app, initiative]` does not run on a feature doc.
The PRD asks for a baseline on a goal, for non-functional requirements and for dependencies only
from `app` upwards. An SDD at `initiative` must also link the bundles it covers.

So a feature doc is not judged by the standard of an app doc. Declaring a size larger than the
work makes Speccy ask for sections nobody needs. Declaring one smaller hides the questions an
implementer will ask.

## PRD or SDD

A **PRD** states the problem, who has it, what must be true, and what is out of scope. It does
not say how the thing is built.

An **SDD** states how it is built: the decisions, the parts, the data, the interfaces and what
happens when something fails. It links up to its PRD with an `implements` link, and the
coherence stage checks that it covers the PRD's requirements and does not contradict them.

One doc can be both only by being neither. Write the PRD, then the SDD that implements it.

## What a profile holds

| Key           | What it does                                                                                                                                                    |
| ------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `key`, `name` | The doc type a frontmatter `type:` selects, and its label.                                                                                                      |
| `template`    | The path of the template. Its `<!-- required -->` markers become the required headings.                                                                         |
| `limits`      | The prose limits: words in a doc, words in a section, words in a sentence, lines in a code block, rows in a table.                                              |
| `links`       | `upstream` says which doc types this one links to, and whether a link is required. `children` says a doc of `min_at` or larger must link the bundles it covers. |
| `trace`       | `prefixes` are the trace ID prefixes this doc uses, such as REQ and DEC. `cover` are the upstream prefixes a downstream doc must reference.                     |
| `verify`      | The post-build gate: `prefixes` are the trace IDs it verifies, and the scan bounds.                                                                             |
| `waivers`     | Who approves a waiver of a SHOULD and of a MUST.                                                                                                                |
| `approvals`   | How many human approvals a bundle needs.                                                                                                                        |
| `divergence`  | How many readers answer the build questions, how many questions, and the themes they cover.                                                                     |
| `grounding`   | `sources` says which domains the grounding stage accepts, their tier, their freshness, and the claim class of a section.                                        |
| `lint`        | `overrides` change the level of a lint rule, or turn it off. `slop_extra` adds phrases to the slop list.                                                        |
| `checks`      | The rubric. Each check has a slug, a level, a stage, and the question a model answers.                                                                          |

## A check

```yaml
- slug: sdd.failure-modes
  level: MUST # MUST blocks the verdict. SHOULD does not. INFO never does.
  stage: rubric # rubric, grounding, divergence, or coherence.
  scope: section # doc, or section.
  sizes: [app, initiative] # empty means every size.
  question: Does the doc say what happens when each dependency fails?
  pass_when: Every dependency has a named failure and a named behaviour.
```

The **level** decides whether a failed check stops the verdict. The **stage** decides when it
runs and what it reads. The **sizes** decide which docs it applies to.

## Three changes people usually want

**A section that only large docs need.** Mark it in the template, not in the checks:

```markdown
## Data model <!-- required: app -->
```

**A check that fires on every doc in a repo you have just adopted.** Relax it while the repo
catches up, with `adoption.relaxed` in `.speccy.yaml`. The check still reports, and it does not
block. See [adoption.md](adoption.md).

**A limit that people waive again and again.** A waiver that keeps coming back is a limit that
is wrong. Change the limit:

```yaml
limits:
  max_section_words: 1200
```

Every save is a new version, and an earlier review keeps the version it used. The **Versions**
list on the profile page diffs any two versions and rolls an old one forward.
