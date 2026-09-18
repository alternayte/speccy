import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { useEffect } from "react";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Finding } from "@/lib/api";
import { listFindingsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { levelStyle } from "./verdict";

const order = { MUST: 0, SHOULD: 1, INFO: 2 } as const;

// FindingsPanel lists the findings of a run: MUST first, then in document order. A click
// opens the text the finding points at.
export function FindingsPanel({ runId, onOpen }: { runId?: string; onOpen: (f: Finding) => void }) {
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const { refetch } = findings;
  useEffect(() => {
    if (runId) refetch();
  }, [runId, refetch]);

  if (!runId) return <Empty title="No review yet" />;
  if (findings.isPending) return <Loading label="Loading findings" />;
  if (findings.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(findings.error)} />
      </div>
    );
  const items = [...findings.data.items].sort((a, b) => order[a.level] - order[b.level]);
  if (items.length === 0)
    return (
      <Empty title="No findings">Every check passed. SHOULD and INFO findings appear here when a check fails.</Empty>
    );
  return (
    <ul className="divide-y divide-line">
      {items.map((f) => {
        const { icon: Icon, tone, label } = levelStyle[f.level];
        return (
          <li key={f.id}>
            <button
              type="button"
              onClick={() => onOpen(f)}
              className="group block w-full px-3 py-2.5 text-left transition-colors hover:bg-sunken"
            >
              <div className="flex items-center gap-1.5">
                <Icon aria-hidden className={clsx("size-3.5 shrink-0", tone)} />
                <span className={clsx("text-2xs font-semibold tracking-wide", tone)}>{label}</span>
                <span className="min-w-0 truncate font-mono text-2xs text-ink-3">{f.check_slug}</span>
                {f.relaxed ? <span className="ml-auto text-2xs text-warn">relaxed</span> : null}
              </div>
              <p className="mt-1 text-sm text-ink">{f.message}</p>
              {f.anchor.quote.trim() ? (
                <p className="mt-1 line-clamp-2 border-l-2 border-line-strong pl-2 font-mono text-xs text-ink-2 group-hover:border-accent">
                  {f.anchor.quote}
                </p>
              ) : null}
              {f.anchor.heading_path.length ? (
                <p className="mt-1 truncate text-2xs text-ink-3">{f.anchor.heading_path.join(" › ")}</p>
              ) : null}
              {f.fix ? <p className="mt-1 text-xs text-ink-2">Fix: {f.fix}</p> : null}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
