# Two ways out of a blocking item

## What it does
Every item on the questions screen and every MUST card in the rail offers the same two ways out besides an answer. **Not needed** says the finding does not apply to this doc. It takes a reason and closes at once. **Ship it open** says the gap is real and the author ships it anyway. It is a waiver and the only answer that waits for approval. Acknowledgements (out of scope, covered by another doc, standalone) close at once, as Not needed does. In local mode, Ship it open also closes at once, because the one person is the approver.

## Decisions
- Each blocking item offers Not needed and Ship it open. A material gap adds Ship it open beside its options, Other and Not needed. A coverage gap keeps "This doc covers it". "Another doc covers it" and "Out of scope" stay as its answers, and both close at once. A missing upstream link keeps its link fix, and "Mark it standalone" closes at once. — The cost of "does not apply" no longer depends on which check raised the item.
- An acknowledgement request (trace out_of_scope, trace covered_by, standalone) records Requested and then Approved in one decision, as Not needed does today. Approval writes the sidecar entry as before. — An acknowledgement is a statement of scope or fact, not an exception.
- The reply command `/speccy ack` and the MCP tools follow the same rule. — One rule on every surface.
- "Ask for a waiver" is renamed "Ship it open" on the questions screen, the rail and the dialog. The dialog says the gap stays real and an approver decides. — The label says what the author is doing.
- Ship it open keeps the profile's waiver policy in hosted mode. In local mode it records Requested and Approved in one decision. — Local mode has one person, and nextaction already asks no approval of them.
- A profile policy that forbids waivers of a check still forbids Ship it open. It does not forbid Not needed or an acknowledgement, unless the check already forbids Not needed today. — No change to what a profile can forbid.
- The upstream request and the send-back keep their approval flow. — They hand the question to another doc's owner.
- On the first read after the upgrade, each acknowledgement request that waits for approval gets an Approved event with the actor "speccy" and the note "approval no longer needed", and its sidecar entry is written. — Old requests follow the new rule, and no inbox keeps a request that needs nobody.
- The approval flow of PR #20 does not change for the requests that still need it.

## Out
- No change to the materiality judge or the verdict rule.
- No change to upstream requests and send-backs.
- No change to the waiver approval UI.
- No new sidecar fields.

## How I know it works
- In local mode, "Out of scope" with a reason on a trace.coverage gap closes the gap with no Approve click. The sidecar holds the trace entry, and the matrix shows Out of scope.
- In local mode, "Ship it open" on any blocking item closes it at once and writes a waiver to the sidecar.
- In hosted mode, Ship it open waits for approval under the profile's policy, and the approver sees it at the top of their questions screen and in the inbox.
- In hosted mode, Mark it standalone closes the links.has-upstream finding with no approval.
- A store with a pending out-of-scope request from before the upgrade shows it approved by "speccy" after the upgrade, and the approver's inbox no longer lists it.
- No surface shows the label "Ask for a waiver".
- `just verify` passes.
