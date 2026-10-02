# A re-review that converges

## What it does
An author fixes findings, runs a full review again, and sees fewer findings. An edit in one section does not change the result of a check about another section. A full review reports every shortfall of a failed check, keeps the build questions of the doc, and says what is fixed, still open, and new since the last review. A finding never leaves the verdict because the model did not look at it again. Issues #99, #96 and #100.

## Decisions
- A rubric check can name its heading: `section: "Monitoring"` in the profile. — Only a named heading lets Speccy keep a passed answer when another section changes.
- A check with `section` reads that section, its subsections, and the doc title. Its cache key is the hash of that section. — The answer can stay only if it cannot depend on text elsewhere.
- When the doc has no such heading, the check runs on the whole doc, as a check with no `section` does. — Speccy does not force its format on an adopted doc.
- The heading match is the match that `lint.required-headings` uses. — A numbered heading such as "5. Monitoring" must count.
- The built-in PRD and SDD profiles set `section` on each check that is about one template section. — The default profiles must converge without a profile edit.
- A failed rubric check returns a list of shortfalls. Each shortfall is one finding with its message and quote. No cap. — One reason per check hides the next shortfall until the next run.
- A waiver is per check and section. One waiver covers every shortfall of that check there. — The approver excuses the check, not one sentence of the model.
- A whole-doc finding carries after an edit: a finding of a check with no `section`, or a finding with no quote. It counts until the next full review judges its check again. — Today it drops on any save, and the doc can read Build Ready by mistake.
- A waiver of a whole-doc check does not end on an edit. It ends when a full review passes the check, or when the check's question or pass condition changes. — An edit elsewhere does not change "this check does not apply to this doc".
- A waiver of a check with `section` ends when that section changes. — This is the rule that section-scope checks have today.
- A run with a stage subset carries the findings of each AI stage it skips, from the last run that did that stage, under the carry rule. — A subset run must not drop a MUST finding by not looking.
- A doc keeps its build questions across versions. Speccy drops a question when its cite is gone, and writes new questions only for sections that are new. — A new set on each version makes the gaps a moving target.
- A person can ask for a fresh set of build questions. — It is the only way the whole set changes.
- In a full review the readers answer again a question whose cited section changed, and a question that was a gap or a divergence. An agreed question with an unchanged cite keeps its answers. — A gap can close with text in any section; an agreed answer rarely changes.
- A full review that ends on an old version carries its findings to the current version at once, under the carry rule. The control row names the version the review read and the count of sections changed since. — Today the findings arrive only after the next save.
- The rail shows one line after a full review: fixed, still open, new, against the last full review. The unit is a check in a section, or a build question. MUST and SHOULD count. New items carry a mark. — A comparison by message reads a reworded finding as one fixed and one new.
- Speccy sends temperature 0 where the backend accepts it, and the run report names the value. — It removes variance that Speccy controls, at no cost.

## Out
- No recheck run kind. A full review already calls the model only for what changed.
- No repeated runs of one check, and no "unstable" level.
- No cancel control for a running review.
- No change to the store rule of `speccy review`. The docs say that a run with no state folder shares no cache with the app.
- No guard on `StartRun` against two starts at the same instant, and no shorter job lock. They belong to the spec for one process that owns the local state.
- No change to the checks of the lint stage.

## How I know it works
- Run a full review. Edit one section. Run it again. The run report shows model calls only for the checks of that section, the whole-doc checks, and the questions that cite it or were open.
- A check with five shortfalls shows five findings in one review.
- Waive `sdd.architecture`, then edit another section. The waiver stays approved, and the finding stays waived.
- Save an unrelated edit on a doc with a "section missing" MUST finding. The verdict stays Not Build Ready, and the finding shows the version it came from.
- Run `speccy review --stages divergence` after a full review. The MUST count does not fall for the rubric and grounding stages.
- Edit the doc two times and review each time. The build questions keep their numbers and text.
- Save while a review runs. When the review ends, its findings are in the rail without another save.
- After the second review, the rail reads "Since the review of v3: 4 fixed, 2 still open, 1 new" with the right counts.
