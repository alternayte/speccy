# Speccy

Speccy reviews markdown spec bundles and returns one verdict: Build Ready or Not Build Ready.

Speccy is at milestone M3. In local mode you can create, edit, import, compare, and export bundles. Lint runs on every save and gives a lint-only verdict. The AI review stages do not exist yet.

## Quick start

You need Go, Node 24 or later, and `just`. `just test-pg` and `just verify` also need Docker.

```sh
git clone https://github.com/alternayte/speccy && cd speccy
just build
./bin/speccy
```

`speccy` starts local mode on the current folder and opens your browser. Add `--dir <folder>` to serve another folder, and `--no-open` to stop the browser from opening.

A bundle is a folder with one markdown file that has a `type` field in its frontmatter:

```markdown
---
type: sdd
title: Payment retries
---
```

Speccy keeps a version of each bundle every time a file changes, in the app or on disk. It stores its state in `.speccy/state/`. Do not commit that folder.

To work on Speccy, run `just dev` and open http://127.0.0.1:5173.

## Guarantees

This table lists only the guarantees whose tests pass today.

| Guarantee | Test |
|---|---|
| An open MUST finding gives Not Build Ready. | [`TestVerdict_OpenMustBlocks`](internal/engine/verdict/verdict_test.go) |
| SHOULD findings never change the verdict. | [`TestVerdict_ShouldNeverBlocks`](internal/engine/verdict/verdict_test.go) |
| The verdict function is pure: same input, same output. | [`TestVerdict_Deterministic`](internal/engine/verdict/verdict_test.go) |
| Lint finishes a 10,000-word doc in under 1 second. | [`BenchmarkLint_10kWords`](internal/engine/lint/lint_test.go) |
| A mapped file with no frontmatter is reviewed with the mapped profile; frontmatter `type` wins. | [`TestConfig_PathMapping`](internal/features/review/review_test.go) |
| A relaxed check reports as INFO and never blocks; removing it restores its level. | [`TestAdoption_RelaxedCheck`](internal/features/review/review_test.go) |
| Both store engines pass the same conformance suite. | [`TestStoreConformance`](internal/store/conformance/conformance_test.go) |
| A concurrent append with a stale version is rejected. | [`TestEventStore_ConcurrentAppendRejected`](internal/es/es_test.go) |
| Projections update in the same transaction as the append. | [`TestEventStore_InlineProjectionAtomic`](internal/es/es_test.go) |
| Local mode refuses a non-loopback address. | [`TestLocalMode_LoopbackOnly`](internal/http/server_test.go) |

## How the verdict works

A review has six stages: lint, rubric, grounding, divergence, coherence, and the verdict. Today only lint runs.

- **Lint** checks the writing with fixed rules: placeholders, missing required sections, broken links, duplicate IDs, filler phrases, vague words, long sentences, and more. It runs on every save and takes well under a second.
- **Rubric, grounding, divergence, and coherence** use AI readers. They are not built yet.

Each check has a level: MUST, SHOULD, or INFO. The doc is **Build Ready** only when no MUST finding is open and any required upstream link exists. SHOULD and INFO findings never change the verdict. A verdict for an old version is **stale**.

The **score** is passed checks divided by applicable checks. It is for tracking, not a gate.

## Modes

| Mode | Command | Status |
|---|---|---|
| Local | `speccy` | Edit, import, compare, and export bundles on 127.0.0.1. |
| Hosted | `speccy serve --hosted` | Not built yet. |
| Headless | `speccy review <path>` | Not built yet. |

## Try it on your existing specs

Run `speccy --dir <your repo>`. Speccy finds every folder with a main doc. To review docs that have no frontmatter, add a `.speccy.yaml` at the root:

```yaml
map:                      # single files, with assets in <name>.assets/
  - glob: docs/**/prd-*.md
    profile: prd
  - glob: docs/**/sdd-*.md
    profile: sdd
adoption:                 # these checks report as INFO for now
  relaxed: [links.has-upstream, lint.required-headings]
```

`speccy review docs/ --summary` arrives with the CLI (M11).

## GitHub Action

The GitHub Action does not exist yet.

## Configuration

`docs/configuration.md` does not exist yet. Local mode takes `--dir` (default: the current folder) and `--addr` (default `127.0.0.1:7878`). The address must be a loopback address.

## Licence

AGPL-3.0. See [LICENSE](LICENSE).
