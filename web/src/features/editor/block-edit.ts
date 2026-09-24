// The editable preview writes the markdown of one block at a time (SDD §6.2, REQ-003). A click
// opens the block's markdown where the person clicked, and a commit replaces that byte range in
// the file. Speccy never rebuilds the whole file from the rendered HTML: a doc lives in git, and
// a rebuild would change lines nobody edited.

export type Block = { start: number; end: number; el: HTMLElement };

// blockAt returns the smallest block that holds the click, with its source range. A table is
// one block: its rows and cells carry ranges too, but a cell's range is one line of the table.
export function blockAt(target: Element | null): Block | null {
  const el = target?.closest<HTMLElement>("table[data-src-start]") ?? target?.closest<HTMLElement>("[data-src-start]");
  if (!el) return null;
  const start = Number(el.dataset.srcStart);
  const end = Number(el.dataset.srcEnd);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null;
  return { start, end, el };
}

// visiblePrefix is the rendered text of the block before the click.
export function visiblePrefix(block: HTMLElement, x: number, y: number): string | null {
  const pos = caretAt(x, y);
  if (!pos || !block.contains(pos.node)) return null;
  const range = document.createRange();
  range.setStart(block, 0);
  range.setEnd(pos.node, Math.min(pos.offset, pos.node.textContent?.length ?? 0));
  return range.toString();
}

function caretAt(x: number, y: number): { node: Node; offset: number } | null {
  type WithCaret = Document & {
    caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null;
    caretRangeFromPoint?: (x: number, y: number) => Range | null;
  };
  const d = document as WithCaret;
  const p = d.caretPositionFromPoint?.(x, y);
  if (p) return { node: p.offsetNode, offset: p.offset };
  const r = d.caretRangeFromPoint?.(x, y);
  return r ? { node: r.startContainer, offset: r.startOffset } : null;
}

// caretInSource maps a rendered prefix to an offset in the block's markdown. Markdown marks and
// runs of whitespace do not count, so a click after "**REQ-001:** An agent" lands after the same
// words in the source. With no match the caret goes to the end, which never loses text.
export function caretInSource(source: string, prefix: string | null): number {
  if (prefix === null) return source.length;
  const words = prefix.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return 0;
  const tail = words.slice(-4).map(escape).join("[^\\w]{0,8}");
  const m = source.match(new RegExp(tail, "i"));
  if (m && m.index !== undefined) return m.index + m[0].length;
  // No match: keep the place in proportion, so a long block does not jump to its end.
  const text = source.replace(/\s+/g, " ").trim();
  return text.length === 0 ? source.length : Math.round((prefix.length / text.length) * source.length);
}

function escape(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// The server counts bytes, and JavaScript counts UTF-16 units, so a doc with "§" or "—" needs
// the text as bytes before a block range means anything.
const encoder = new TextEncoder();
const decoder = new TextDecoder();

// slice is the markdown of one block.
export function slice(markdown: string, block: { start: number; end: number }): string {
  return decoder.decode(encoder.encode(markdown).slice(block.start, block.end));
}

// splice replaces the block's range in the file.
export function splice(markdown: string, block: { start: number; end: number }, value: string): string {
  const bytes = encoder.encode(markdown);
  const head = decoder.decode(bytes.slice(0, block.start));
  const tail = decoder.decode(bytes.slice(block.end));
  return head + value + tail;
}
