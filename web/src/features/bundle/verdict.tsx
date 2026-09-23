import { useBundleId } from "./params";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import { CircleAlert, CircleCheck, CircleDashed, Info, OctagonX, TriangleAlert } from "lucide-react";
import type { BundleState, BundleVerdict, Run, SpecDoc, VerdictResult } from "@/lib/api";

const resultStyle: Record<VerdictResult, { label: string; tone: string; soft: string; icon: React.ReactNode }> = {
  build_ready: {
    label: "Build Ready",
    tone: "text-ok",
    soft: "bg-ok-soft border-ok/30",
    icon: <CircleCheck aria-hidden className="size-full" />,
  },
  not_build_ready: {
    label: "Not Build Ready",
    tone: "text-bad",
    soft: "bg-bad-soft border-bad/30",
    icon: <OctagonX aria-hidden className="size-full" />,
  },
  stale: {
    label: "Stale",
    tone: "text-warn",
    soft: "bg-warn-soft border-warn/30",
    icon: <CircleDashed aria-hidden className="size-full" />,
  },
};

export const verdictText: Record<VerdictResult, string> = {
  build_ready: "Build Ready",
  not_build_ready: "Not Build Ready",
  stale: "Stale",
};

export function verdictLabel(v: BundleVerdict): string {
  const base = resultStyle[v.result].label;
  return v.waiver_count > 0 ? `${base} (${v.waiver_count} waiver${v.waiver_count === 1 ? "" : "s"})` : base;
}

// BundleStatePill is the one chip of a bundle row: the worst state of its spec docs.
export function BundleStatePill({ state }: { state: BundleState }) {
  if (state === "not_reviewed") return <span className="text-xs text-ink-3">Not reviewed</span>;
  const s = resultStyle[state];
  return (
    <span className={clsx("inline-flex items-center gap-1.5 text-xs font-medium", s.tone)}>
      <span className="size-3.5">{s.icon}</span>
      <span>{s.label}</span>
    </span>
  );
}

// DocStateIcon is one spec doc's verdict in the file tree: an icon with the verdict in words
// for a screen reader. A doc with no verdict on its current version shows none.
export function DocStateIcon({ doc }: { doc: SpecDoc }) {
  const v = doc.verdict;
  if (!v || v.version_number !== doc.current_version.number) {
    return <span className="sr-only">Not reviewed</span>;
  }
  const s = resultStyle[v.result];
  return (
    <span role="img" aria-label={s.label} title={s.label} className={clsx("size-3 shrink-0", s.tone)}>
      {s.icon}
    </span>
  );
}

// VerdictPill is the compact verdict for lists.
export function VerdictPill({ verdict }: { verdict?: BundleVerdict }) {
  if (!verdict) return <span className="text-xs text-ink-3">Not reviewed</span>;
  const s = resultStyle[verdict.result];
  return (
    <span className={clsx("inline-flex items-center gap-1.5 text-xs font-medium", s.tone)}>
      <span className="size-3.5">{s.icon}</span>
      <span>{verdictLabel(verdict)}</span>
    </span>
  );
}

