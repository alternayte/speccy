# Speccy

**Speccy reviews your specs and answers one question: could someone build this without coming back to ask what you meant?**

The answer is a verdict — Build Ready or Not Build Ready — and it is computed, not guessed. Models find problems. Speccy decides.

![The bundle screen: the verdict bar, the doc with its findings marked, and the findings rail.](docs/images/guide-editor.png)

## What it does

- **Finds the ambiguity, not only the missing sections.** Three independent readers answer the same questions from your doc. Where they disagree, the doc is ambiguous. Where none of them finds an answer, it has a gap.
- **Checks the writing** with fixed rules in under a second: placeholders, missing sections, broken links, duplicate IDs, filler phrases, long sentences.
- **Checks the facts.** Every claim about the outside world needs a source, or it is marked unverified.
- **Keeps two docs honest.** A PRD and the SDD that implements it must cover each other, must not contradict each other, and must not repeat each other.
- **Verifies the build afterwards.** `speccy verify` reads the repo and says where each requirement was implemented and tested, and where the code contradicts it.
- **Never lets a model decide.** The verdict is a pure function of the findings, the waivers, the links and the version. Doc text and search results reach a model as data, never as instructions.
- **One binary.** The app, the CLI, the terminal UI and the MCP server. No account, no server, no telemetry.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/alternayte/speccy/main/install.sh | sh
```

The script picks the release for your machine, **checks it against the published checksum**, and puts `speccy` on your PATH. `SPECCY_VERSION` pins a version and `SPECCY_BIN_DIR` chooses where it goes.

<details>
<summary>By hand, or on Windows</summary>

Download an archive from the [releases page](https://github.com/alternayte/speccy/releases), check it, and put the binary on your PATH:

```sh
VERSION=0.11.0
OS=$(uname -s | tr '[:upper:]' '[:lower:]')          # darwin or linux
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
BASE=https://github.com/alternayte/speccy/releases/download/v$VERSION

curl -fLO $BASE/speccy_${VERSION}_${OS}_${ARCH}.tar.gz
curl -fLO $BASE/checksums.txt
shasum -a 256 --ignore-missing -c checksums.txt      # sha256sum on Linux
tar xzf speccy_${VERSION}_${OS}_${ARCH}.tar.gz
sudo mv speccy /usr/local/bin/
```

On Windows, take the `.zip`, compare it with `Get-FileHash`, and put `speccy.exe` on your `PATH`.

</details>

## Start

```sh
cd <the folder with your specs>
speccy
```

That opens the app on 127.0.0.1. No account, no server. A bundle is a folder with one markdown file that names a type:

```markdown
---
type: sdd
title: Payment retries
size: feature
---
```

Speccy keeps a version every time a file changes, in the app or on disk, in `.speccy/state/`. Do not commit that folder.

| Bundles                                              | Bundle                                                  |
| ---------------------------------------------------- | ------------------------------------------------------- |
| ![The bundles screen](docs/images/guide-bundles.png) | ![The bundle screen](docs/images/guide-editor.png)      |
| **Tour**                                             | **Traceability**                                        |
| ![The tour](docs/images/guide-tour.gif)              | ![The traceability matrix](docs/images/guide-trace.png) |

## Specs you already have

Point Speccy at a repo on GitHub, with no clone:

```sh
speccy add https://github.com/acme/specs/blob/main/docs/prd-payments.md
```

A repo, a folder in one, or a single doc all work, as does `owner/name`. Speccy reads through the GitHub API, writes no file into your folder, and never changes the branch: an edit becomes a pull request. Local mode uses your `gh` login.

For docs with no frontmatter, `speccy init --github` adopts a whole repo in one command: it guesses a type for each spec, writes the mappings into `.speccy.yaml`, puts the checks that fail today into adoption mode so the first verdict is not a wall of red, and writes the workflow below. [docs/adoption.md](docs/adoption.md) has the whole path.

## In a terminal, and in CI

```sh
speccy review docs/ --summary
```

One line per bundle: the verdict, the score, and the top three failing checks. `--format md` for a pull request comment, `--format json` for a script. It needs no server and no setup, and with no model assigned it runs the deterministic checks and says so.

The **GitHub Action** reviews the bundles a pull request changes, posts one summary comment, comments on the changed lines, and sets a check per bundle:

```yaml
# .github/workflows/speccy.yml
name: Speccy
on:
  pull_request:
    paths: ["docs/**", ".speccy.yaml"]
permissions:
  contents: write
  pull-requests: write
  checks: write
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: alternayte/speccy@v0.11.0
        with:
          models: all=anthropic:<model> # leave out for the deterministic checks only
          anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

It is advisory by default: the verdict shows and the job passes. `enforcement: blocking` fails it. Reply to a comment with `/speccy waive <reason>` and the next run commits the decision to your branch. [docs/github.md](docs/github.md) has every reply and the whole flow.

## Coding agents

`speccy mcp` is an MCP server over stdio. An agent can review a doc, read the verdict, take the build packet of a Build Ready bundle, report what it learned while building, and verify what it built:

