import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import type { ResolvedBuild, RunEvent, Verification } from "@/lib/api";
import {
  listVerificationsOptions,
  listVerificationsQueryKey,
  resolveVerificationTargetMutation,
  runVerificationMutation,
  verificationDefaultsOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "./time";

// VerificationsPanel starts a verification run and lists the runs of this bundle, newest
// first. A run is a statement about one commit: it keeps its outcomes, and a new bundle version
// makes it stale because the requirements moved. A run carries its own verdict and never the
// bundle's. prefill is a target another control asked for, such as the commit of a drifted link.
export function VerificationsPanel({
  docId,
  canVerify,
  prefill,
}: {
  docId: string;
  canVerify: boolean;
  prefill?: string;
}) {
  const runs = useQuery({ ...listVerificationsOptions({ path: { docId } }), refetchInterval: 15_000 });
  const items = runs.data?.items ?? [];
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
          {items.map((v) => {
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
      )}
    </section>
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
