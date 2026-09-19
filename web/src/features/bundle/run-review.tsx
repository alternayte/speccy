import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import { Check, Loader2, Play } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ErrorState, Loading } from "@/components/ui/states";
import type { Run, RunEvent } from "@/lib/api";
import { estimateRunOptions, listRunsOptions, startRunMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemCode, problemMessage } from "@/lib/problem";

const stages = ["lint", "rubric", "grounding", "divergence", "coherence", "verdict"] as const;

// RunReviewButton starts a full review after it shows the estimated cost (REQ-104).
export function RunReviewButton({
  bundleId,
  active,
  onStarted,
}: {
  bundleId: string;
  active: boolean;
  onStarted: (run: Run) => void;
}) {
  const [open, setOpen] = useState(false);
  const estimate = useQuery({ ...estimateRunOptions({ path: { bundleId } }), enabled: open, staleTime: 0 });
  const start = useMutation({
    ...startRunMutation(),
    onSuccess: (run) => {
      setOpen(false);
      onStarted(run);
    },
  });
  const setupMissing = estimate.isError && problemCode(estimate.error) === "role_unassigned";
  return (
    <>
      <Button
        size="sm"
        variant="primary"
        disabled={active}
        icon={active ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
        onClick={() => setOpen(true)}
      >
        {active ? "Reviewing" : "Run review"}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          if (!o) start.reset();
          setOpen(o);
        }}
        title="Run a full review"
        description="Lint runs on every save. A full review adds the AI stages: the rubric checks, fact checks, the divergence test, and the check against linked docs."
      >
        {estimate.isPending ? (
          <Loading label="Estimating the cost" />
        ) : setupMissing ? (
          <ErrorState
            message={problemMessage(estimate.error)}
            action={
              <Link to="/admin" className="text-sm font-medium text-ink underline">
                Open Admin
              </Link>
            }
          />
        ) : estimate.isError ? (
          <ErrorState message={problemMessage(estimate.error)} />
        ) : (
          <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
            <dt className="text-ink-2">Model calls</dt>
            <dd className="text-right font-mono">{estimate.data.calls}</dd>
            <dt className="text-ink-2">Steps from the cache</dt>
            <dd className="text-right font-mono">{estimate.data.cached_steps}</dd>
            <dt className="text-ink-2">Tokens (estimate)</dt>
            <dd className="text-right font-mono">
              {(estimate.data.tokens_in + estimate.data.tokens_out).toLocaleString()}
            </dd>
            <dt className="text-ink-2">Cost (estimate)</dt>
            <dd className="text-right font-mono">
              {estimate.data.priced && estimate.data.cost_usd != null
                ? `$${estimate.data.cost_usd.toFixed(2)}`
                : "No prices set"}
            </dd>
          </dl>
        )}
        {start.isError ? (
          <div className="mt-3">
            <ErrorState message={problemMessage(start.error)} />
          </div>
        ) : null}
        <div className="mt-5 flex justify-end gap-2">
          <Button onClick={() => setOpen(false)}>Cancel</Button>
          <Button
            variant="primary"
            disabled={estimate.isPending || estimate.isError || start.isPending}
            onClick={() => start.mutate({ path: { bundleId } })}
          >
            {start.isPending ? "Starting" : "Run review"}
          </Button>
        </div>
      </Dialog>
    </>
  );
}

// useActiveRun finds a queued or running full review of the bundle and follows its events
// (REQ-026). It calls onEnd when the run ends.
export function useActiveRun(bundleId: string, onEnd: () => void) {
  const qc = useQueryClient();
  const runs = useQuery({ ...listRunsOptions({ path: { bundleId }, query: { limit: 5 } }), refetchInterval: 5000 });
  const latest = runs.data?.items.find((r) => r.kind === "full");
  const active = latest && (latest.status === "queued" || latest.status === "running") ? latest : undefined;
  const [events, setEvents] = useState<RunEvent[]>([]);
  const runId = active?.id;

  useEffect(() => {
    if (!runId) return;
    setEvents([]);
    const es = new EventSource(`/api/v1/runs/${runId}/events`);
    es.onmessage = (m) => {
      const ev = JSON.parse(m.data) as RunEvent;
      setEvents((prev) => [...prev, ev]);
      if (ev.type === "done" || ev.type === "failed") {
        es.close();
        qc.invalidateQueries({ queryKey: listRunsOptions({ path: { bundleId }, query: { limit: 5 } }).queryKey });
        onEnd();
      }
    };
    return () => es.close();
    // onEnd is stable enough: it only refreshes queries.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runId, bundleId, qc]);

  return { active, events, refetch: runs.refetch };
}

// RunProgress shows the stages of a running review, with a count for the current one.
export function RunProgress({ events }: { events: RunEvent[] }) {
  const last = events[events.length - 1];
  const stage = [...events].reverse().find((e) => e.type === "stage")?.stage ?? "queued";
  const progress = [...events].reverse().find((e) => e.type === "progress" && e.stage === stage);
  const idx = stages.indexOf(stage as (typeof stages)[number]);
  return (
    <div role="status" aria-live="polite" className="border-b border-line bg-surface px-4 py-3 sm:px-5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="inline-flex items-center gap-2 text-md font-semibold">
          <Loader2 aria-hidden className="size-4 animate-spin text-accent" />
          {stage === "queued" ? "Waiting to start" : "Reviewing"}
        </span>
        <ol className="flex items-center gap-1 text-xs">
          {stages.map((s, i) => (
            <li
              key={s}
              className={clsx(
                "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 transition-colors duration-300",
                i < idx && "border-ok/40 text-ok",
                i === idx && "border-accent bg-accent-soft text-ink",
                (i > idx || idx < 0) && "border-line text-ink-3",
              )}
            >
              {i < idx ? <Check aria-hidden className="size-3" /> : null}
              {s}
            </li>
          ))}
        </ol>
        {progress?.total ? (
          <span className="text-xs text-ink-2">
            {progress.message ? `${progress.message}: ` : ""}
            {progress.done} of {progress.total}
          </span>
        ) : null}
        {last?.cache_hits ? <span className="text-xs text-ink-3">{last.cache_hits} from the cache</span> : null}
      </div>
      {progress?.total ? (
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-sunken">
          <div
            className="h-full rounded-full bg-accent transition-[width] duration-500 ease-out"
            style={{ width: `${Math.round((100 * (progress.done ?? 0)) / progress.total)}%` }}
          />
        </div>
      ) : null}
    </div>
  );
}