```sh
speccy verify docs/specs/pay https://github.com/acme/pay/pull/12
```

Speccy reads the code. It runs no code and no tests, so a cited test is a citation, never a pass.

## Run it for a team

Hosted mode adds accounts, roles, share links, threads and approvals. It needs Postgres.

```sh
docker run -p 8080:8080 \
  -e SPECCY_DATABASE_URL=postgres://user:pass@host:5432/speccy \
  -e SPECCY_MASTER_KEY="$(openssl rand -base64 32)" \
  -e SPECCY_BASE_URL=https://speccy.example.com \
  ghcr.io/alternayte/speccy:0.11.0
```

The image runs hosted mode as a non-root user. Local mode is not a container job: it listens on loopback only, by design, because it has no sign-in. [docs/configuration.md](docs/configuration.md) has the environment, the accounts and the sharing.

## Models

Open **Admin** to add a backend and assign it to the review roles:

- An API key for Anthropic, OpenAI, OpenRouter or DeepSeek. Speccy encrypts it with a key in `.speccy/state/key`.
- An agent CLI you already pay for: `claude`, `cursor-agent`, `opencode` or `pi`, or your own command. Speccy runs it in a temporary folder holding only the bundle.

Set a monthly token budget to cap spend. When it is spent, the AI stages stop and the deterministic checks still run.

## How the verdict works

A review has six stages: lint, rubric, grounding, divergence, coherence, and the verdict.

- **Lint** — fixed rules, no model, well under a second on a 10,000-word doc.
- **Rubric** — the reviewer model answers each yes-or-no check of the doc type, with quotes as evidence.
- **Grounding** — each factual claim is checked against a source. A claim with no source is unverified. Start a sentence with "Assumption:" to state something you cannot source.
- **Divergence** — 10 to 20 build questions; three readers answer each from the doc alone. Every answer must quote the doc, and Speccy checks each quote against the text. A judge groups the answers by meaning.
- **Coherence** — linked docs must cover each other, must not contradict, and must not repeat. When a linked doc changes, the verdict goes stale.
- **Verdict** — a pure function with no I/O.

Each check carries a level. The doc is **Build Ready** only when no MUST finding is open and any required upstream link exists. SHOULD and INFO never change the verdict. A verdict for an old version is **stale**. The score is passed checks over applicable checks: for tracking, not a gate.

## Commands

| Command                                                | What it does                                           |
| ------------------------------------------------------ | ------------------------------------------------------ |
| `speccy`                                               | The app on 127.0.0.1, on the current folder.           |
| `speccy review <path…>`                                | Review in a terminal or in CI. Text, JSON or markdown. |
| `speccy verify <path> [<GitHub URL or folder>]`        | Verify one build against the bundle.                   |
| `speccy add <url>`                                     | Read a repo, a folder or a doc from GitHub.            |
| `speccy tui`                                           | The terminal UI. `e` opens a finding in `$EDITOR`.     |
| `speccy mcp`                                           | An MCP server over stdio.                              |
| `speccy serve --hosted`                                | Hosted mode, for a team.                               |

`speccy review` exits 0 for Build Ready or for any verdict in advisory mode, 1 for Not Build Ready with `--enforcement blocking`, 2 for a usage error, and 3 when a review fails. `speccy verify` exits 1 when the run is Not Verified.

## Documentation

| Document                                       | What it holds                                                                                                               |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| [docs/guide.md](docs/guide.md)                 | One doc end to end: make a bundle, write, review, take the tour, reach Build Ready, hand it to a builder, verify the build. |
| [docs/adoption.md](docs/adoption.md)           | Docs you already have: a file on disk, one doc in GitHub, a folder, a repo, and a repo your team reviews in.                |
| [docs/github.md](docs/github.md)               | Specs in pull requests: adopt a repo, read the comment, decide with a reply.                                                |
| [docs/profiles.md](docs/profiles.md)           | What a profile holds, what the template's required markers mean, and how a doc's size changes what it must answer.          |
| [docs/linked-docs.md](docs/linked-docs.md)     | Two docs that must agree: links, trace IDs, the matrix, coverage, contradiction, and the stale verdict.                     |
| [docs/cli-and-tui.md](docs/cli-and-tui.md)     | Every command, the terminal UI and its keys, connected mode, and the MCP server.                                            |
| [docs/configuration.md](docs/configuration.md) | Local mode flags, `.speccy.yaml`, the GitHub Action, hosted mode, accounts, roles, and sharing.                             |
| [docs/guarantees.md](docs/guarantees.md)       | Each guarantee Speccy makes, and the test that proves it.                                                                   |
| [docs/decisions.md](docs/decisions.md)         | Each design decision, its alternative, and its reason.                                                                      |

## Contributing

You need Go, Node 24 or later, and `just`. `just test-pg` and `just verify` also need Docker.

```sh
git clone https://github.com/alternayte/speccy && cd speccy
just dev      # the Go server and Vite together, on http://127.0.0.1:5173
just verify   # the gate a pull request must pass
```

`just build` writes `bin/speccy`.

## Licence

AGPL-3.0. See [LICENSE](LICENSE).
