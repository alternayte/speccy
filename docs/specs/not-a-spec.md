# Not a spec

## What it does
A markdown file that Speccy offers to adopt is often not a spec: a meeting note, a changelog, a readme. Each row in the lists of files that are not bundles yet carries **Not a spec**. The file leaves the list, and the section keeps one line that says how many files carry the mark, with a control that shows them. Each shown file carries **Undo**. The mark lives in Speccy, so the repo and the file take no change.

## Decisions
- The mark lives in Speccy, per workspace, keyed by path — it is a decision about one screen, not a rule about the repo, and a team that wants the rule shared writes a glob in `.speccy.yaml`.
- A dismissed file leaves the working list, and a count with a show control stays — hiding with no trace is a dead end, and a mistake must be two clicks from undone.
- The count shows even when the working list is empty, so the section stays on screen — a person who dismissed every file still needs the way back.
- The mark covers a skipped doc of a GitHub source and a markdown file of the local folder, in one table keyed by source and path — the two lists ask the same question, so they carry the same answer.
- Accepting a type for a marked file clears its mark — the two states contradict each other, and the newer act wins.
- A file the scan later reads as a bundle keeps no mark — a bundle is not offered for adoption, so the row cannot return.

## Out
- Speccy writes nothing to the repo, and nothing into the file.
- No profile for notes, and no verdict for a dismissed file.
- No pattern or glob in this control. One file, one mark.
- The TUI does not carry it. It reviews only.

## How I know it works
- The bundles screen lists a markdown file with no type. **Not a spec** takes the row away.
- The section then reads "1 file marked not a spec — show". The control lists the file.
- **Undo** on that file returns it to the working list, with its guess.
- The same two controls work on the skipped docs of a GitHub source.
- A sync does not bring a marked file back into the working list.
- Accepting a type for a marked file makes the bundle, and the count falls by one.
- The repo takes no commit: the branch's head is the same before and after.
- Marking every file leaves the section on screen, with the count and the show control.
