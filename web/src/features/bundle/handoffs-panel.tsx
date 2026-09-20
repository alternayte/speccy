import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { PackageCheck } from "lucide-react";
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
      <h3 className="px-4 pt-4 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        Handoffs
      </h3>
      <ul className="divide-y divide-line">
        {items.map((h) => (
          <li key={h.id} className="px-4 py-3.5 text-sm">
            <p className="flex items-center gap-1.5 text-2xs">
              <PackageCheck aria-hidden className={clsx("size-3.5", h.stale ? "text-ink-3" : "text-ok")} />
              <span className="font-semibold text-ink">v{h.version_number}</span>
              <span className="text-ink-3">{relativeTime(h.created_at)}</span>
              {h.stale ? <span className="ml-auto text-warn">stale</span> : null}
            </p>
            <p className="mt-1 text-ink-2">
              {h.taken_by} took the build packet at {verdictLabel[h.verdict] ?? h.verdict}.
              {h.acknowledged ? " The verdict did not allow it." : ""}
            </p>
            {h.label ? <p className="mt-0.5 font-mono text-xs text-ink-3">{h.label}</p> : null}
            {h.stale ? (
              <p className="mt-0.5 text-xs text-warn">
                The bundle is at v{current} now. Tell the builder what changed.
              </p>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
