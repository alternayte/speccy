import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ErrorState, Loading } from "@/components/ui/states";
import { listVerificationsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "./time";

// VerificationsPanel lists the verification runs of this bundle, newest first. A run is a
// statement about one commit: it keeps its outcomes, and a new bundle version makes it stale
// because the requirements moved. A run carries its own verdict and never the bundle's.
export function VerificationsPanel({ bundleId }: { bundleId: string }) {
  const runs = useQuery({ ...listVerificationsOptions({ path: { bundleId } }), refetchInterval: 15_000 });
  const items = runs.data?.items ?? [];
  if (runs.isPending) return <Loading label="Loading verification runs" />;
  if (runs.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(runs.error)} />
      </div>
    );
  if (items.length === 0) return null;
  return (
    <section aria-label="Verification runs" className="border-t border-line">
      <h3 className="px-4 pt-4 pb-2 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        Verification
      </h3>
      <ul className="divide-y divide-line border-t border-line">
        {items.map((v) => {
          const state = v.stale
            ? { label: "Stale", chip: "bg-warn-soft text-warn" }
            : v.verdict === "verified"
              ? { label: "Verified", chip: "bg-ok-soft text-ok" }
              : { label: "Not Verified", chip: "bg-bad-soft text-bad" };
          const c = v.counts;
          const meta = [
            `${c.implemented} implemented`,
            `${c.untested} untested`,
            `${c.unproven} unproven`,
            `${c.missing} missing`,
            `${c.breached} breached`,
          ].join(" · ");
          const at = v.sha ? v.sha.slice(0, 7) : "a folder";
          return (
            <li key={v.id} className="px-4 py-3">
              <div className="flex items-baseline gap-2">
                <p className="min-w-0 flex-1 truncate font-mono text-sm text-ink">
                  {v.repo} @ {at}
                </p>
                <span className="shrink-0 text-xs text-ink-3">{relativeTime(v.created_at)}</span>
              </div>
              <div className="mt-1.5 flex items-center gap-2">
                <span className={clsx("shrink-0 rounded-full px-1.5 py-0.5 text-2xs font-semibold", state.chip)}>
                  {state.label}
                </span>
                <span className="min-w-0 truncate text-xs text-ink-2">
                  {meta}
                  {c.waived > 0 ? ` · ${c.waived} waived` : ""}
                </span>
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
