# Upstream doc from an open pull request

## What it does
A link rule can name an upstream doc that is not in the tree yet, because its pull request is open (#142). Speccy finds an open pull request of the same repo that adds or changes that path. It reads the doc at that pull request's head commit. The coherence, coverage and `reads: [upstream]` checks then run against it. The review says which pull request and which commit it used.

## Decisions
- Only reviews that know their GitHub repo look: `action --pr`, `review-prs`, `review_url`, a GitHub source, and the CI Action. A local folder review does not look, because it has no repo it can trust.
- A pull request matches when its changed files add or change exactly the target path.
- Draft pull requests count. A PRD is often a draft while its SDD is written.
- When several pull requests match, Speccy uses the one with the latest update, and a run note names the others.
- Speccy reads the doc again on each review. A pull request can change between reviews.
- The link shows "from pull request #N at <sha>" in the report, the PR review body and the JSON output.
- A Build Ready verdict that depends on such a link gets a run note: the upstream doc is not merged.
- A doc in the tree always wins over a doc in a pull request.

## Out
- No search in other repos.
- No search for frontmatter links or external links. Link rules only.
- No local folder reviews.

## How I know it works
- A repo has an open pull request that adds `docs/x/PRD.md`, and another pull request that adds `docs/x/SDD.md`. `speccy action --pr <SDD pull request> --dry-run` links the PRD, runs coverage against it, and names the PRD pull request and its head commit.
- Push a new commit to the PRD pull request. The next review uses the new commit.
- Merge the PRD. The next review uses the PRD in the tree and names no pull request.
- `just verify` passes.
