import type { EditorView } from "@codemirror/view";

// Split view keeps the editor and the preview at the same place in the doc (REQ-003). Each
// preview block carries data-line, the first source line of the block.

type Anchor = { line: number; top: number };

function anchors(preview: HTMLElement): Anchor[] {
  const base = preview.getBoundingClientRect().top - preview.scrollTop;
  const out: Anchor[] = [];
  preview.querySelectorAll<HTMLElement>("article [data-line]").forEach((el) => {
    // Nested blocks (list items inside a list) repeat lines; the outer block wins.
    const line = Number(el.dataset.line);
    const top = el.getBoundingClientRect().top - base;
    const last = out[out.length - 1];
    if (!last || line > last.line) out.push({ line, top });
  });
  return out;
}

// previewTopFor returns the preview scrollTop that shows the given source line at the top.
function previewTopFor(list: Anchor[], line: number): number | null {
  if (list.length === 0) return null;
  let i = 0;
  while (i + 1 < list.length && list[i + 1]!.line <= line) i++;
  const a = list[i]!;
  const b = list[i + 1];
  if (!b || line <= a.line) return a.top;
  return a.top + ((line - a.line) / (b.line - a.line)) * (b.top - a.top);
}

// lineFor returns the source line at a preview scrollTop.
function lineFor(list: Anchor[], top: number): number | null {
  if (list.length === 0) return null;
  let i = 0;
  while (i + 1 < list.length && list[i + 1]!.top <= top) i++;
  const a = list[i]!;
  const b = list[i + 1];
  if (!b || top <= a.top) return a.line;
  return a.line + ((top - a.top) / (b.top - a.top)) * (b.line - a.line);
}

export function editorTopLine(view: EditorView): number {
  const block = view.lineBlockAtHeight(view.scrollDOM.scrollTop);
  return view.state.doc.lineAt(block.from).number;
}

export function syncPreview(view: EditorView, preview: HTMLElement) {
  const top = previewTopFor(anchors(preview), editorTopLine(view));
  if (top !== null) preview.scrollTop = Math.max(0, top - 24);
}

export function syncEditor(preview: HTMLElement, view: EditorView) {
  const line = lineFor(anchors(preview), preview.scrollTop + 24);
  if (line === null) return;
  const n = Math.min(Math.max(1, Math.floor(line)), view.state.doc.lines);
  const block = view.lineBlockAt(view.state.doc.line(n).from);
  view.scrollDOM.scrollTop = block.top;
}
