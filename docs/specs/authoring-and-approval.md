# Authoring and approval

## What it does

A person writes the main doc in the preview. They click any text and type, and a control bar above the preview inserts markdown and applies the fixes that the profile knows about. They drag files from Finder or Explorer into the explorer to add assets, or onto the bundles list to make bundles. A reviewer meets each waiver request on the finding it excuses, in the verdict bar, and in the inbox. The explorer, the review rail, and the split view have dividers that the person drags to the width they want.

## Decisions

- The preview writes the markdown range of the block the person edits — a full re-render to markdown would rewrite untouched lines and destroy the diff.
- A click on a paragraph, a heading, a list item, a quote, or a table cell edits it in place — these hold the prose of a spec.
- A click on a code block, a diagram, an image, or the frontmatter shows its raw markdown in the same place — rendering those back loses text that matters.
- There is no edit mode and no dialog: a click puts the caret where the person clicked — a mode is one more thing to learn.
- An edit marks the file dirty, and the Save button or ⌘S writes it, as the code view does now — a version is what a verdict and a waiver hash point at, so a silent save would invalidate a waiver while a person types.
- The control bar holds markdown controls (heading, bold, italic, inline code, link, bullet list, numbered list, task, quote, table picker, code block) and profile controls (insert a missing required heading, insert the next free trace ID, insert a requirement with an acceptance criterion, move the block to `assets/` and leave a link).
- Every control writes text and leaves the file dirty — the person undoes it like any edit.
- One button toggles the whole bar, and the choice stays per person in the browser, like the overlay choice — a writer wants it, a reader does not.
- The bar shows only for a markdown file that the person can edit — it has nothing to do for an asset or a guest.
- A drop on the explorer adds assets to the open bundle, at the folder under the pointer — the explorer means "inside this bundle".
- A drop on the bundles list makes one bundle per folder, a single-file bundle per loose markdown file, and sends a `.zip` to the import path — the list means "the set of bundles".
- A drop that clashes with a file name opens one dialog with Replace and Keep both, and Replace is preselected — a newer diagram over an old one is the common case, and a silent overwrite of reviewed text is not acceptable.
- A replaced file lands in the next version — the diff and the verdict then show it.
- The findings list sorts a finding with a requested waiver to the top, and Approve and Reject sit on that finding — the reason and the text it excuses belong together.
- The verdict bar shows how many waivers wait for approval, with a button to the first one — the verdict is what the approver came to change.
- The inbox lists each waiver request for the people whose policy lets them approve it — a profile maintainer is not always on the bundle.
- The waivers section of the rail keeps the approved and the rejected waivers — it is the record, not the queue.
- Three dividers resize: explorer against editor, editor against rail, and code against preview. Each divider takes a drag, and the arrow keys when it has focus.
- Each width stays in browser storage, with a minimum width per pane, and a double click resets it — a width is a preference of one screen, not a fact about the bundle.
- Below the `lg` breakpoint the explorer and the rail stay overlays — a phone has no room for three panes.
- Speccy adds no resizing library — the pane code is short, and `react-resizable-panels` would be a dependency for 60 lines.
- Fast scrolling in the preview stays smooth — the overlay marks and the scroll sync are the cause, and they are measured and fixed with the rest of this work.

## Out

- Rich-text editing of code blocks, diagrams, and frontmatter.
- A document outline, find and replace, and a spell checker.
- Drag and drop between two bundles, and drag to reorder files.
- Waiver approval from the tour and from the CLI.
- Pane widths that follow a person to another device.

## How I know it works

- A person clicks a paragraph in the preview, types, and saves. The version number rises by one, and `git diff` on the bundle folder shows only the edited lines.
- A person clicks a code block. The raw markdown appears in place, with the fence.
- A person presses the control bar's "missing heading" control. The heading of the profile template appears in the doc, and the next review has no `lint.required-headings` finding.
- A person drags two PNG files onto the explorer. Both appear in the bundle, and `ls` on the bundle folder shows them on disk.
- A person drags a folder onto the bundles list. A bundle appears with that folder's markdown file as its main doc.
- A person drops a file with the name of an asset. The dialog offers Replace and Keep both.
- A member asks for a waiver. The approver opens the bundle, and the finding with the request is the first one in the list, with Approve on it. The verdict bar says one waiver waits.
- A maintainer of the profile who is not an author opens the inbox and sees the request.
- A person drags the divider between the explorer and the editor. The explorer keeps its width after a reload, and the layout of another browser does not change.
- A person scrolls the preview of the SDD at full speed. The scroll stays at 60 frames per second in the browser's performance panel, with the overlay on.
