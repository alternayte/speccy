import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { CircleCheck, CircleHelp, CircleX, Lightbulb } from "lucide-react";
import { useEffect } from "react";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Anchor, Claim } from "@/lib/api";
import { listAssumptionsOptions, listClaimsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

const labelStyle = {
  verified: { icon: CircleCheck, tone: "text-ok", text: "Verified" },
  contradicted: { icon: CircleX, tone: "text-bad", text: "Contradicted" },
  unverified: { icon: CircleHelp, tone: "text-warn", text: "Unverified" },
} as const;

// EvidencePanel lists the claims of the last full review with their labels (REQ-031), and
// the doc's assumptions, which claim checks skip (REQ-033).
export function EvidencePanel({
  bundleId,
  runId,
  version,
  onOpen,
}: {
  bundleId: string;
  runId?: string;
  version: string;
  onOpen: (a: Anchor) => void;
}) {
  const claims = useQuery({ ...listClaimsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const assumptions = useQuery(listAssumptionsOptions({ path: { bundleId } }));
  const { refetch } = assumptions;
  useEffect(() => {
    refetch();
  }, [version, refetch]);

  return (
    <div>
      <h3 className="px-4 pt-4 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Claims</h3>
      {!runId ? (
        <p className="px-4 py-2 text-xs text-ink-3">Run a full review to check the doc's factual claims.</p>
      ) : claims.isPending ? (
        <Loading label="Loading claims" />
      ) : claims.isError ? (
        <div className="p-2">
          <ErrorState message={problemMessage(claims.error)} />
        </div>
      ) : claims.data.items.length === 0 ? (
        <Empty title="No factual claims">The review found no claims to check against a source.</Empty>
      ) : (
        <ul className="divide-y divide-line">
          {claims.data.items.map((c) => (
            <ClaimRow key={c.id} claim={c} onOpen={onOpen} />
          ))}
        </ul>
      )}
      <h3 className="mt-2 px-4 pt-4 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        Assumptions
      </h3>
      {assumptions.data && assumptions.data.items.length > 0 ? (
        <ul className="divide-y divide-line">
          {assumptions.data.items.map((a) => (
            <li key={a.start}>
              <button
                type="button"
                onClick={() => onOpen(a)}
                className="flex w-full items-start gap-2 px-4 py-3.5 text-left text-sm transition-colors hover:bg-sunken"
              >
                <Lightbulb aria-hidden className="mt-0.5 size-3.5 shrink-0 text-ink-3" />
                <span className="text-ink-2">{a.quote}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="px-4 py-2 text-xs text-ink-3">
          Start a sentence with “Assumption:” to state something you cannot source. Claim checks skip it.
        </p>
      )}
    </div>
  );
}

function ClaimRow({ claim: c, onOpen }: { claim: Claim; onOpen: (a: Anchor) => void }) {
  const { icon: Icon, tone, text } = labelStyle[c.label];
  return (
    <li className="px-4 py-3.5 transition-colors hover:bg-sunken">
      <button type="button" onClick={() => onOpen(c.anchor)} className="block w-full text-left">
        <span className={clsx("inline-flex items-center gap-1 text-2xs font-semibold tracking-wide", tone)}>
          <Icon aria-hidden className="size-3.5" />
          {text}
        </span>
        <span className="mt-1 block text-sm text-ink">{c.text}</span>
        {c.reason ? <span className="mt-1 block text-xs text-ink-2">{c.reason}</span> : null}
      </button>
      {c.sources.length ? (
        <ul className="mt-1 space-y-0.5">
          {c.sources.map((s) => (
            <li key={s} className="truncate text-xs">
              {/^https?:\/\//.test(s) ? (
                <a href={s} target="_blank" rel="noreferrer noopener" className="text-accent underline">
                  {s}
                </a>
              ) : (
                <span className="text-ink-2">{s}</span>
              )}
            </li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}
