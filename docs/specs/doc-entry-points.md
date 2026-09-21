# The way in for a doc that Speccy did not write

## What it does
Import and drag-and-drop accept a markdown file with no frontmatter. Speccy guesses the profile from the headings, shows the guess in the import dialog, and lets the person pick another type before it imports. It then writes one line, `type: <key>`, into the frontmatter of the file it creates. A drop whose guess finds no profile opens the import dialog with the file in it, instead of making a bundle. The bundles screen lists the markdown files the local scan skipped, and one control per file writes the guessed type into that file. `speccy init` offers the same guess as its default answer.

## Decisions
- A file the person hands to Speccy needs no type; a file the scan finds still does — the person who drops a file asks for a bundle, and a folder scan does not.
- The import dialog shows the guessed type and lets the person change it — a silently wrong profile makes every check wrong.
- A drop with no guess opens the import dialog with the file in it — the app asks at the moment the person is already deciding.
- Import writes `type: <key>` into the file, in local mode and in hosted mode — one rule for both modes, and the bundle survives a rescan of the folder.
- The dialog says the file gains that line before the import runs — the author sees the change before it happens, in a file they are about to commit.
- Import changes nothing else in the text — Speccy edits the markdown on disk, so `git diff` stays readable.
- The bundles screen lists the skipped markdown files with a control that adopts each one — a person in the app does not go back to a terminal to get their docs in.
- The control and `speccy init` write the same line through the same code — two ways in must not make two results.
- A half-written doc gets no softer state — the verdict already says Not Build Ready, and the next action names the one thing to fix first.
- A GitHub source still needs a type in the file — a repo is a tree with branches and other people's work, so Speccy makes no surprise commit there.

## Out
- No change to the local scan's rule for a folder bundle.
- No change to the review stages, the verdict rule, or the next action's order.
- No new profile, and no change to the guess itself.
- No write to a GitHub repo.

## How I know it works
- Import a markdown file with no frontmatter. The dialog names the guessed type before the import. The bundle opens, and the file on disk now starts with `type: <key>` and its original text.
- Change the type in the dialog before the import. The file gets the type you picked.
- Drag a markdown file with no frontmatter and no heading that matches any profile onto the bundles screen. The import dialog opens with that file in it. No bundle exists until you confirm.
- Restart the server on the same folder. The imported bundle is still there.
- Put a markdown file with no frontmatter directly in the served folder. The bundles screen lists it as skipped. Press its control. The file gains the type, and the bundle appears.
- Run `speccy init` in a folder with a loose doc. The prompt names the guessed profile, and Enter accepts it.
- Add a GitHub source for a file with no type. Speccy refuses, and says to add the type in the repo.
