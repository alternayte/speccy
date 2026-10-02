# A finding stays until it is fixed

## What it does
A full review judges the shortfalls of the last review again, one by one, and does not find them afresh. A shortfall on text that did not change stays until the model says that it is fixed. An agreed build question stays agreed while the text its readers quoted is in the doc. A profile maintainer finds checks that pull against each other with one command. Issue #107.

## Decisions
- The rubric prompt lists, for each check, the shortfalls of the last full review of that check, with their quotes. The model answers "still holds" or "fixed" for each, then lists other shortfalls. — A check that starts from nothing gives a new subset on each run.
- A shortfall of the last review leaves only when the model says "fixed", when the check passes, or when its quoted text is gone from the doc. A shortfall that the model does not mention stays. — Silence of the model is not evidence of a fix.
- A shortfall whose quote is gone is not in the prompt. — The text it was about does not exist.
- A check whose text did not change keeps its cached answer, with the shortfalls it had. A fresh review reads no answer of a review before it. — The cache must still save the call for unchanged text, and a clean look must be clean.
- An agreed build question keeps its answers while each quote that its readers gave is in the doc, even when other text of the cited section changed. A question whose readers gave no quote keeps the rule of the cited text. — The quotes are the evidence for the agreement.
- The control for a fresh set of build questions also makes the next full review judge each rubric check from nothing. — It is the one way to get a clean look, and one control is enough.
- `speccy profile validate --conflicts` sends the rubric checks of the profile to the reviewer model in one call. It prints each check, or pair of checks, whose pass conditions cannot both hold, with one sentence that says why. — The cause of two findings that contradict each other is in the profile.
- `speccy profile validate` with no flag calls no model. — It must work with no state and no model.

## Out
- No guard in a review that picks between two findings that contradict each other.
- No change to a custom profile by Speccy.
- No repeated runs of one check, and no "unstable" level.
- No change to the checks of the built-in profiles.

## How I know it works
- Review a doc whose check fails with two shortfalls. Fix the text of one. The next review shows the other one with the same message, and no new subset.
- A reviewer model that lists no shortfall of the last review on the second run: the findings on unchanged text stay.
- A reviewer model that says "fixed" for a shortfall: that finding leaves, and the trend counts it as fixed.
- Add a sentence to the cited section of an agreed build question, away from the quoted text. The next review makes no reader call for that question.
- Remove the quoted sentence. The next review asks the readers again.
- Use the fresh control, then review. The rubric prompt lists no shortfall of the last review.
- `speccy profile validate --conflicts` on a profile where one check asks for a short narrative and another asks that the narrative names each flow prints that pair and the reason.
