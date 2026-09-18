import type { StateCommand } from "@codemirror/state";
import { EditorSelection } from "@codemirror/state";

const item = /^(\s*)([-*+]|(\d+)([.)]))(\s+)(\[[ xX]\]\s+)?/;

// continueList is Enter in a markdown list: it starts the next item with the same marker (the
// next number for an ordered list, an open box for a task list). Enter on an empty item ends
// the list. It replaces @codemirror/lang-markdown's command, which the JS budget cannot carry.
export const continueList: StateCommand = ({ state, dispatch }) => {
  const range = state.selection.main;
  if (!range.empty || state.selection.ranges.length > 1) return false;
  const line = state.doc.lineAt(range.head);
  const m = item.exec(line.text);
  if (!m || range.head < line.from + m[0].length) return false;
  const [whole, indent, bullet, num, delim, space, box] = m;
  if (line.text.slice(whole!.length).trim() === "") {
    // An empty item: remove its marker and stop the list.
    dispatch(
      state.update({ changes: { from: line.from, to: line.to, insert: "" }, scrollIntoView: true, userEvent: "input" }),
    );
    return true;
  }
  const marker = num ? `${Number(num) + 1}${delim}` : bullet!;
  const insert = `\n${indent}${marker}${space}${box ? "[ ] " : ""}`;
  dispatch(
    state.update({
      changes: { from: range.head, insert },
      selection: EditorSelection.cursor(range.head + insert.length),
      scrollIntoView: true,
      userEvent: "input",
    }),
  );
  return true;
};
