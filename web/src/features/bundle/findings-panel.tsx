import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ShieldCheck, Unlink, Wand2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Finding, FixSuggestion, Waiver } from "@/lib/api";
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
  onOpenWaiver,
  onVerify,
}: {
  runId?: string;
  bundleId: string;
  member: boolean;
  canEdit: boolean;
  selected?: string;
  onOpen: (f: Finding) => void;
  onDiscuss: (f: Finding) => void;
  onOpenWaiver: (w: Waiver) => void;
  // onVerify opens the verify field at a target, for a drifted code link.
  onVerify?: (target: string) => void;
}) {
  const [waiving, setWaiving] = useState<{ finding: Finding; reason?: string }>();
  const frozen = useRef<{ runId?: string; ranks: Map<string, number> }>({ ranks: new Map() });
  const selectedRef = useRef<HTMLLIElement>(null);
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [selected]);
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const requests = useQuery({ ...listWaiversOptions({ path: { bundleId } }), refetchInterval: 5000 });
  const decide = useDecide(bundleId);
  // A GitHub bundle's sidecar reaches the repo through a pull request, so an approval here is
  // not final until that pull request merges (REQ-123, DEC-009).
  const bundle = useQuery(getBundleOptions({ path: { bundleId } }));
  const viaPullRequest = bundle.data?.source_kind === "github";
  // decided holds the waivers this person decided here, so the card stays on its finding
  // and shows what happened, instead of vanishing on the refetch.
  const [decided, setDecided] = useState<string[]>([]);
  const all = requests.data?.items ?? [];
  // onFinding is the waiver of a finding: same check, and the same section or the whole doc.
  const onFinding = (f: Finding) =>
    all.find((w) => waiverCovers(w, f) && (w.status === "requested" || decided.includes(w.id)));
  const pending = (f: Finding) => all.find((w) => w.status === "requested" && waiverCovers(w, f));
  const { refetch } = findings;
  useEffect(() => {
    if (runId) refetch();
  }, [runId, refetch]);

  if (!runId) return <Empty title="No review yet" />;
  const waivers = (
    <>
      <WaiversList bundleId={bundleId} findings={findings.data?.items ?? []} onAskAgain={setWaiving} />
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
  // the reason belongs beside the text it excuses (SDD §9.1). The order freezes on the first
  // sort of a run, so a decision does not move the list under the reader.
  const open = findings.data.items.filter((f) => !f.anchor.detached);
  if (frozen.current.runId !== runId && !requests.isPending) {
    const ranks = new Map(open.map((f) => [f.id, rank(pending(f)) * 10 + order[f.level]]));
    frozen.current = { runId, ranks };
  }
  const ranks = frozen.current.ranks;
  const items = open.sort(
    (a, b) =>
      (ranks.get(a.id) ?? rank(pending(a)) * 10 + order[a.level]) -
      (ranks.get(b.id) ?? rank(pending(b)) * 10 + order[b.level]),
  );
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
              {f.verify_target && onVerify ? (
                <div className="px-4 pb-2 text-xs">
                  <button
                    type="button"
                    onClick={() => onVerify(f.verify_target!)}
                    className="font-medium text-accent hover:underline"
                  >
                    Verify at {f.verify_target.split("/").pop()?.slice(0, 7)}
                  </button>
                </div>
              ) : null}
              {(() => {
                const w = onFinding(f);
                if (!w) return null;
                return (
                  <WaiverCard
                    waiver={w}
                    waivers={all}
                    viaPullRequest={viaPullRequest}
                    decide={decide}
                    onDecided={() => setDecided((d) => [...d, w.id])}
                    next={nextWaiting(all, items, decided, w)}
                    onNext={onOpenWaiver}
                  />
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
                        <button
                          type="button"
                          onClick={() => setWaiving({ finding: f })}
                          className="text-ink-2 hover:text-ink"
                        >
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
      <WaiverDialog bundleId={bundleId} ask={waiving} onClose={() => setWaiving(undefined)} />
    </>
  );
}

// WaiverDialog asks for a waiver of one finding, with a reason (REQ-072). An ended waiver
// opens it with the old reason, because the edit often does not change what it was for.
function WaiverDialog({
  bundleId,
  ask,
  onClose,
}: {
  bundleId: string;
  ask?: { finding: Finding; reason?: string };
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const finding = ask?.finding;
  const [reason, setReason] = useState("");
  useEffect(() => setReason(ask?.reason ?? ""), [ask]);
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

type Decide = ReturnType<typeof useDecide>;

// rank puts a finding with a waiver request first.
function rank(waiver: unknown): number {
  return waiver ? 0 : 1;
}

// waiverCovers reports whether w excuses f: the same check, and the same section or the whole doc.
export function waiverCovers(w: Waiver, f: Finding): boolean {
  return (
    w.check_slug === f.check_slug &&
    (w.section.length === 0 || w.section.join(" › ") === f.anchor.heading_path.join(" › "))
  );
}

// sectionName names the section a waiver covers, for a sentence.
function sectionName(w: Waiver): string {
  return w.section.length ? w.section.join(" › ") : "the doc";
}

// nextWaiting is the next waiver that waits for this person, after the one just decided. It
// counts only the waivers whose finding is in the rail, so the link always has somewhere to go.
function nextWaiting(all: Waiver[], items: Finding[], decided: string[], not: Waiver): Waiver[] {
  return all.filter(
    (w) =>
      w.id !== not.id &&
      w.status === "requested" &&
      w.can_approve &&
      !decided.includes(w.id) &&
      items.some((f) => waiverCovers(w, f)),
  );
}

// WaiverCard is the request on the finding it excuses: the reason, the policy count, the
// waivers already decided on the same section, and the decision (SDD §9.1).
function WaiverCard({
  waiver: w,
  waivers,
  viaPullRequest,
  decide,
  onDecided,
  next,
  onNext,
}: {
  waiver: Waiver;
  waivers: Waiver[];
  viaPullRequest: boolean;
  decide: Decide;
  onDecided: () => void;
  next: Waiver[];
  onNext: (w: Waiver) => void;
}) {
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  const history = waivers.filter(
    (h) => h.id !== w.id && h.status !== "requested" && h.check_slug !== "" && sectionName(h) === sectionName(w),
  );
  const tone =
    w.status === "approved"
      ? "border-ok/40 bg-ok-soft"
      : w.status === "requested"
        ? "border-warn/40 bg-warn-soft"
        : "border-line bg-sunken";
  return (
    <div className={clsx("mx-4 mb-3 rounded-md border px-3 py-2 text-xs", tone)}>
      <p className="font-semibold text-ink">Waiver requested by {w.requested_by}</p>
      <p className="mt-0.5 text-ink-2">{w.reason}</p>
      <p className="mt-0.5 text-ink-3">
        {/* The count belongs to an open request. A decided waiver keeps only its section. */}
        {w.status === "requested"
          ? `${w.approvals.length} of ${w.needed} approvals (${w.policy.replace("_", " ")}) · `
          : ""}
        {w.section.length === 0 ? "whole doc" : w.section.join(" › ")}
      </p>
      {history.length ? (
        <div className="mt-2 border-t border-line pt-1.5">
          <p className="text-2xs font-semibold tracking-wide text-ink-3 uppercase">Decided on this section</p>
          <ul className="mt-1 space-y-1">
            {history.map((h) => (
              <li key={h.id} className="text-ink-2">
                <span className={clsx("font-semibold uppercase", waiverStatus[h.status])}>
                  {h.status === "invalidated" ? "ended" : h.status}
                </span>{" "}
                <span className="font-mono text-2xs">{h.check_slug}</span> · {h.requested_by} · {h.reason}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {w.status === "approved" ? (
        <p className="mt-2 font-semibold text-ok">Approved. This check no longer fails here.</p>
      ) : null}
      {w.status === "rejected" ? (
        <p className="mt-2 text-ink">
          <span className="font-semibold">Rejected.</span> {w.decision_reason}
        </p>
      ) : null}
      {w.status === "requested" && !w.can_approve ? (
        <p className="mt-1 text-ink-3">Someone with the {w.policy.replace("_", " ")} role decides it.</p>
      ) : null}
      {w.status === "requested" && w.can_approve ? (
        rejecting ? (
          <form
            className="mt-2 space-y-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              decide.reject.mutate({ path: { waiverId: w.id }, body: { reason } }, { onSuccess: onDecided });
            }}
          >
            <Textarea
              aria-label="Reason for the rejection"
              rows={3}
              className="font-sans text-xs"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="What the author must change instead. At least 20 characters."
              required
              autoFocus
            />
            <div className="flex gap-1.5">
              <Button
                size="sm"
                type="submit"
                variant="primary"
                disabled={decide.reject.isPending || reason.trim().length < 20}
              >
                Reject
              </Button>
              <Button size="sm" type="button" onClick={() => setRejecting(false)}>
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <div className="mt-2">
            {viaPullRequest ? (
              <p className="mb-1.5 text-ink-3">
                The approval writes the sidecar into a pull request. The waiver stays pending until that pull request
                merges.
              </p>
            ) : null}
            <div className="flex gap-1.5">
              <Button
                size="sm"
                variant="primary"
                disabled={decide.approve.isPending}
                onClick={() => decide.approve.mutate({ path: { waiverId: w.id } }, { onSuccess: onDecided })}
              >
                Approve
              </Button>
              <Button size="sm" onClick={() => setRejecting(true)}>
                Reject
              </Button>
            </div>
          </div>
        )
      ) : null}
      {w.status !== "requested" && next[0] ? (
        <button type="button" onClick={() => onNext(next[0]!)} className="mt-2 font-medium text-accent hover:underline">
          Next waiver ({next.length} left)
        </button>
      ) : null}
      {decide.error ? <p className="mt-1 text-bad">{problemMessage(decide.error)}</p> : null}
    </div>
  );
}

const waiverStatus = {
  requested: "text-warn",
  approved: "text-ok",
  rejected: "text-ink-3",
  invalidated: "text-ink-3",
} as const;

// WaiversList is the record of the bundle's decided waivers. A request sits on its finding
// above, so this list never needs approve or reject (§9.1).
function WaiversList({
  bundleId,
  findings,
  onAskAgain,
}: {
  bundleId: string;
  findings: Finding[];
  onAskAgain: (ask: { finding: Finding; reason?: string }) => void;
}) {
  const waivers = useQuery(listWaiversOptions({ path: { bundleId } }));
  const items = (waivers.data?.items ?? []).filter((w) => w.status !== "requested");
  if (items.length === 0) return null;
  return (
    <section className="border-t border-line">
      <h3 className="px-4 pt-4 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Waivers</h3>
      <ul className="divide-y divide-line">
        {items.map((w) => {
          // An ended waiver can be asked for again when the current run still has its finding.
          const again = w.status === "invalidated" ? findings.find((f) => waiverCovers(w, f)) : undefined;
          return (
            <li key={w.id} className="px-4 py-3.5 text-sm">
              <p className="flex items-center gap-1.5 text-2xs">
                <span className={clsx("font-semibold tracking-wide uppercase", waiverStatus[w.status])}>
                  {w.status === "invalidated" ? "ended" : w.status}
                </span>
                <span className="font-mono text-ink-3">{w.check_slug}</span>
              </p>
              <p className="mt-1 text-ink">{w.reason}</p>
              <p className="mt-0.5 text-xs text-ink-3">
                {w.requested_by}
                {w.section.length ? ` · ${w.section.join(" › ")}` : " · whole doc"}
              </p>
              {w.status === "rejected" && w.decision_reason ? (
                <p className="mt-1 text-xs text-ink-2">
                  <span className="font-semibold">Rejected:</span> {w.decision_reason}
                </p>
              ) : null}
              {w.status === "invalidated" ? (
                <p className="mt-1 text-xs text-warn">
                  Ended: someone edited {sectionName(w)} after this was approved. Run the review again.
                </p>
              ) : null}
              {again ? (
                <button
                  type="button"
                  onClick={() => onAskAgain({ finding: again, reason: w.reason })}
                  className="mt-1.5 text-xs font-medium text-accent hover:underline"
                >
                  Ask for it again
                </button>
              ) : null}
            </li>
          );
        })}
      </ul>
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