// VerdictBar is the most prominent element of the bundle screen (SDD §13.4): the verdict,
// why, and what to do next. The score is secondary.
export function VerdictBar({
  verdict,
  runError,
  currentVersion,
  report,
  onShowFindings,
  onShowWaivers,
  waiting,
  docId,
}: {
  docId: string;
  verdict?: BundleVerdict;
  runError?: string;
  currentVersion: number;
  // report is the run behind a full verdict: its tokens, cost, cache hits, and notes (REQ-022).
  report?: Run;
  onShowFindings: () => void;
  // onShowWaivers opens the first waiver that waits for this person (SDD §9.1).
  onShowWaivers: () => void;
  // waiting is how many waivers wait for this person's approval.
  waiting: number;
}) {
  const bundleId = useBundleId();
  if (runError && !verdict) {
    return (
      <div role="status" className="flex items-start gap-3 border-b border-bad/30 bg-bad-soft px-4 py-3 sm:px-5">
        <CircleAlert aria-hidden className="mt-0.5 size-5 shrink-0 text-bad" />
        <div className="min-w-0">
          <p className="text-md font-semibold text-ink">Speccy cannot review this doc</p>
          <p className="mt-0.5 text-sm text-ink-2">{runError}</p>
        </div>
      </div>
    );
  }
  if (!verdict) {
    return (
      <div role="status" className="border-b border-line bg-surface px-4 py-3 text-sm text-ink-3 sm:px-5">
        Checking the doc
      </div>
    );
  }
  const s = resultStyle[verdict.result];
  const next =
    verdict.kind === "full" && verdict.result === "build_ready" && verdict.must === 0 && verdict.should === 0
      ? "No findings. The doc passes every check."
      : verdict.result === "stale" && verdict.stale_reason === "upstream_changed"
        ? "A linked doc changed after this review. Run the review again."
        : verdict.result === "stale"
          ? `This verdict is for version ${verdict.version_number}. The current version is ${currentVersion}.`
          : verdict.must > 0
            ? `${verdict.must} MUST finding${verdict.must === 1 ? "" : "s"} to fix. SHOULD findings never block.`
            : verdict.result === "not_build_ready" && (verdict.blocking_threads ?? 0) > 0
              ? `${verdict.blocking_threads} blocking thread${verdict.blocking_threads === 1 ? "" : "s"} to answer.`
              : verdict.result === "not_build_ready"
                ? "A required link or decision is missing."
                : verdict.should > 0
                  ? `No blocking findings. ${verdict.should} SHOULD finding${verdict.should === 1 ? "" : "s"} can improve the doc.`
                  : "No findings. The doc passes every lint check.";
  const lintOnly = verdict.kind === "lint";
  return (
    <div
      role="status"
      aria-live="polite"
      className={clsx(
        "flex flex-wrap items-center gap-x-5 gap-y-2 border-b px-4 py-3 transition-colors duration-500 sm:px-5",
        s.soft,
      )}
    >
      <div className="flex min-w-0 items-center gap-3">
        <span className={clsx("size-7 shrink-0", s.tone)}>{s.icon}</span>
        <div className="min-w-0">
          <p className={clsx("text-lg leading-tight font-semibold tracking-tight", s.tone)}>{verdictLabel(verdict)}</p>
          <p className="mt-0.5 text-sm text-ink-2">{next}</p>
        </div>
      </div>
      <div className="ml-auto flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-ink-2">
        <LevelCount level="MUST" n={verdict.must} />
        <LevelCount level="SHOULD" n={verdict.should} />
        <LevelCount level="INFO" n={verdict.info} />
        <span title="Passed checks divided by applicable checks. For metrics only; not a gate.">
          Score <span className="font-mono text-ink">{verdict.score}</span>
        </span>
        {lintOnly ? <span className="text-ink-3">Lint checks only</span> : null}
        {verdict.relaxed_count > 0 ? (
          <span className="text-warn">
            Adoption mode: {verdict.relaxed_count} check{verdict.relaxed_count === 1 ? "" : "s"} relaxed
          </span>
        ) : null}
        {waiting > 0 ? (
          <button type="button" onClick={onShowWaivers} className="font-medium text-warn underline underline-offset-2">
            {waiting} waiver{waiting === 1 ? "" : "s"} wait{waiting === 1 ? "s" : ""} for approval
          </button>
        ) : null}
        <button type="button" onClick={onShowFindings} className="font-medium text-ink underline underline-offset-2">
          Show findings
        </button>
        <Link
          to="/bundles/$bundleId/docs/$docId/runs/$runId"
          params={{ bundleId, docId, runId: verdict.run_id }}
          className="font-medium text-ink underline underline-offset-2"
        >
          Run report
        </Link>
      </div>
      {runError || report?.notes?.length || report ? (
        <div className="w-full space-y-1 text-xs text-ink-2">
          {runError ? (
            <p className="flex items-start gap-1.5 text-bad">
              <CircleAlert aria-hidden className="mt-px size-3.5 shrink-0" />
              The last review failed, so this verdict is stale. {runError}
            </p>
          ) : null}
          {report?.notes?.map((n) => (
            <p key={n} className="flex items-start gap-1.5">
              <Info aria-hidden className="mt-px size-3.5 shrink-0 text-ink-3" />
              {n}
            </p>
          ))}
          {report && verdict.kind === "full" ? (
            <p className="text-ink-3">
              Full review of v{verdict.version_number}:{" "}
              {((report.tokens_in ?? 0) + (report.tokens_out ?? 0)).toLocaleString()} tokens
              {report.cost_estimate
                ? report.cost_estimate < 0.01
                  ? ", under $0.01"
                  : `, about $${report.cost_estimate.toFixed(2)}`
                : ""}
              {report.cache_hits ? `, ${report.cache_hits} steps from the cache` : ""}.
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export const levelStyle = {
  MUST: { icon: OctagonX, tone: "text-bad", label: "MUST" },
  SHOULD: { icon: TriangleAlert, tone: "text-warn", label: "SHOULD" },
  INFO: { icon: Info, tone: "text-ink-3", label: "INFO" },
} as const;

function LevelCount({ level, n }: { level: keyof typeof levelStyle; n: number }) {
  const { icon: Icon, tone, label } = levelStyle[level];
  return (
    <span className="inline-flex items-center gap-1">
      <Icon aria-hidden className={clsx("size-3.5", n > 0 ? tone : "text-ink-3")} />
      <span className="font-mono text-ink">{n}</span> {label}
    </span>
  );
}
