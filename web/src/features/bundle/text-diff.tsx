import { clsx } from "clsx";

type Part = { op: "equal" | "delete" | "insert"; text: string };

// maxCells bounds the diff table. A text pair above it shows as two blocks.
const maxCells = 4_000_000;

// tokens splits text into words and the white space between them, so a diff keeps the layout.
function tokens(text: string): string[] {
  return text.match(/\s+|\w+|[^\s\w]/g) ?? [];
}

// diffWords compares two texts word by word, with the longest common subsequence. It returns
// undefined for a pair too long to compare.
export function diffWords(before: string, after: string): Part[] | undefined {
  const a = tokens(before);
  const b = tokens(after);
  if (a.length * b.length > maxCells) return undefined;
  const w = b.length + 1;
  const table = new Uint32Array((a.length + 1) * w);
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--)
      table[i * w + j] =
        a[i] === b[j] ? table[(i + 1) * w + j + 1]! + 1 : Math.max(table[(i + 1) * w + j]!, table[i * w + j + 1]!);
  const out: Part[] = [];
  const push = (op: Part["op"], text: string) => {
    const last = out[out.length - 1];
    if (last?.op === op) last.text += text;
    else out.push({ op, text });
  };
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      push("equal", a[i]!);
      i++;
      j++;
    } else if (table[(i + 1) * w + j]! >= table[i * w + j + 1]!) push("delete", a[i++]!);
    else push("insert", b[j++]!);
  }
  while (i < a.length) push("delete", a[i++]!);
  while (j < b.length) push("insert", b[j++]!);
  return regions(out);
}

// regions joins the changes that only white space separates into one removed text and one
// added text, so a reworded phrase reads as two phrases and not as words in turn.
function regions(parts: Part[]): Part[] {
  const out: Part[] = [];
  let del = "";
  let ins = "";
  const flush = () => {
    if (del) out.push({ op: "delete", text: del });
    if (ins) out.push({ op: "insert", text: ins });
    del = ins = "";
  };
  parts.forEach((p, k) => {
    if (p.op === "delete") del += p.text;
    else if (p.op === "insert") ins += p.text;
    else if ((del || ins) && !p.text.trim() && !p.text.includes("\n") && parts[k + 1] && parts[k + 1]!.op !== "equal") {
      del += p.text;
      ins += p.text;
    } else {
      flush();
      out.push(p);
    }
  });
  flush();
  return out;
}

// TextDiff shows what a fix changes in one text: the words it removes and the words it adds,
// in place. New text with nothing before it shows as added.
export function TextDiff({ before, after, className }: { before: string; after: string; className?: string }) {
  const parts = before ? diffWords(before, after) : [{ op: "insert" as const, text: after }];
  if (!parts)
    return (
      <div className={className}>
        <pre className="max-h-40 overflow-auto rounded-sm bg-[var(--diff-del)] px-1.5 py-1 font-mono whitespace-pre-wrap line-through decoration-ink-3">
          {before}
        </pre>
        <pre className="mt-1 max-h-40 overflow-auto rounded-sm bg-[var(--diff-add)] px-1.5 py-1 font-mono whitespace-pre-wrap">
          {after}
        </pre>
      </div>
    );
  return (
    <pre
      className={clsx(
        "overflow-auto rounded-sm border border-line bg-surface px-2 py-1.5 font-mono leading-relaxed whitespace-pre-wrap text-ink",
        className,
      )}
    >
      {parts.map((p, i) =>
        p.op === "equal" ? (
          <span key={i}>{p.text}</span>
        ) : p.op === "delete" ? (
          <del key={i} className="bg-[var(--diff-del)] decoration-ink-3">
            {p.text}
          </del>
        ) : (
          <ins key={i} className="bg-[var(--diff-add)] no-underline">
            {p.text}
          </ins>
        ),
      )}
    </pre>
  );
}
