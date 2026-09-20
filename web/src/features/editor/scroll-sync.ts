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

// syncPreview moves the preview to the editor's place and returns what it wrote.
export function syncPreview(view: EditorView, preview: HTMLElement): number | null {
  const top = previewTopFor(anchors(preview), editorTopLine(view));
  if (top === null) return null;
  preview.scrollTop = Math.max(0, top - 24);
  return preview.scrollTop;
}

// syncEditor moves the editor to the preview's place and returns what it wrote.
export function syncEditor(preview: HTMLElement, view: EditorView): number | null {
  const line = lineFor(anchors(preview), preview.scrollTop + 24);
  if (line === null) return null;
  const n = Math.min(Math.max(1, Math.floor(line)), view.state.doc.lines);
  const block = view.lineBlockAt(view.state.doc.line(n).from);
  view.scrollDOM.scrollTop = block.top;
  return view.scrollDOM.scrollTop;
}

// ScrollLink keeps the two panes together without a loop. A sync writes the other pane's
// scrollTop, and that write fires a scroll event of its own. The link drops that echo, because a
// sync back to a rounded line puts the pane the person is scrolling behind where it was: during
// a fast scroll the reader sees the text jump backwards.
export class ScrollLink {
  // writing is true while a sync writes the other pane, for the echo that arrives at once.
  private writing = false;
  // wroteEditor and wrotePreview hold the last value written to each pane, for the echo that a
  // browser sends on the next frame.
  private wroteEditor: number | null = null;
  private wrotePreview: number | null = null;

  // fromEditor handles a scroll of the editor.
  fromEditor(view: EditorView, preview: HTMLElement) {
    if (this.writing || near(view.scrollDOM.scrollTop, this.wroteEditor)) {
      this.wroteEditor = null;
      return;
    }
    this.writing = true;
    const wrote = syncPreview(view, preview);
    this.writing = false;
    this.wrotePreview = wrote;
  }

  // fromPreview handles a scroll of the preview.
  fromPreview(preview: HTMLElement, view: EditorView) {
    if (this.writing || near(preview.scrollTop, this.wrotePreview)) {
      this.wrotePreview = null;
      return;
    }
    this.writing = true;
    const wrote = syncEditor(preview, view);
    this.writing = false;
    this.wroteEditor = wrote;
  }
}

// near is true when a pane sits where the last sync put it, give or take a rounded pixel.
function near(top: number, wrote: number | null): boolean {
  return wrote !== null && Math.abs(top - wrote) <= 1;
}
