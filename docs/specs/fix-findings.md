# Fix findings faster

## What it does
A person or a coding agent fixes the findings of a review in fewer steps. The rail shows what blocks Build Ready first, and groups the rest by check. One control rewrites every finding of a rewording check, section by section, and the author accepts the diffs. A finding that needs a fact asks the author for the answer, then writes it into the doc. An agent reads the same fix list over MCP, with line numbers, and follows one loop that ends with one review. The suggested fix no longer fails on a cut-off answer or on a miscopied quote.

## Decisions
- One finding shape serves the rail and MCP. Each finding in the API has its line and end line, its fix kind, and its new and carried marks. — A fixer needs the same facts in both places, and two shapes drift.
- A finding has a fix kind: reword or answer. A reword finding needs no fact. An answer finding needs a fact from the author. — The bulk fix and an agent must know which findings they may change alone.
- The check catalog names the reword checks: `lint.passive-voice`, `lint.sentence-length`, `lint.slop-phrase`, `lint.rfc2119-case`, and `lint.broken-link` when one bundle file has the linked name. Every other finding is an answer finding. — Lint can prove a fix of these checks with no model.
- The rail shows MUST findings as cards in doc order. SHOULD and INFO findings show as one row for each check with a count. A click opens a row to its cards. — 150 cards hide the 10 that block Build Ready.
- The rail gets no view switch and no filter control. — The grouping does the work, and the app has too many controls.
- A row of a reword check has "Fix all". Speccy makes one model call for each section with such findings, and gets that section back rewritten. — One call for each finding sends the whole file each time.
- Speccy lints each rewritten section and drops one that still fails. The author accepts or rejects each section diff, or accepts all. The accepted ones save as one version. — The author stays in control of the doc text.
- The fix control of an answer finding asks for the author's answer first, then writes it into the section in the doc's style, and shows the diff. — With no fact the model writes a placeholder, and a placeholder is a new MUST finding.
- For a suggested fix, Speccy picks the text to replace: the paragraph or the section of the finding. The model returns only the new text. — A model that must copy the old text exactly fails on one changed space.
- A backend reports that an answer was cut off at the token limit. Speccy says so in words and tries once more with a higher limit. — A reasoning model spends the limit on its thinking, and the cut-off answer reads as broken JSON.
- An agent edits the file itself for a local bundle and a GitHub bundle. — The file is on disk or in its checkout, and Speccy lints the save.
- MCP gets one write tool: it saves one file of a bundle that Speccy stores, as a new version, with the version it was based on. It refuses a local or GitHub bundle and says where the file is. — A stored bundle has no file for the agent to edit.
- MCP gets no suggest-fix and no accept-fix tool. — An agent is a model that writes the fix itself.
- `get_findings` returns the fix list with the state of the review: the version the AI review read, the count of sections changed since, and the trend. — An agent must know that an edited section has no AI result until the next review.
- The Findings tab has "Copy for a coding agent". It copies a prompt with the loop: read the fix list, rewrite the reword findings, ask the person every answer in one message, write the answers, run one full review, report the trend. The MCP server instructions hold the same loop. — One review at the end costs model calls only for the changed sections.

## Out
- No fix kind for a profile's own rubric checks. They are answer findings.
- No bulk fix of answer findings.
- No change to how a waiver is asked for or decided.
- The model picker in Admin. It is `docs/specs/admin-model-picker.md`.
- A GitHub file or a pull request as an MCP source (#92), and the pull request surface (#91).

## How I know it works
- A doc with 10 MUST and 150 SHOULD findings shows 10 cards and one row for each SHOULD check, in about one screen.
- "Fix all" on `lint.passive-voice` with 26 findings in 6 sections makes 6 model calls. Accept all saves one version, and lint has no passive-voice finding left in those sections.
- The fix control on a gap asks for an answer. The answer "429, and no provider call" gives a diff that states it in the cited section, with no placeholder.
- A suggested fix through an OpenAI reasoning model on OpenRouter gives a patch, or the message that the answer was cut off. It never gives the JSON error.
- `get_findings` over MCP gives each finding with a line, an end line and a fix kind, and gives the trend.
- An agent that gets the copied prompt rewrites the reword findings, asks the answer findings in one message, runs one review, and reports fixed, still open and new.
- The MCP save tool on a local bundle answers with the path of the file and writes nothing.
