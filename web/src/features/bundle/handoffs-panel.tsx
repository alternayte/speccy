import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ErrorState, Loading } from "@/components/ui/states";
import { listHandoffsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "./time";

const verdictLabel: Record<string, string> = {
  build_ready: "Build Ready",
  not_build_ready: "Not Build Ready",
  stale: "a stale verdict",
  none: "no review",
};

// HandoffsPanel lists the builders that took this bundle's build packet, newest first
// (REQ-136). A handoff is stale once the bundle has a newer version, which is what tells an
// author their edit landed after someone started building.
export function HandoffsPanel({ bundleId, current }: { bundleId: string; current: number }) {
  const handoffs = useQuery({ ...listHandoffsOptions({ path: { bundleId } }), refetchInterval: 15_000 });
  const items = handoffs.data?.items ?? [];
  if (handoffs.isPending) return <Loading label="Loading handoffs" />;
  if (handoffs.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(handoffs.error)} />
      </div>
    );
  if (items.length === 0) return null;
  return (
    <section aria-label="Handoffs" className="border-t border-line">
      <h3 className="px-4 pt-4 pb-2 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        Handoffs
      </h3>
      <ul className="divide-y divide-line border-t border-line">
        {items.map((h) => {
          const state = h.stale
            ? { label: "Stale", chip: "bg-warn-soft text-warn" }
            : { label: "Current", chip: "bg-ok-soft text-ok" };
          // Every row is two lines that never wrap, so the list keeps one rhythm. The label
          // leads, one tinted chip carries the state, and a stale row names the version the
          // bundle is at now.
          const version = h.stale ? `v${h.version_number} → v${current}` : `v${h.version_number}`;
          const meta = [version, h.taken_by, verdictLabel[h.verdict] ?? h.verdict].filter(Boolean).join(" · ");
          return (
            <li key={h.id} className="px-4 py-3">
              <div className="flex items-baseline gap-2">
                <p className="min-w-0 flex-1 truncate font-mono text-sm text-ink">
                  {h.label || `Version ${h.version_number}`}
                </p>
                <span className="shrink-0 text-xs text-ink-3">{relativeTime(h.created_at)}</span>
              </div>
              <div className="mt-1.5 flex items-center gap-2">
                <span className={clsx("shrink-0 rounded-full px-1.5 py-0.5 text-2xs font-semibold", state.chip)}>
                  {state.label}
                </span>
                <span className="min-w-0 truncate text-xs text-ink-2">
                  {meta}
                  {h.acknowledged ? " (override)" : ""}
                </span>
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
