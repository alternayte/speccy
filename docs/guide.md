# The guide: one doc, from a blank page to a build packet

This guide follows one bundle end to end. It uses the payments PRD that ships in `testdata/bundles`. Every picture comes from the real app. `just docs-shots` captures them again.

Speccy answers one question about a doc: Build Ready, or Not Build Ready. Build Ready means an implementer can build the thing without asking you what you meant.

## 1. Start Speccy

You need Go 1.26, Node 24 or later, and [just](https://just.systems).

```sh
git clone https://github.com/alternayte/speccy && cd speccy
just build
./bin/speccy --dir ~/work/specs
```

Speccy serves the folder you name and opens your browser. `--no-open` starts it without the browser. The state lives in `.speccy/state/` beside your docs. Do not commit that folder. `speccy init` writes a `.gitignore` entry for it.

The bundles screen lists every bundle in the folder, with its verdict.

![The bundles screen: one row per bundle, with its verdict](images/guide-bundles.png)

## 2. Make a bundle

A bundle is a folder with one main doc and its assets. The main doc is the one markdown file whose frontmatter has a `type`:

```markdown
---
type: prd
title: Payment retries
size: feature
---
```

Three ways to start one:

- **New bundle** writes the profile's template.
- Drag a folder or a file from Finder or Explorer onto the bundles screen. Speccy makes one bundle per folder, and a single-file bundle per loose markdown file.
- `speccy init` in a repo that holds specs already. Then set the paths in `.speccy.yaml`. See [configuration.md](configuration.md).

A doc written before Speccy names no type. **Import** takes it anyway: it guesses the type from the headings, shows the guess, and lets you pick another. It then writes one line, `type: <key>`, at the top of the file it creates, and changes nothing else. A drop whose type Speccy cannot guess opens the same dialog with the file in it.

The bundles screen also lists the markdown files in the served folder that name no type. **Adopt** writes the type into one, and the doc becomes a bundle. `speccy init` does the same in a terminal, and Enter takes the guess.

A doc in a GitHub source still needs the type in the repo. Speccy makes no commit there, and the source says which files need one.

![The new bundle dialog: the title, the profile, and the size](images/guide-new-bundle.png)

Pick the size the doc covers: a feature, an app, or an initiative. The profile asks for less of a feature doc than of an initiative doc.

## 3. Write the doc

Open the bundle. One row sits above the doc: the title, the verdict in words, and one button that names the next thing to do. Everything else is in **More**. The screen shows only what this doc has earned, so a new doc has no findings tab and no file tree.

![The bundle screen: one control row, the doc, and the rail](images/guide-editor.png)

The preview opens first. **Split** puts the markdown beside it, scrolled together, and **Code** shows the markdown alone. Speccy remembers your choice.

The preview is editable. Click a paragraph, a heading, a list item, a quote, or a table cell, and the markdown of that block opens where you clicked. `Esc` leaves the block as it was. ⌘S writes the file as one version.

![Click the text in the preview and type](images/guide-edit-preview.gif)

A code block, a diagram, an image, and the frontmatter open as raw markdown, with the fence and the marks, because the exact text matters.

**Controls** shows the control bar above the preview. Its markdown group writes headings, lists, links, tables, and code blocks. Its profile group applies what the profile knows: a missing required heading, the next free trace ID, a requirement with an acceptance criterion, or a move of a long block into `assets/`.

Lint runs on every save, so the first findings appear before any model does.

## 4. Add a model

Lint alone gives a lint-only verdict. The other stages need a model. Open **Admin → Models** and add one of:

- an API key for Anthropic, OpenAI, OpenRouter, or DeepSeek;
- an agent CLI you already pay for: `claude`, `cursor-agent`, `opencode`, or `pi`.

Give each role a backend and a model: the reviewer, the three readers, the judge, and the writer. **Test** checks a backend with one short call.

![Admin, Models: a backend and a model for each role](images/guide-models.png)

## 5. Run the review

The next action on a doc with no current check says **Check this doc**. It shows the estimated cost first, then runs every stage. **More → Run a review** does the same at any time.

| Stage | What it asks | Needs a model |
|---|---|---|
| Lint | Is the writing clear, and are the headings, links, and trace IDs in order? | No |
| Rubric | Does the doc answer what its profile requires? | Yes |
| Grounding | Does each factual claim hold, and what says so? | Yes |
| Divergence | Do independent readers get the same meaning from each build question? | Yes |
| Coherence | Does the doc agree with the docs it links to, and cover their IDs? | Yes |

![The review runs stage by stage and ends on a verdict](images/guide-run-review.gif)

The verdict is the answer. It sits under the title, in words. Build Ready means: no open MUST finding, every required link or acknowledgement is there, no blocking thread is open, and the run is on the current version. The score beside it is passed checks divided by applicable checks. It is for metrics. It never decides the verdict.

![The control row after a review: the verdict, and the next thing to fix](images/guide-verdict.png)

An older version's verdict is stale. Speccy says so and asks for a new run.

## 6. Follow the next action

The button in the control row always names one thing, and it is the only primary button on the screen. The order is fixed: a waiver that waits for you, then a question from the tour, then the first MUST finding, then a check of the current version, then the frontmatter, then the reviews the profile needs, then the handoff. The bundles list names the same thing for every doc, so you can pick the work before you open it.

## 7. Read the findings

Each finding anchors to the text that failed a check. The overlay marks that text in the preview, one layer per kind. Risk and Ambiguous are on by default. The rest are one click away.

![The findings rail, beside the text each finding points at](images/guide-findings.png)

A finding carries a fix when the fix is certain, such as a missing trace ID, or a broken link to a file that exists under another name.

The **Evidence** tab holds the build questions: what an implementer must know to build the thing. A gap means every reader answered that the doc does not say. A divergence means the readers answered differently.

![The build questions, with the gaps and the divergences](images/guide-questions.png)

The run report shows the stages, their timings, and the findings by category.

![The run report](images/guide-run-report.png)

## 8. Take the tour

**Tour** lists the points that need a person, in order: a blocking thread, a divergence, a gap, a contradiction, an open decision. The doc dims around the section in question.

![The tour moves point to point](images/guide-tour.gif)

`j` and `k` move. `d` records a decision. `w` asks for a waiver. `c` opens a comment. `Esc` leaves.

A waiver is an approved exception for one check in one section, with a reason of at least 20 characters. The profile's waiver policy says who may approve: any member, a non-author, N distinct non-authors, a profile maintainer, or nobody. An approved waiver is written into the main doc's frontmatter, so it travels with the doc in git. Any edit of that section ends the waiver, and the check runs again.

An acknowledgement uses the same mechanism for a link or a trace item that is intentionally absent.

## 9. Send it to a reviewer

**Share** makes a link. A person who opens it reads the spec, and answers the questions you have for them. They see no findings, no waivers, and no author tools.

![Reviewer mode: the spec, the attachments, and one status line](images/guide-reviewer.png)

They select any words and press **Comment** to start a thread on that text. **Answer the questions** walks them through the points that wait for them.

![The questions a reviewer must answer](images/guide-reviewer-questions.png)

To see the same screen yourself, add `?as=reviewer` to the bundle's URL.

## 10. Reach Build Ready

Fix what the findings and the tour raised. Save. Run the review again. Build Ready means the doc is ready to hand over.

![Build Ready. This doc passes every lint check, so its verdict says so](images/guide-build-ready.png)

**Traceability** shows every upstream ID, and whether this doc references it, covers it elsewhere, or leaves a gap.

![The traceability matrix](images/guide-trace.png)

A bundle reaches `approved` when it has a current Build Ready verdict and the approvals its profile requires. An author cannot approve their own bundle. Any change to the main doc or its assets revokes the approvals, and the bundle returns to `in_review`.

## 11. Hand it to a builder

A build packet is the main doc, its assets, the linked bundles' main docs, the trace IDs, and the build questions with their agreed answers. A coding agent takes the packet, and Speccy records the handoff with the verdict at that moment. `HANDOFF.md`, the re-entry prompt, lets the agent resume after it loses its context.

![History: the versions of the bundle, and the handoffs a builder took](images/guide-handoff.png)

The agent sends back a build report. A blocked report says it cannot build a section without an answer, and it opens a blocking thread. A note says it built the section, but the doc was unclear. A note changes no verdict.

For one profile, the false-ready rate is the share of Build Ready handoffs that came back blocked. **Insights** shows it.

## 12. Keep the verdict in CI

`speccy review docs/specs/*` gives the same verdict in a terminal. The GitHub Action posts the findings on the pull request. See [cli-and-tui.md](cli-and-tui.md) and [configuration.md](configuration.md).
