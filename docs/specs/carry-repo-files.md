# A bundle carries the repo files its spec doc references

## What it does
A spec doc that links to a file above its own folder, such as `../shared/bus.md`, gets that file into its bundle. The link is then not a broken link, so it no longer blocks Build Ready. Grounding, the rubric, the divergence readers, the export and the build packet all see the same file. A link to a spec doc of another bundle is not a broken link either. A file that git ignores never enters a bundle.

## Decisions
- The bundle carries a file above the spec doc's folder under the reserved path `@repo/<path from the source root>`. — A bundle path may hold no ".." segment, and the reserved path gives the file one place in every consumer of the bundle. This replaces "no reach above the bundle's folder" of docs/specs/referenced-files.md.
- The file is a carried file: read only, never written back, and marked in the explorer with its repo path. — Two bundles can carry one file, and a write from either would overwrite the other.
- Speccy never rewrites the link in the spec doc. Lint, the preview and the explorer resolve `../shared/bus.md` to `@repo/shared/bus.md`. — The doc on disk and in git stays the text that the author wrote.
- `source.CleanPath` refuses a real path that starts with `@repo/`. — The reserved path must not collide with a file of the bundle.
- A carried file above the folder is a file of the bundle for the review: grounding, the rubric and the divergence readers read it within the bound for text assets. — The evidence that the author links is the evidence that Speccy checks.
- A change to the file makes a new bundle version, and the verdict of the old version reads stale. — A run pins the version, so anything a reader saw belongs in it.
- A link to a spec doc of another bundle, at any place in the source, is not a broken link, and the bundle does not carry it. — Two bundles must not hold one text, and the link rules relate the two bundles.
- Local mode and GitHub sources follow one rule. A GitHub source reads the file from the repo at the commit of the source, with one API call for each file. — The Action in CI must give the same verdict as local mode on the same files.
- Speccy never carries a file that git ignores, below or above the spec doc's folder. The link stays a `lint.broken-link` MUST, and the message says that git ignores the file. — Ignore rules mark the files that must not leave the machine, and the Action cannot see such a file.
- A folder that is not a git repo has no ignore rules. Hidden paths stay refused, as today. — Speccy reads the rules that the folder has, and adds none of its own.
- A link above the source root stays a broken-link MUST. — Speccy holds no file outside the source.
- Speccy follows the references of each carried markdown file to closure, and the bundle size limit bounds the walk, as today. — A carried doc that shows a diagram is the same fault one level down.
- A mapped or adopted doc is never carried. — It is another bundle.
- The build packet holds the spec doc byte for byte as reviewed, and each carried file above the folder at `@repo/<path>`. HANDOFF.md has a table of each such link in the doc and its file in the packet. — The verdict and the build reports quote the reviewed text, and a builder reads HANDOFF.md first.
- The export .zip uses the layout of the build packet. — A builder and a reviewer get the same folder.

## Out
- No rewrite of a link in a spec doc, on disk, in git, or in the build packet.
- No carry of a file outside the source, a hidden file, or a file that git ignores.
- No carry of a spec doc of another bundle.
- No change to the files that a folder bundle holds from its own folder.
- No new size limit.

## How I know it works
- In local mode, a spec doc in `pay/` links `../shared/bus.md`. The review has no broken-link finding for it, and the explorer shows `@repo/shared/bus.md` as carried.
- A claim that `shared/bus.md` states is verified, with `@repo/shared/bus.md` as its source.
- The same doc links `../prd/PRD.md`, a spec doc of another bundle. The review has no broken-link finding for it, and the bundle does not hold it.
- `shared/bus.md` changes on disk. The bundle gets a new version, and the old verdict reads stale.
- `config/prod-secrets.yaml` is in `.gitignore`, and the doc links it. The bundle does not hold it, and the broken-link MUST says that git ignores the file.
- The doc links `../../outside.md`, above the served folder. The broken-link MUST stays.
- The Action on a pull request that holds the same files gives the same findings as local mode.
- `speccy handoff pay --out packet` writes `packet/@repo/shared/bus.md`, a `SPEC.md` that equals the reviewed version, and a HANDOFF.md table row from `../shared/bus.md` to `@repo/shared/bus.md`.
- A file in the bundle named `@repo/x.md` is refused with a message.
- `just verify` passes.
