import { clsx } from "clsx";
import type { LineOp } from "@/lib/api";
import { type Cell, toRows } from "./rows";

// SideBySide shows a line diff in two columns (REQ-006). On a narrow screen the columns stack.
export function SideBySide({ ops }: { ops: LineOp[] }) {
  const rows = toRows(ops);
  if (rows.length === 0) return <p className="px-3 py-2 text-xs text-ink-3">No text.</p>;
  return (
    <div className="overflow-x-auto font-mono text-xs leading-5" role="table" aria-label="Line differences">
      {rows.map((r, i) =>
        r.kind === "gap" ? (
          <div key={i} role="row" className="bg-sunken px-3 py-0.5 text-2xs text-ink-3">
            {r.hidden} unchanged lines
          </div>
        ) : (
          <div key={i} role="row" className="grid grid-cols-1 md:grid-cols-2">
            <Side cell={r.left} tone={r.kind === "change" ? "del" : "eq"} hideOnNarrow={r.kind === "equal"} />
            <Side
              cell={r.right}
              tone={r.kind === "change" ? "add" : "eq"}
              hideOnNarrow={r.kind === "change" && !r.right}
            />
          </div>
        ),
      )}
    </div>
  );
}

function Side({ cell, tone, hideOnNarrow }: { cell: Cell; tone: "add" | "del" | "eq"; hideOnNarrow: boolean }) {
  const mark = tone === "add" ? "+" : tone === "del" ? "−" : " ";
  return (
    <div
      role="cell"
      className={clsx(
        "flex min-w-0 md:border-r md:border-line",
        hideOnNarrow && "hidden md:flex",
        cell === null && "hidden bg-sunken md:flex",
        cell && tone === "add" && "bg-[var(--diff-add)]",
        cell && tone === "del" && "bg-[var(--diff-del)]",
      )}
    >
      <span aria-hidden className="w-10 shrink-0 pr-2 text-right text-ink-3 select-none">
        {cell?.n ?? ""}
      </span>
      <span aria-hidden className="w-3 shrink-0 text-ink-3 select-none">
        {cell ? mark : ""}
      </span>
      <span className="min-w-0 flex-1 pr-3 break-words whitespace-pre-wrap">
        {cell ? (
          <span className="sr-only">{tone === "add" ? "Added: " : tone === "del" ? "Removed: " : ""}</span>
        ) : null}
        {cell?.text ?? ""}
      </span>
    </div>
  );
}
