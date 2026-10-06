# A MUST check with no answer blocks the verdict

## What it does
A rubric check can still have no valid answer after Speccy asks again (#134). When that check is a MUST, the run still gets a verdict, but the verdict is Not Build Ready. A finding says that the reviewer gave no answer. The next run asks only for that check.

## Decisions
- A MUST check with no answer gives a MUST finding on the check: "The reviewer gave no answer for this check. Run the review again." Build Ready then means that every MUST check got a judgement.
- The finding is not cached. The next run asks for the check again, and the other answers come from the cache.
- A SHOULD or INFO check with no answer stays not applicable, with a run note, as today.
- The run does not fail. One bad answer does not cost a full review.
- A waiver cannot cover the finding. Only an answer clears it.

## Out
- No change to the re-ask path or to the batch size.
- No change for a stage that fails as a whole.

## How I know it works
- A fake reviewer drops one MUST check from every answer. The run ends with Not Build Ready and the finding that names the check.
- The next run with a working reviewer sends only that check, and the verdict follows its answer.
- `just verify` passes.
