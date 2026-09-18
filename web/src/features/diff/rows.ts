import type { LineOp } from "@/lib/api";

export type Cell = { n: number; text: string } | null;
export type Row = { kind: "equal" | "change"; left: Cell; right: Cell } | { kind: "gap"; hidden: number };

function lines(text: string): string[] {
  const out = text.split("\n");
  if (out[out.length - 1] === "") out.pop();
  return out;
}

// toRows pairs line operations into side-by-side rows. Deleted lines pair with the inserted
// lines that follow them. Runs of equal lines longer than 2 × context collapse to a gap row.
export function toRows(ops: LineOp[], context = 3): Row[] {
  const rows: Row[] = [];
  let ln = 1;
  let rn = 1;
  for (let i = 0; i < ops.length; i++) {
    const op = ops[i]!;
    if (op.op === "equal") {
      for (const t of lines(op.text))
        rows.push({ kind: "equal", left: { n: ln++, text: t }, right: { n: rn++, text: t } });
      continue;
    }
    const del = op.op === "delete" ? lines(op.text) : [];
    let ins = op.op === "insert" ? lines(op.text) : [];
    if (op.op === "delete" && ops[i + 1]?.op === "insert") ins = lines(ops[++i]!.text);
    for (let k = 0; k < Math.max(del.length, ins.length); k++) {
      rows.push({
        kind: "change",
        left: k < del.length ? { n: ln++, text: del[k]! } : null,
        right: k < ins.length ? { n: rn++, text: ins[k]! } : null,
      });
    }
  }
  // Collapse long equal runs.
  const out: Row[] = [];
  let run: Row[] = [];
  const flush = (atStart: boolean, atEnd: boolean) => {
    const keepHead = atStart ? 0 : context;
    const keepTail = atEnd ? 0 : context;
    if (run.length > keepHead + keepTail + 1) {
      out.push(
        ...run.slice(0, keepHead),
        { kind: "gap", hidden: run.length - keepHead - keepTail },
        ...run.slice(run.length - keepTail),
      );
    } else out.push(...run);
    run = [];
  };
  rows.forEach((r) => {
    if (r.kind === "equal") run.push(r);
    else {
      flush(out.length === 0, false);
      out.push(r);
    }
  });
  flush(out.length === 0, true);
  return out;
}
