import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ShieldCheck, Unlink, Wand2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Finding, FixSuggestion } from "@/lib/api";
import {
  acceptFixMutation,
  approveWaiverMutation,
  getBundleOptions,
  listBundleThreadsOptions,
  listFindingsOptions,
  listWaiversOptions,
  listWaiversQueryKey,
  rejectWaiverMutation,
  requestWaiverMutation,
  suggestFixMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { levelStyle } from "./verdict";

const order = { MUST: 0, SHOULD: 1, INFO: 2 } as const;

// FindingsPanel lists the findings of a run: MUST first, then in document order. A click
// opens the text the finding points at. A member can discuss a finding or ask for a waiver
// (REQ-072), and an author can ask for a fix (REQ-025); the waivers of the bundle follow the
// findings, then the detached findings and threads (SDD §8.8).
export function FindingsPanel({
  runId,
  bundleId,
  member,
  canEdit,
  selected,
  onOpen,
  onDiscuss,
}: {
  runId?: string;
  bundleId: string;
  member: boolean;
  canEdit: boolean;
  selected?: string;
  onOpen: (f: Finding) => void;
  onDiscuss: (f: Finding) => void;
}) {
  const [waiving, setWaiving] = useState<Finding>();
  const selectedRef = useRef<HTMLLIElement>(null);
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [selected]);
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const requests = useQuery({ ...listWaiversOptions({ path: { bundleId } }), refetchInterval: 5000 });
  const decide = useDecide(bundleId);
  // pending is the waiver request of a finding: same check, and the same section or the whole doc.
  const pending = (f: Finding) =>
    (requests.data?.items ?? []).find(
      (w) =>
        w.status === "requested" &&
        w.check_slug === f.check_slug &&
        (w.section.length === 0 || w.section.join(" › ") === f.anchor.heading_path.join(" › ")),
    );
  const { refetch } = findings;
  useEffect(() => {
    if (runId) refetch();
  }, [runId, refetch]);

  if (!runId) return <Empty title="No review yet" />;
  const waivers = (
    <>
      <WaiversList bundleId={bundleId} />
      <DetachedList bundleId={bundleId} findings={findings.data?.items ?? []} />
    </>
  );
  if (findings.isPending) return <Loading label="Loading findings" />;
  if (findings.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(findings.error)} />
      </div>
    );
  // A finding whose waiver waits for a decision goes to the top: the approver came for it, and
  // the reason belongs beside the text it excuses (SDD §9.1).
  const items = findings.data.items
    .filter((f) => !f.anchor.detached)
    .sort((a, b) => rank(pending(a)) - rank(pending(b)) || order[a.level] - order[b.level]);
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
            <li
              key={f.id}
              ref={f.id === selected ? selectedRef : undefined}
              className={clsx(f.id === selected && "bg-accent-soft/60")}
            >
              <button
                type="button"
                onClick={() => onOpen(f)}
                className="group block w-full px-4 py-3.5 text-left transition-colors hover:bg-sunken"
              >
                <div className="flex items-center gap-1.5">
                  <Icon aria-hidden className={clsx("size-3.5 shrink-0", tone)} />
                  <span className={clsx("text-2xs font-semibold tracking-wide", tone)}>{label}</span>
                  <span className="min-w-0 truncate font-mono text-2xs text-ink-3">{f.check_slug}</span>
                  {f.relaxed ? <span className="ml-auto text-2xs text-warn">relaxed</span> : null}
                </div>
                <p className="mt-1.5 text-sm text-ink">{f.message}</p>
                {f.anchor.quote.trim() ? (
                  <p className="mt-2 line-clamp-2 border-l-2 border-line-strong pl-2 font-mono text-xs text-ink-2 group-hover:border-accent">
                    {f.anchor.quote}
                  </p>
                ) : null}
                {f.anchor.heading_path.length ? (
                  <p className="mt-1 truncate text-2xs text-ink-3">{f.anchor.heading_path.join(" › ")}</p>
                ) : null}
                {f.fix ? <p className="mt-1 text-xs text-ink-2">Fix: {f.fix}</p> : null}
              </button>
              {(() => {
                const w = pending(f);
                if (!w) return null;
                return (
                  <div className="mx-4 mb-3 rounded-md border border-warn/40 bg-warn-soft px-3 py-2 text-xs">
                    <p className="font-semibold text-ink">Waiver requested by {w.requested_by}</p>
                    <p className="mt-0.5 text-ink-2">{w.reason}</p>
                    <p className="mt-0.5 text-ink-3">
                      {w.approvals.length} of {w.needed} approvals ({w.policy.replace("_", " ")})
                      {w.section.length === 0 ? " · whole doc" : ` · ${w.section.join(" › ")}`}
                    </p>
                    {w.can_approve ? (
                      <div className="mt-2 flex gap-1.5">
                        <Button
                          size="sm"
                          variant="primary"
                          onClick={() => decide.approve.mutate({ path: { waiverId: w.id } })}
                        >
                          Approve
                        </Button>
                        <Button size="sm" onClick={() => decide.reject.mutate({ path: { waiverId: w.id } })}>
                          Reject
                        </Button>
                      </div>
                    ) : (
                      <p className="mt-1 text-ink-3">Someone with the {w.policy.replace("_", " ")} role decides it.</p>
                    )}
                    {decide.error ? <p className="mt-1 text-bad">{problemMessage(decide.error)}</p> : null}
                  </div>
                );
              })()}
              {f.waived || member ? (
                <div className="flex items-center gap-3 px-4 pb-3.5 text-xs">
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
              {canEdit && !f.waived ? <SuggestFix runId={runId} bundleId={bundleId} finding={f} /> : null}
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

// useDecide approves or rejects a waiver from the finding it excuses (SDD §9.1).
function useDecide(bundleId: string) {
  const qc = useQueryClient();
  const done = () => {
    qc.invalidateQueries({ queryKey: listWaiversQueryKey({ path: { bundleId } }) });
    qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
  };
  const approve = useMutation({ ...approveWaiverMutation(), onSuccess: done });
  const reject = useMutation({ ...rejectWaiverMutation(), onSuccess: done });
  return { approve, reject, error: approve.error ?? reject.error };
}

// rank puts a finding with a waiver request first.
function rank(waiver: unknown): number {
  return waiver ? 0 : 1;
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
  // The requests sit on their findings above; this section is the record of what was decided.
  const items = (waivers.data?.items ?? []).filter((w) => w.status !== "requested");
  if (items.length === 0) return null;
  const error = approve.error ?? reject.error;
  return (
    <section className="border-t border-line">
      <h3 className="px-4 pt-4 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Waivers</h3>
      <ul className="divide-y divide-line">
        {items.map((w) => (
          <li key={w.id} className="px-4 py-3.5 text-sm">
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

// SuggestFix asks the AI for a patch for one finding and shows it. The doc changes only when
// the author accepts the patch (REQ-025, T-092).
function SuggestFix({ runId, bundleId, finding }: { runId: string; bundleId: string; finding: Finding }) {
  const qc = useQueryClient();
  const [patch, setPatch] = useState<FixSuggestion>();
  const [accepted, setAccepted] = useState<number>();
  const path = { runId, findingId: finding.id };
  const suggest = useMutation({ ...suggestFixMutation(), onSuccess: (p) => setPatch(p) });
  const accept = useMutation({
    ...acceptFixMutation(),
    onSuccess: (r) => {
      setPatch(undefined);
      setAccepted(r.version.number);
      qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
    },
  });
  if (accepted)
    return (
      <p className="px-4 pb-3 text-xs text-ok">Fix applied as version {accepted}. Run the review again to check it.</p>
    );
  if (!patch)
    return (
      <div className="px-4 pb-3">
        <button
          type="button"
          onClick={() => suggest.mutate({ path })}
          disabled={suggest.isPending}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink disabled:text-ink-3"
        >
          <Wand2 aria-hidden className="size-3.5" />
          {suggest.isPending ? "Writing a fix" : "Suggest fix"}
        </button>
        {suggest.isError ? (
          <div className="mt-1.5">
            <ErrorState message={problemMessage(suggest.error)} />
          </div>
        ) : null}
      </div>
    );
  return (
    <div className="mx-4 mb-3 rounded-md border border-line bg-sunken p-2 text-xs">
      <p className="text-ink-2">{patch.explanation}</p>
      <p className="mt-1.5 font-mono text-2xs text-ink-3">{patch.file}</p>
      <pre className="mt-1 max-h-40 overflow-auto rounded-sm bg-[var(--diff-del)] px-1.5 py-1 font-mono whitespace-pre-wrap line-through decoration-ink-3">
        {patch.old}
      </pre>
      <pre className="mt-1 max-h-40 overflow-auto rounded-sm bg-[var(--diff-add)] px-1.5 py-1 font-mono whitespace-pre-wrap">
        {patch.new}
      </pre>
      {accept.isError ? (
        <div className="mt-1.5">
          <ErrorState message={problemMessage(accept.error)} />
        </div>
      ) : null}
      <div className="mt-2 flex justify-end gap-1.5">
        <Button size="sm" onClick={() => setPatch(undefined)}>
          Reject
        </Button>
        <Button size="sm" variant="primary" onClick={() => accept.mutate({ path })} disabled={accept.isPending}>
          {accept.isPending ? "Applying" : "Accept"}
        </Button>
      </div>
    </div>
  );
}

// DetachedList lists findings and threads whose text changed so that Speccy cannot find it
// again (SDD §8.8). They are never shown at a wrong place.
function DetachedList({ bundleId, findings }: { bundleId: string; findings: Finding[] }) {
  const threads = useQuery(listBundleThreadsOptions({ path: { bundleId } }));
  const lost = findings.filter((f) => f.anchor.detached);
  const lostThreads = (threads.data?.items ?? []).filter(
    (t) => t.status === "open" && t.anchor_kind === "text" && t.anchor.detached,
  );
  if (lost.length + lostThreads.length === 0) return null;
  return (
    <section className="border-t border-line">
      <h3 className="flex items-center gap-1.5 px-4 pt-4 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        <Unlink aria-hidden className="size-3.5" /> Detached
      </h3>
      <p className="px-4 pt-1 text-xs text-ink-3">The text changed. Speccy cannot find these quotes in this version.</p>
      <ul className="divide-y divide-line">
        {lost.map((f) => (
          <li key={f.id} className="px-4 py-3.5">
            <p className="font-mono text-2xs text-ink-3">{f.check_slug}</p>
            <p className="mt-0.5 text-sm">{f.message}</p>
            <p className="mt-1 line-clamp-2 border-l-2 border-warn pl-2 font-mono text-xs text-ink-2">
              {f.anchor.quote}
            </p>
          </li>
        ))}
        {lostThreads.map((t) => (
          <li key={t.id} className="px-4 py-3.5">
            <p className="text-2xs text-ink-3">Thread</p>
            <p className="mt-0.5 text-sm">{t.title}</p>
            <p className="mt-1 line-clamp-2 border-l-2 border-warn pl-2 font-mono text-xs text-ink-2">
              {String(t.anchor.quote ?? "")}
            </p>
          </li>
        ))}
      </ul>
    </section>
  );
}
