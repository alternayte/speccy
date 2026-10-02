# A GitHub URL as the upstream doc

## What it does
An SDD links its PRD by the GitHub URL of the PRD on a branch. Speccy resolves that link to the spec doc of a GitHub source, so the PRD counts as the upstream doc: `links.has-upstream` passes, the coverage checks read its requirements, and a new commit on the branch makes the verdict stale. When no source holds the file, Speccy adds a one-doc source for it. Issue #97.

## Decisions
- A link whose target is a GitHub file URL, or `github:owner/repo@branch#path`, resolves to the spec doc of a GitHub source that holds that file on that branch. — Every check that reads an upstream doc already works on a spec doc.
- When no source holds the file, local mode adds a one-doc source for it when a review starts. — The person asked for the review, and local mode has one user.
- Hosted mode adds no source by itself. The finding says that an admin must add the URL, and the finding card gives an admin the control. — Only an admin adds a source there.
- The ref must be a branch. A link at a commit SHA stays an external link, and the finding says that an upstream link needs a branch. — A source follows a branch, and a pinned file never changes.
- Only a link of an upstream kind of the profile adds a source. — A `references` link to a page must not pull a repo in.

## Out
- No read-only upstream doc that is not a spec doc.
- No source at a tag or a commit.
- No change to `implemented-by` links and code drift.

## How I know it works
- An SDD with `kind: implements` and the `blob/<branch>/...` URL of a PRD that a source holds: `links.has-upstream` passes, and Traceability shows the PRD's requirements.
- The same link with no source, in local mode: after **Run review**, the bundle list has the PRD as a doc of a GitHub source, and the check passes.
- A new commit to the PRD on its branch makes the SDD's verdict Stale after the next sync.
- A link at a commit SHA: the finding says that an upstream link needs a branch.
- In hosted mode, a member sees the finding with the text for an admin, and an admin sees **Add it as a source**.
