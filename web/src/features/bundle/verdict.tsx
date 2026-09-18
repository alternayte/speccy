import { clsx } from "clsx";
import { CircleAlert, CircleCheck, CircleDashed, Info, OctagonX, TriangleAlert } from "lucide-react";
import type { BundleVerdict, VerdictResult } from "@/lib/api";

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

export function verdictLabel(v: BundleVerdict): string {
  const base = resultStyle[v.result].label;
  return v.waiver_count > 0 ? `${base} (${v.waiver_count} waiver${v.waiver_count === 1 ? "" : "s"})` : base;
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
  onShowFindings,
}: {
  verdict?: BundleVerdict;
  runError?: string;
  currentVersion: number;
  onShowFindings: () => void;
}) {
  if (runError) {
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
    verdict.result === "stale"
      ? `This verdict is for version ${verdict.version_number}. The current version is ${currentVersion}.`
      : verdict.must > 0
        ? `${verdict.must} MUST finding${verdict.must === 1 ? "" : "s"} to fix. SHOULD findings never block.`
        : verdict.result === "not_build_ready"
          ? "A required link or decision is missing."
          : verdict.should > 0
            ? `No blocking findings. ${verdict.should} SHOULD finding${verdict.should === 1 ? "" : "s"} can improve the doc.`
            : "No findings. The doc passes every lint check.";
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
        {verdict.kind === "lint" ? <span className="text-ink-3">Lint checks only</span> : null}
        {verdict.relaxed_count > 0 ? (
          <span className="text-warn">
            Adoption mode: {verdict.relaxed_count} check{verdict.relaxed_count === 1 ? "" : "s"} relaxed
          </span>
        ) : null}
        <button type="button" onClick={onShowFindings} className="font-medium text-ink underline underline-offset-2">
          Show findings
        </button>
      </div>
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
