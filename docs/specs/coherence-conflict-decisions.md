# Coherence conflict decisions

## What it does
A coherence conflict keeps its identity from run to run, so the list of conflicts on unchanged text stays the same (#132). The author of the downstream doc can answer a conflict with "The linked doc must change". After approval, the conflict stops blocking the downstream verdict, and the upstream doc gets it as a MUST finding (#133). The upstream author can answer that finding with "The downstream doc must change", and the conflict blocks the downstream doc again. A new version of either doc that removes the conflict closes it on both docs.

## Decisions
- A conflict's identity is the linked doc's slug and the two quotes, as in a waiver's `conflict` field (#136). A waiver and an upstream decision match on that identity.
- After a new conflict call, a separate small call matches each new conflict with the earlier conflicts. A match keeps the earlier identity. The model rewords conflicts, and a second question in the main prompt made the model call open items fixed (#119, `same.go`).
- An earlier conflict with no match ends when one of its quotes is no longer in its doc. If both quotes are still there, the conflict stays as a carried finding. A conflict then ends only through an edit, not through a change in the model's answer.
- "The linked doc must change" is an upstream request, a new decision in the downstream doc's sidecar, under `upstream_changes`. It names the conflict and holds a reason. The sidecar goes with the doc to CI, to PR reviews, and to other people.
- The decision uses the waiver mechanism and the approval policy of `coherence.contradiction`. It lifts a MUST from the verdict, so it needs the same approval as a waiver.
- An approved decision does not block the downstream verdict. The finding stays on the downstream doc as "Waiting on <upstream doc>". A coding agent then follows the downstream doc.
- The profile key `coherence.upstream_pending: block` keeps the block for a team that wants it. The default is `allow`.
- The upstream review reads the `upstream_changes` of every spec doc that links to it, and makes one MUST finding `coherence.downstream-request` for each one. The finding quotes both docs and gives the downstream reason. Speccy writes nothing into the upstream repo.
- On the upstream doc, the finding has the answer "The downstream doc must change", a send-back, with a reason and the same approval policy. The answer ends the downstream decision, and its reason shows on the downstream finding.
- No more answers come after "The downstream doc must change". The conflict then needs an edit or a waiver.
- A waiver of the conflict on either doc says the conflict is acceptable. It clears the conflict on both docs.
- A PR review of the upstream doc (`action --pr --pending`) posts `coherence.downstream-request` as a comment on the quoted upstream line, like any MUST finding.
- An ended decision stays in the sidecar until a later decision rewrites it, as an ended waiver does. The history shows why it ended.

## Out
- No decision on a coherence finding other than `coherence.contradiction`.
- No upstream finding when the upstream review cannot see the downstream doc.
- No writes into the upstream doc, its sidecar, or its repo.
- No change to the rubric's carried findings.

## How I know it works
- Review an SDD and its PRD two times with no edit. Both runs give the same conflicts with the same quotes.
- Edit an SDD section that holds no conflict. The next full review keeps every earlier conflict, and the model's wording does not change their quotes.
- Answer a conflict with "The linked doc must change" and approve it. The SDD verdict no longer counts it, and the finding reads "Waiting on PRD". The sidecar holds an `upstream_changes` entry.
- Review the PRD in the same folder. It has a `coherence.downstream-request` MUST finding that quotes both docs.
- Answer that finding with "The downstream doc must change" and approve it. The next SDD review blocks on the conflict again and shows the reason.
- Edit the PRD so that its quote is gone. The next reviews close the conflict on both docs.
- With `coherence.upstream_pending: block`, the approved decision still blocks the SDD verdict.
- `just verify` passes.
