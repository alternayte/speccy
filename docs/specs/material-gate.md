# Block only on material gaps

## What it does
Build Ready blocks only on a material gap: a point where a builder who cannot ask the author must guess, and a wrong guess changes the data model, a public interface, security or what the user sees. Each blocking finding is a build question with two plausible options, a suggested pick, and its consequence. The author answers every blocking question on one screen, Speccy writes the answers into the doc, and the next review asks new questions only about text that changed. A default review runs lint and one model pass. The other stages run only when the profile or the doc gives them work.

## Decisions
- A finding blocks only if its check is MUST, it is open, and it is material. — A checklist gate grows with doc length; a material gate grows with real risk.
- The model decides materiality for each finding. A material finding states two plausible builder choices and the class they differ in: data model, interface, security or user-visible. A finding with no such pair is advice. — Two options make the gap answerable in one line and keep the model honest.
- The finding record stores the consequence class, the two options, the suggested pick, and the author's answer with the option chosen. — A later review must not ask an answered question again.
- Rubric shortfalls, reader gaps and divergences that name the same section and fork become one build question. — The author answers one fork once.
- Check level: MUST means "blocks when material". SHOULD never blocks. INFO stays hidden. — One control that profile authors already know.
- Built-in SDD profile keeps MUST on data.model, interfaces, failure-modes, limits, security, state, consistency. It moves non-goals, decisions.ids, decisions.alternatives, components, data.ownership, observability, testing, migration, performance, delivery, open-questions and links.has-upstream to SHOULD. The PRD profile gets the same split. — These are where real forks occur; the rest test format.
- lint.placeholder no longer blocks. Each placeholder becomes a build question for the readers. — A TBD is an admitted gap, and materiality decides if it matters.
- lint.required-headings, lint.broken-link and lint.duplicate-id move to SHOULD. frontmatter.readable stays MUST. — Speccy must not force its format; it cannot review a doc it cannot parse.
- The questions screen lists every open blocking question with its options, the suggested pick, "Other" with a text box, and "Not needed" with a reason. — The author sees the whole cost of Build Ready at once.
- "Not needed" closes the finding with its reason and needs no approval. A waiver is only for a material gap the author ships open. — Rejecting a wrong finding must cost nothing.
- The write-back call for an answer starts when the author picks it. Submit shows one diff per section. Accept saves one version and starts a review of the changed sections. — The author never waits on a model call they could have started earlier.
- The rail cards stay for one-at-a-time answers. Advice shows grouped by check, as fix-findings defines. — Advice must not compete with the blocking questions.
- A review raises a new build question only in changed text, or about the effect of changed text on the rest of the doc. Unchanged sections keep their result. A whole-doc check reports only a fork that involves changed text. — Each round shrinks, so the author can trust the screen.
- A person can ask for a fresh full review. It is the only way to raise new questions about unchanged text. — A missed gap still has a path; the false-ready rate measures how often it happens.
- The question set has no minimum. Its maximum scales with the doc's size. Readers ask only where they see a fork. — A minimum forces filler questions on a short doc.
- A default review runs lint, rubric and readers. Grounding runs only when the doc makes claims about outside facts and the profile turns it on. Coherence runs only when the doc has links. Code drift and verification run only when a code repo is attached. No stage code is deleted. — A new user sees one review, one screen, one verdict.
- The model stages of a run start at the same time and merge their results at the end. The default bound on model calls is 8, and it stays configurable. Questions stream to the screen as each exists. — Stages do not depend on each other.
- Existing waivers and sidecars keep working. A waiver on a finding that is now advice stays valid and has no effect on the verdict. — No sidecar migration.

## Out
- No change to the waiver approval flow (PR #20).
- No deletion of grounding, coherence, drift or verification.
- No change to the score or the radar. They stay metrics.
- No new reader mechanism. Readers stay separate sessions.
- No bulk answer by the model without the author.

## How I know it works
- An AI-written SDD of 6000 words with no PRD reaches the questions screen with at most 20 blocking questions, and no question about format, headings or trace IDs.
- Each blocking question shows two options, a suggested pick and a consequence class.
- Picking the suggested option on every question and accepting the diffs gives Build Ready after at most two reviews, unless a written answer creates a new fork.
- After the author answers all questions, a review that edited one section shows new questions only for that section.
- "Not needed" with a reason closes a finding with no approval, and the verdict follows.
- A doc with "TBD" in the data model section shows a blocking question for it, not a lint MUST finding.
- The run report shows rubric and divergence calls in flight at the same time, and up to 8 calls in flight.
- A doc with no links, no outside claims and no code repo runs no coherence, grounding, drift or verification step.
- `just verify` passes.
