import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ChevronRight, Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import type { ResolvedBuild, RunEvent, Verification, VerificationOutcome, Waiver } from "@/lib/api";
import {
  approveWaiverMutation,
  getSpecDocOptions,
  listVerificationsOptions,
  listVerificationsQueryKey,
  listWaiversOptions,
  listWaiversQueryKey,
  rejectWaiverMutation,
  requestVerificationWaiverMutation,
  resolveVerificationTargetMutation,
  runVerificationMutation,
  verificationDefaultsOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "./time";

// RunFocus names the verification run a link asked to open, and the trace ID to show in it. A
// waiver that names no run opens the newest run of its repo. seq changes on every request.
export type RunFocus = { trace_id: string; repo: string; run_id?: string; seq: number };

// VerificationsPanel starts a verification run and lists the runs of this bundle, newest
// first. A run is a statement about one commit: it keeps its outcomes, and a new bundle version
// makes it stale because the requirements moved. A run carries its own verdict and never the
// bundle's. prefill is a target another control asked for, such as the commit of a drifted link.
// A done run opens to its outcomes, where a missing or breached one takes a verification waiver.
export function VerificationsPanel({
  docId,
  canVerify,
  prefill,
  focus,
}: {
  docId: string;
  canVerify: boolean;
  prefill?: string;
  focus?: RunFocus;
}) {
  const runs = useQuery({ ...listVerificationsOptions({ path: { docId } }), refetchInterval: 15_000 });
  const waivers = useQuery({ ...listWaiversOptions({ path: { docId } }), refetchInterval: 5000 });
  const items = runs.data?.items ?? [];
  const [open, setOpen] = useState<string>();
  // The run a link asked for opens once the list is here, and its trace ID's row comes into view.
  const focused = focus
    ? (items.find((v) => v.id === focus.run_id) ??
      items.find((v) => v.repo === focus.repo && v.outcomes.some((o) => o.trace_id === focus.trace_id)))
    : undefined;
  const [shown, setShown] = useState(0);
  if (focus && focused && shown !== focus.seq) {
    setShown(focus.seq);
    setOpen(focused.id);
  }
  if (runs.isPending) return <Loading label="Loading verification runs" />;
  if (runs.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(runs.error)} />
      </div>
    );
  const active = items.find((v) => v.status === "queued" || v.status === "running");
  return (
    <section aria-label="Verification runs" className="border-t border-line">
      <h3 className="px-4 pt-4 pb-2 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        Verification
      </h3>
      {active ? (
        <RunProgress docId={docId} run={active} />
      ) : canVerify ? (
        <VerifyForm key={prefill ?? ""} docId={docId} prefill={prefill} />
      ) : null}
      {items.length === 0 ? null : (
        <ul className="divide-y divide-line border-t border-line">
          {items.map((v) => (
            <RunRow
              key={v.id}
              docId={docId}
              run={v}
              open={open === v.id}
              onToggle={() => setOpen(open === v.id ? undefined : v.id)}
              waivers={(waivers.data?.items ?? []).filter((w) => w.verification?.repo === v.repo)}
              canWaive={canVerify}
              highlight={focused?.id === v.id ? focus : undefined}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

// RunRow is one verification run. A done run opens to the outcome of each trace ID.
function RunRow({
  docId,
  run: v,
  open,
  onToggle,
  waivers,
  canWaive,
  highlight,
}: {
  docId: string;
  run: Verification;
  open: boolean;
  onToggle: () => void;
  waivers: Waiver[];
  canWaive: boolean;
  highlight?: RunFocus;
}) {
  const state = runState(v);
  const c = v.counts;
  // A run has counts only when it is done; a failed run says why.
  const meta =
    v.status === "done"
      ? [
          `${c.implemented} implemented`,
          `${c.untested} untested`,
          `${c.unproven} unproven`,
          `${c.missing} missing`,
          `${c.breached} breached`,
        ].join(" · ")
      : v.status === "failed"
        ? (v.error ?? "")
        : "";
  const at = v.sha ? (v.branch ? `${v.branch} ${v.sha.slice(0, 7)}` : v.sha.slice(0, 7)) : "a folder";
  const done = v.status === "done";
  const head = (
    <>
      <div className="flex items-baseline gap-2">
        {done ? (
          <ChevronRight
            aria-hidden
            className={clsx("size-3.5 shrink-0 self-center text-ink-3 transition-transform", open && "rotate-90")}
          />
        ) : null}
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
    </>
  );
  return (
    <li>
      {done ? (
        <button
          type="button"
          aria-expanded={open}
          onClick={onToggle}
          className="block w-full px-4 py-3 text-left hover:bg-sunken"
        >
          {head}
        </button>
      ) : (
        <div className="px-4 py-3">{head}</div>
      )}
      {open && done ? (
        <Outcomes docId={docId} run={v} waivers={waivers} canWaive={canWaive} highlight={highlight} />
      ) : null}
    </li>
  );
}

const outcomeTone = {
  implemented: "text-ok",
  untested: "text-warn",
  unproven: "text-warn",
  missing: "text-bad",
  breached: "text-bad",
} as const;

// Outcomes lists the outcome of each trace ID in one run. A missing or breached outcome that
// no waiver covers gets Ask for a waiver. The newest waiver of the trace ID in this repo shows
// on the row, with its decision for a person who can approve it.
function Outcomes({
  docId,
  run,
  waivers,
  canWaive,
  highlight,
}: {
  docId: string;
  run: Verification;
  waivers: Waiver[];
  canWaive: boolean;
  highlight?: RunFocus;
}) {
  const [asking, setAsking] = useState<VerificationOutcome>();
  const list = useRef<HTMLUListElement>(null);
  useEffect(() => {
    if (!highlight) return;
    const el = list.current?.querySelector<HTMLElement>(`[data-trace="${CSS.escape(highlight.trace_id)}"]`);
    el?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [highlight]);
  if (run.outcomes.length === 0) return <p className="px-4 pb-3 text-xs text-ink-3">The run verified no trace ID.</p>;
  return (
    <>
      <ul ref={list} aria-label={`Outcomes in ${run.repo}`} className="space-y-2 px-4 pb-3">
        {run.outcomes.map((o) => {
          const w = waivers
            .filter((x) => x.verification?.trace_id === o.trace_id)
            .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
          const wrong = o.outcome === "missing" || o.outcome === "breached";
          const ask = wrong && !o.waived && canWaive && (!w || w.status === "rejected" || w.status === "invalidated");
          return (
            <li
              key={o.trace_id}
              data-trace={o.trace_id}
              className={clsx(
                "rounded-md border px-3 py-2 text-xs",
                highlight?.trace_id === o.trace_id ? "border-accent" : "border-line",
              )}
            >
              <p className="flex items-baseline gap-2">
                <span className="font-mono font-medium text-ink">{o.trace_id}</span>
                <span className={clsx("font-semibold", outcomeTone[o.outcome])}>{o.outcome}</span>
                <span className="text-ink-3">{o.level}</span>
                {o.waived ? <span className="ml-auto font-semibold text-ok">waived</span> : null}
              </p>
              {o.reason || o.note ? <p className="mt-1 text-ink-2">{o.reason ?? o.note}</p> : null}
              {wrong && w ? <WaiverLine docId={docId} waiver={w} /> : null}
              {ask ? (
                <button
                  type="button"
                  onClick={() => setAsking(o)}
                  className="mt-1.5 font-medium text-accent hover:underline"
                >
                  Ask for a waiver
                </button>
              ) : null}
            </li>
          );
        })}
      </ul>
      <VerificationWaiverDialog docId={docId} run={run} outcome={asking} onClose={() => setAsking(undefined)} />
    </>
  );
}

// WaiverLine is the verification waiver of one trace ID on its outcome row: who asked and
// why, where it stands, and Approve or Reject for a person the policy allows (SDD §9.1).
function WaiverLine({ docId, waiver: w }: { docId: string; waiver: Waiver }) {
  const qc = useQueryClient();
  const done = () => {
    qc.invalidateQueries({ queryKey: listWaiversQueryKey({ path: { docId } }) });
    qc.invalidateQueries({ queryKey: getSpecDocOptions({ path: { docId } }).queryKey });
  };
  const approve = useMutation({ ...approveWaiverMutation(), onSuccess: done });
  const reject = useMutation({ ...rejectWaiverMutation(), onSuccess: done });
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  const policy = w.policy.replace("_", " ");
  const status =
    w.status === "requested"
      ? `Waiver requested by ${w.requested_by} · ${w.approvals.length} of ${w.needed} approvals (${policy})`
      : w.status === "approved"
        ? `Waiver approved · asked by ${w.requested_by}`
        : w.status === "rejected"
          ? `Waiver rejected · asked by ${w.requested_by}`
          : "Waiver ended: the section of the requirement changed";
  const tone =
    w.status === "approved"
      ? "border-ok/40 bg-ok-soft"
      : w.status === "requested"
        ? "border-warn/40 bg-warn-soft"
        : "border-line bg-sunken";
  return (
    <div className={clsx("mt-2 rounded-md border px-2.5 py-1.5", tone)}>
      <p className="font-semibold text-ink">{status}</p>
      <p className="mt-0.5 text-ink-2">{w.reason}</p>
      {w.status === "rejected" && w.decision_reason ? (
        <p className="mt-0.5 text-ink">
          <span className="font-semibold">Decision reason:</span> {w.decision_reason}
        </p>
      ) : null}
      {w.status === "approved" ? (
        <p className="mt-0.5 text-ink-3">The next run in this repo counts {w.verification?.trace_id} as waived.</p>
      ) : null}
      {w.status === "requested" && !w.can_approve ? (
        <p className="mt-0.5 text-ink-3">Someone with the {policy} role decides it.</p>
      ) : null}
      {w.status === "requested" && w.can_approve ? (
        rejecting ? (
          <form
            className="mt-1.5 space-y-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              reject.mutate({ path: { waiverId: w.id }, body: { reason } });
            }}
          >
            <Textarea
              aria-label="Decision reason"
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
                disabled={reject.isPending || reason.trim().length < 20}
              >
                Reject
              </Button>
              <Button size="sm" type="button" onClick={() => setRejecting(false)}>
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <div className="mt-1.5 flex gap-1.5">
            <Button
              size="sm"
              variant="primary"
              disabled={approve.isPending}
              onClick={() => approve.mutate({ path: { waiverId: w.id } })}
            >
              Approve
            </Button>
            <Button size="sm" onClick={() => setRejecting(true)}>
              Reject
            </Button>
          </div>
        )
      ) : null}
      {approve.isError || reject.isError ? (
        <p className="mt-1 text-bad">{problemMessage(approve.error ?? reject.error)}</p>
      ) : null}
    </div>
  );
}

// VerificationWaiverDialog asks to excuse one trace ID in the run's repo. The request names
// the run, so the inbox link to it opens this run.
function VerificationWaiverDialog({
  docId,
  run,
  outcome,
  onClose,
}: {
  docId: string;
  run: Verification;
  outcome?: VerificationOutcome;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const request = useMutation({
    ...requestVerificationWaiverMutation(),
    onSuccess: () => {
      setReason("");
      qc.invalidateQueries({ queryKey: listWaiversQueryKey({ path: { docId } }) });
      qc.invalidateQueries({ queryKey: getSpecDocOptions({ path: { docId } }).queryKey });
      onClose();
    },
  });
  return (
    <Dialog
      open={!!outcome}
      onOpenChange={(o) => {
        if (!o) {
          request.reset();
          onClose();
        }
      }}
      title="Ask for a waiver"
      description="A verification waiver excuses one trace ID in one code repo. The profile's waiver policy for a MUST says who approves it. It ends when the section of the requirement changes, and it never goes in the sidecar."
    >
      {outcome ? (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            request.mutate({
              path: { docId },
              body: { trace_id: outcome.trace_id, repo: run.repo, reason, run_id: run.id },
            });
          }}
        >
          <p className="text-sm">
            <span className="font-mono text-xs">{outcome.trace_id}</span>
            <span className="text-ink-2"> is {outcome.outcome} in </span>
            <span className="font-mono text-xs">{run.repo}</span>
          </p>
          <Textarea
            aria-label="Reason"
            rows={4}
            className="font-sans text-sm"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Why this repo does not need to meet the requirement. At least 20 characters."
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

function runState(v: Verification) {
  if (v.status === "queued" || v.status === "running") return { label: "Running", chip: "bg-accent-soft text-accent" };
  if (v.status === "failed") return { label: "Failed", chip: "bg-bad-soft text-bad" };
  if (v.stale) return { label: "Stale", chip: "bg-warn-soft text-warn" };
  return v.verdict === "verified"
    ? { label: "Verified", chip: "bg-ok-soft text-ok" }
    : { label: "Not Verified", chip: "bg-bad-soft text-bad" };
}

// VerifyForm takes one pasted target. It prefills from the doc's implemented-by link, else from
// the repo of the last run. Speccy says which repo and commit the target names before the run
// starts, because a run makes model calls for every trace ID.
function VerifyForm({ docId, prefill }: { docId: string; prefill?: string }) {
  const qc = useQueryClient();
  const defaults = useQuery(verificationDefaultsOptions({ path: { docId } }));
  const options = defaults.data?.items ?? [];
  const [typed, setTyped] = useState<string>();
  const target = typed ?? prefill ?? options[0]?.target ?? "";
  const [found, setFound] = useState<ResolvedBuild | null>(null);
  const resolve = useMutation({ ...resolveVerificationTargetMutation(), onSuccess: setFound });
  const run = useMutation({
    ...runVerificationMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: listVerificationsQueryKey({ path: { docId } }) }),
  });
  const change = (t: string) => {
    setTyped(t);
    setFound(null);
    resolve.reset();
    run.reset();
  };
  return (
    <form
      className="space-y-2 px-4 pb-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (!target.trim()) return;
        if (!found) resolve.mutate({ path: { docId }, body: { target } });
        else run.mutate({ path: { docId }, body: { target } });
      }}
    >
      <Label htmlFor="verify-target">Code to verify</Label>
      <Input
        id="verify-target"
        value={target}
        placeholder="https://github.com/acme/pay/pull/12"
        onChange={(e) => change(e.target.value)}
      />
      {options.length > 1 ? (
        <div className="flex flex-wrap gap-1.5" aria-label="Linked repos">
          {options.map((o) => (
            <button
              key={o.target}
              type="button"
              onClick={() => change(o.target)}
              className={clsx(
                "rounded-full border px-2 py-0.5 font-mono text-2xs",
                o.target === target ? "border-accent text-accent" : "border-line text-ink-2 hover:text-ink",
              )}
            >
              {o.target.replace(/^https:\/\/[^/]+\//, "")}
            </button>
          ))}
        </div>
      ) : null}
      <p className="text-xs text-ink-3">
        Paste the GitHub URL of a repo, a branch, a commit or a pull request. The run reads the whole repo at that
        commit.
      </p>
      {found ? (
        <p className="rounded-md border border-line bg-sunken px-3 py-2 font-mono text-xs text-ink">
          {describe(found)}
        </p>
      ) : null}
      {resolve.isError ? <ErrorState message={problemMessage(resolve.error)} /> : null}
      {run.isError ? <ErrorState message={problemMessage(run.error)} /> : null}
      <div className="flex gap-2">
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={!target.trim() || resolve.isPending || run.isPending}
        >
          {resolve.isPending ? "Checking" : found ? "Verify" : "Check"}
        </Button>
        {found ? (
          <Button size="sm" onClick={() => setFound(null)}>
            Change
          </Button>
        ) : null}
      </div>
    </form>
  );
}

function describe(r: ResolvedBuild): string {
  if (r.folder) return `The folder ${r.repo}`;
  const sha = r.sha.slice(0, 7);
  if (r.pull) return `${r.repo}, pull request #${r.pull}, at ${sha}`;
  if (r.branch) return `${r.repo}, ${r.branch}, at ${sha}`;
  return `${r.repo} at ${sha}`;
}

// RunProgress follows a queued or running verification, by trace ID.
function RunProgress({ docId, run }: { docId: string; run: Verification }) {
  const qc = useQueryClient();
  const [last, setLast] = useState<RunEvent>();
  useEffect(() => {
    const es = new EventSource(`/api/v1/verifications/${run.id}/events`);
    es.onmessage = (m) => {
      const ev = JSON.parse(m.data) as RunEvent;
      setLast(ev);
      if (ev.type === "done" || ev.type === "failed") {
        es.close();
        qc.invalidateQueries({ queryKey: listVerificationsQueryKey({ path: { docId } }) });
      }
    };
    return () => es.close();
  }, [run.id, docId, qc]);
  const at = run.sha ? run.sha.slice(0, 7) : "a folder";
  let text = "Waiting to start";
  if (last?.stage === "reading") text = `Reading ${run.repo}`;
  if (last?.stage === "finding") text = "Finding where each requirement lives";
  if (last?.stage === "judging")
    text = last.message ? `Judging ${last.message} (${(last.done ?? 0) + 1} of ${last.total})` : "Judging";
  return (
    <div role="status" aria-live="polite" className="px-4 pb-4">
      <p className="font-mono text-sm text-ink">
        {run.repo} @ {at}
      </p>
      <p className="mt-1 inline-flex items-center gap-2 text-xs text-ink-2">
        <Loader2 aria-hidden className="size-3.5 animate-spin text-accent" />
        {text}
      </p>
    </div>
  );
}
