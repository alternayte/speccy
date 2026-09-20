# Waiver approval flow

## What it does
An approver opens a waiver request from the inbox and lands on the finding it excuses, with the section in view. The request card carries the reason, the policy count, and the waivers already decided on the same section. The approver approves, or rejects with a reason the requester reads. After one decision, one click moves to the next request waiting on that bundle. When an edit ends an approved waiver, the author reads why and re-requests in one click.

## Decisions
- A waiver_request inbox item links to the bundle with the finding selected and focused — the reason alone does not support a decision; the section text does.
- The deep link focuses the whole section, not the finding's quote — a waiver covers the section, and the section hash gates it.
- A doc-scope waiver focuses no text and the card reads "whole doc" — the section is empty by design.
- The decided request stays on its finding and changes in place — an auto-jump moves the text under the reader and hides the result.
- A "Next waiver (n left)" line under the decided card scrolls to the next waiting finding — a run of decisions costs one click each without stealing the view.
- The request card lists the decided waivers on the same section, with status, check slug, who, and reason — a run of exceptions in one place is the strongest signal at the moment of the decision.
- Those rows stay in the Waivers section as well — that section is the record of the whole bundle.
- `POST /waivers/{id}/reject` takes a body with a reason, of at least 20 characters — a rejection tells an author to change the doc, and a bare no sends them to ask a person.
- `WaiverRejected` carries the reason, and the Waiver object returns it as `decision_reason` — the store holds the decision, not only its outcome.
- An approval takes no reason — the approved waiver writes its request reason into the frontmatter, and that is the record.
- A rejection creates an inbox item for the requester, and an invalidation creates one for the authors — both land on a person who is not looking at the rail.
- An invalidated waiver reads "Ended: someone edited <section> after this was approved. Run the review again." — invalidation is silent today, and the cost lands later as a surprise Not Build Ready.
- The invalidated card offers a re-request that pre-fills the old reason — the edit often does not change what the exception was for.
- The stale frontmatter entry stays until a later approval rewrites it — `validWaivers` already drops it on the section hash, so nothing reads it.

## Out
- No queue that walks requests across bundles.
- No approval reason.
- No removal of a waiver from the frontmatter on invalidation.
- No change to the waiver policies or to who can approve.
- No change to the verdict bar count or to the request card's place at the top of the rail.

## How I know it works
- The inbox shows a waiver request. A click opens the bundle, the rail selects that finding, and the preview scrolls to the section the waiver names.
- The request card shows the reason, "0 of 1 approvals", and each earlier decided waiver on that same section.
- Reject with fewer than 20 characters fails with a problem that names the minimum. Reject with a reason succeeds, and the card reads the rejection and its reason.
- The requester's inbox holds an item that names the check and the bundle, and shows the rejection reason.
- With two requests waiting on one bundle, a decision on the first shows "Next waiver (1 left)". A click scrolls to the second finding.
- Edit a section that holds an approved waiver, then save. The Waivers section shows the waiver as ended, and names the section. The authors' inbox holds an item. A click on re-request opens the dialog with the old reason in the field.
- `just verify` passes, including gen-check for the reject request body.
