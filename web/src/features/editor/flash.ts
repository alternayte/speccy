import { StateEffect, StateField } from "@codemirror/state";
import { Decoration, type DecorationSet, EditorView } from "@codemirror/view";

// How long the wash lasts. It matches the preview's flash, so one act looks the same in both
// views.
export const flashMs = 1600;

/** setFlash marks a range as the one the reader was sent to. An empty range clears it. */
export const setFlash = StateEffect.define<{ from: number; to: number }>();

const flashMark = Decoration.mark({ class: "cm-flash" });
const flashLine = Decoration.line({ class: "cm-flash-line" });

// flashField holds the wash. A document change drops it, because the range it marked has moved.
const flashField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, tr) {
    value = value.map(tr.changes);
    for (const e of tr.effects) {
      if (!e.is(setFlash)) continue;
      const { from, to } = e.value;
      if (from >= to) return Decoration.none;
      const ranges = [flashMark.range(from, to)];
      // A range over several lines gets a line wash too, so a whole section reads as one block.
      const first = tr.state.doc.lineAt(from);
      const last = tr.state.doc.lineAt(Math.min(to, tr.state.doc.length));
      if (last.number > first.number) {
        for (let n = first.number; n <= last.number; n++) {
          ranges.push(flashLine.range(tr.state.doc.line(n).from));
        }
      }
      return Decoration.set(ranges, true);
    }
    if (tr.docChanged) return Decoration.none;
    return value;
  },
  provide: (f) => EditorView.decorations.from(f),
});

/** flash is the extension that draws the wash. */
export function flash() {
  return [flashField];
}

/**
 * flashRange sends the view to a range and washes it, the way the preview washes a block. The
 * selection alone is not enough: it paints in the accent's soft tone, which on the editor's
 * surface is too faint to notice.
 */
export function flashRange(view: EditorView, from: number, to: number) {
  view.dispatch({
    selection: { anchor: from, head: to },
    effects: [EditorView.scrollIntoView(from, { y: "center" }), setFlash.of({ from, to })],
  });
  window.setTimeout(() => {
    // The view may be gone by now: a person can switch file or view while it fades.
    if (view.dom.isConnected) view.dispatch({ effects: setFlash.of({ from: 0, to: 0 }) });
  }, flashMs);
}
