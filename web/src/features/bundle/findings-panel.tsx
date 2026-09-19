import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Finding } from "@/lib/api";
import {
  approveWaiverMutation,
  getBundleOptions,
  listFindingsOptions,
  listWaiversOptions,
  listWaiversQueryKey,
  rejectWaiverMutation,
  requestWaiverMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { levelStyle } from "./verdict";

const order = { MUST: 0, SHOULD: 1, INFO: 2 } as const;

// FindingsPanel lists the findings of a run: MUST first, then in document order. A click
// opens the text the finding points at. A member can discuss a finding or ask for a waiver
// (REQ-072); the waivers of the bundle follow the findings.
export function FindingsPanel({
  runId,
  bundleId,
  member,
  onOpen,
  onDiscuss,
}: {
  runId?: string;
  bundleId: string;
  member: boolean;
  onOpen: (f: Finding) => void;
  onDiscuss: (f: Finding) => void;
}) {
  const [waiving, setWaiving] = useState<Finding>();
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const { refetch } = findings;
  useEffect(() => {
    if (runId) refetch();
  }, [runId, refetch]);

  if (!runId) return <Empty title="No review yet" />;
  const waivers = <WaiversList bundleId={bundleId} />;
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
      <>
        <Empty title="No findings">Every check passed. SHOULD and INFO findings appear here when a check fails.</Empty>
        {waivers}
      </>
    );
  return (
    <>
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
              {f.waived || member ? (
                <div className="flex items-center gap-3 px-3 pb-2 text-xs">
                  {f.waived ? (
                    <span className="inline-flex items-center gap-1 font-medium text-ok">
                      <ShieldCheck aria-hidden className="size-3.5" /> Waived
                    </span>
                  ) : null}
                  {member ? (
                    <>
                      <button type="button" onClick={() => onDiscuss(f)} className="text-ink-2 hover:text-ink">
                        Discuss
                      </button>
                      {!f.waived && f.level !== "INFO" ? (
                        <button type="button" onClick={() => setWaiving(f)} className="text-ink-2 hover:text-ink">
                          Ask for a waiver
                        </button>
                      ) : null}
                    </>
                  ) : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
      {waivers}
      <WaiverDialog bundleId={bundleId} finding={waiving} onClose={() => setWaiving(undefined)} />
    </>
  );
}

// WaiverDialog asks for a waiver of one finding, with a reason (REQ-072).
function WaiverDialog({ bundleId, finding, onClose }: { bundleId: string; finding?: Finding; onClose: () => void }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const request = useMutation({
    ...requestWaiverMutation(),
    onSuccess: () => {
      setReason("");
      qc.invalidateQueries({ queryKey: listWaiversQueryKey({ path: { bundleId } }) });
      onClose();
    },
  });
  return (
    <Dialog
      open={!!finding}
      onOpenChange={(o) => {
        if (!o) {
          request.reset();
          onClose();
        }
      }}
      title="Ask for a waiver"
      description="A waiver is an approved exception for this check in this section. It ends when the section changes."
    >
      {finding ? (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            request.mutate({ path: { bundleId }, body: { finding_id: finding.id, reason } });
          }}
        >
          <p className="text-sm">
            <span className="font-mono text-xs">{finding.check_slug}</span>
            {finding.anchor.heading_path.length ? (
              <span className="text-ink-2"> in {finding.anchor.heading_path.join(" › ")}</span>
            ) : null}
          </p>
          <Textarea
            aria-label="Reason"
            rows={4}
            className="font-sans text-sm"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Why this check does not apply here. At least 20 characters."
            required
            autoFocus
          />
          {request.isError ? <ErrorState message={problemMessage(request.error)} /> : null}
          <div className="flex justify-end gap-2">
            <Button type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={request.isPending || reason.trim().length < 20}>
              Ask
            </Button>
          </div>
        </form>
      ) : null}
    </Dialog>
  );
}

const waiverStatus = {
  requested: "text-warn",
  approved: "text-ok",
  rejected: "text-ink-3",
  invalidated: "text-ink-3",
} as const;

// WaiversList shows the bundle's waivers, with approve and reject for those who can (§9.1).
function WaiversList({ bundleId }: { bundleId: string }) {
  const qc = useQueryClient();
  const waivers = useQuery(listWaiversOptions({ path: { bundleId } }));
  const done = () => {
    qc.invalidateQueries({ queryKey: listWaiversQueryKey({ path: { bundleId } }) });
    qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
  };
  const approve = useMutation({ ...approveWaiverMutation(), onSuccess: done });
  const reject = useMutation({ ...rejectWaiverMutation(), onSuccess: done });
  const items = waivers.data?.items ?? [];
  if (items.length === 0) return null;
  const error = approve.error ?? reject.error;
  return (
    <section className="border-t border-line">
      <h3 className="px-3 pt-3 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Waivers</h3>
      <ul className="divide-y divide-line">
        {items.map((w) => (
          <li key={w.id} className="px-3 py-2.5 text-sm">
            <p className="flex items-center gap-1.5 text-2xs">
              <span className={clsx("font-semibold tracking-wide uppercase", waiverStatus[w.status])}>{w.status}</span>
              <span className="font-mono text-ink-3">{w.check_slug}</span>
            </p>
            <p className="mt-1 text-ink">{w.reason}</p>
            <p className="mt-0.5 text-xs text-ink-3">
              {w.requested_by}
              {w.section.length ? ` · ${w.section.join(" › ")}` : " · whole doc"}
              {w.status === "requested"
                ? ` · ${w.approvals.length} of ${w.needed} approvals (${w.policy.replace("_", " ")})`
                : ""}
              {w.status === "invalidated" ? " · the section changed" : ""}
            </p>
            {w.can_approve ? (
              <div className="mt-1.5 flex gap-1.5">
                <Button
                  size="sm"
                  variant="primary"
                  onClick={() => approve.mutate({ path: { waiverId: w.id } })}
                  disabled={approve.isPending}
                >
                  Approve
                </Button>
                <Button
                  size="sm"
                  onClick={() => reject.mutate({ path: { waiverId: w.id } })}
                  disabled={reject.isPending}
                >
                  Reject
                </Button>
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      {error ? (
        <div className="p-2">
          <ErrorState message={problemMessage(error)} />
        </div>
      ) : null}
    </section>
  );
}
