# Speccy

**Speccy reviews your specs and answers one question: could someone build this without coming back to ask what you meant?**

The answer is a verdict — Build Ready or Not Build Ready — and it is computed, not guessed. Models find problems. Speccy decides.

![The bundle screen: the control row with the verdict, the doc with its findings marked, and the findings rail.](site/src/assets/shots/guide-editor.png)

**Documentation: [speccy-docs.pages.dev](https://speccy-docs.pages.dev)**

## What it does

- **Finds the ambiguity, not only the missing sections.** Three independent readers answer the same questions from your doc. Where they disagree, the doc is ambiguous. Where none of them finds an answer, it has a gap.
- **Checks the writing** with fixed rules in under a second: placeholders, missing sections, broken links, duplicate IDs, filler phrases, long sentences.
- **Checks the facts.** Every claim about the outside world needs a source, or Speccy marks it unverified.
- **Keeps two docs honest.** A PRD and the SDD that implements it must cover each other, must not contradict each other, and must not repeat each other.
- **Verifies the build afterwards.** `speccy verify` reads the repo and says where each requirement lives in the code and its tests, and where the code contradicts it.
- **Never lets a model decide.** The verdict is a pure function of the findings, the waivers, the links and the version.
- **One binary.** The app, the CLI, the terminal UI and the MCP server. No account, no server, no telemetry.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/alternayte/speccy/main/install.sh | sh
```

The script picks the release for your machine, checks it against the published checksum, and puts `speccy` on your PATH. The [releases page](https://github.com/alternayte/speccy/releases) has the archives for installing by hand and for Windows.

## Start

```sh
cd <the folder with your specs>
speccy
```

That opens the app on 127.0.0.1. A spec doc is a markdown file that names its type:

```markdown
---
type: sdd
title: Payment retries
size: feature
---
```

Then follow the Guide, [From a blank page to a build packet](https://speccy-docs.pages.dev/tutorials/blank-page-to-build-packet/), or [review docs you already have](https://speccy-docs.pages.dev/how-to/review-docs-you-already-have/).

## Documentation

| Section                                                                                   | What it holds                                                                                   |
| ----------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| [Tutorials](https://speccy-docs.pages.dev/tutorials/blank-page-to-build-packet/)          | One doc from a blank page to a build packet, and a PRD with the SDD that implements it.         |
| [How-to guides](https://speccy-docs.pages.dev/how-to/review-docs-you-already-have/)       | Adopt a repo, close a coverage gap, ask for a waiver, hand a spec to a coding agent, run in CI. |
| [Concepts](https://speccy-docs.pages.dev/concepts/verdict/)                               | The verdict, the review pipeline, profiles, waivers, traceability, and the guarantees.          |
| [Reference](https://speccy-docs.pages.dev/reference/checks/)                              | Every check, command, key, Action input, MCP tool and API operation.                            |
| [Operations](https://speccy-docs.pages.dev/operations/back-up-and-restore/)               | Back up and restore, upgrade, the master key, and the model budget.                             |

[docs/decisions.md](docs/decisions.md) records each design decision, its alternative, and its reason.

## Contributing

You need Go, Node 24 or later, and `just`. `just test-pg` and `just verify` also need Docker.

```sh
git clone https://github.com/alternayte/speccy && cd speccy
just dev        # the Go server and Vite together, on http://127.0.0.1:5173
just docs-dev   # the docs site, on http://127.0.0.1:4321
just verify     # the gate a pull request must pass
```

`just build` writes `bin/speccy`.

## Licence

AGPL-3.0. See [LICENSE](LICENSE).
