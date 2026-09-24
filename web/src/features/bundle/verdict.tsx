import { clsx } from "clsx";
import { CircleCheck, CircleDashed, Info, OctagonX, TriangleAlert } from "lucide-react";
import type { BundleState, BundleVerdict, SpecDoc, VerdictResult } from "@/lib/api";

const resultStyle: Record<VerdictResult, { label: string; tone: string; icon: React.ReactNode }> = {
  build_ready: {
    label: "Build Ready",
    tone: "text-ok",
    icon: <CircleCheck aria-hidden className="size-full" />,
  },
  not_build_ready: {
    label: "Not Build Ready",
    tone: "text-bad",
    icon: <OctagonX aria-hidden className="size-full" />,
  },
  stale: {
    label: "Stale",
    tone: "text-warn",
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

// upstreamNames names the linked spec docs that changed after the run read them, as one
// phrase: "A", "A and B", or "A, B and C".
export function upstreamNames(v: BundleVerdict): string {
  const names = (v.stale_upstream ?? []).map((u) => u.title || u.slug);
  if (names.length < 2) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
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

export const levelStyle = {
  MUST: { icon: OctagonX, tone: "text-bad", label: "MUST" },
  SHOULD: { icon: TriangleAlert, tone: "text-warn", label: "SHOULD" },
  INFO: { icon: Info, tone: "text-ink-3", label: "INFO" },
} as const;
