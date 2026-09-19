import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, Info, TriangleAlert } from "lucide-react";
import { ErrorState, Loading } from "@/components/ui/states";
import { VerdictPill } from "@/features/bundle/verdict";
import type { CategoryCount, StageTiming } from "@/lib/api";
import { getRunOptions, getRunReportOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

const categoryLabel: Record<string, string> = {
  structure: "Structure",
  clarity: "Clarity",
  completeness: "Completeness",
  evidence: "Evidence",
  precision: "Precision",
  coherence: "Coherence",
};

// ReportPage is the run report (SDD §13.1): stages and timings, cache hits, reader diversity,
// cost, findings by category, and the radar.
export function ReportPage({ bundleId, runId }: { bundleId: string; runId: string }) {
  const run = useQuery(getRunOptions({ path: { runId } }));
  const report = useQuery(getRunReportOptions({ path: { runId } }));
  if (run.isPending || report.isPending) return <Loading label="Loading the run report" />;
  if (run.isError || report.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(run.error ?? report.error)} />
      </div>
    );
  const r = run.data;
  const rep = report.data;
  const total = r.finished_at ? new Date(r.finished_at).getTime() - new Date(r.started_at).getTime() : undefined;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[960px] px-4 py-6 sm:px-6">
        <Link
          to="/bundles/$bundleId"
          params={{ bundleId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> Back to the bundle
        </Link>
        <div className="mt-2 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Run report</h1>
            <p className="mt-0.5 text-sm text-ink-2">
              {r.kind === "full" ? "Full review" : "Lint"} of v{r.version_number} ·{" "}
              <span className="uppercase">{r.profile_key}</span> profile v{r.profile_version} ·{" "}
              {new Date(r.started_at).toLocaleString()}
            </p>
          </div>
          {r.verdict ? <VerdictPill verdict={r.verdict} /> : <span className="text-sm text-ink-3">{r.status}</span>}
        </div>
        {r.error ? (
          <div className="mt-4">
            <ErrorState message={r.error} />
          </div>
        ) : null}

        <dl className="mt-6 grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-line bg-line sm:grid-cols-4">
          <Stat label="Duration" value={total !== undefined ? duration(total) : "Running"} />
          <Stat label="Tokens" value={((r.tokens_in ?? 0) + (r.tokens_out ?? 0)).toLocaleString()} />
          <Stat
            label="Cost estimate"
            value={
              !r.cost_estimate
                ? "Not priced"
                : r.cost_estimate < 0.01
                  ? "Under $0.01"
                  : `$${r.cost_estimate.toFixed(2)}`
            }
          />
          <Stat label="Steps from the cache" value={String(r.cache_hits ?? 0)} />
        </dl>

        {rep.readers ? (
          <p className="mt-3 flex items-start gap-1.5 text-sm text-ink-2">
            {rep.readers.low ? (
              <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warn" />
            ) : (
              <Info aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-3" />
            )}
            <span>
              {rep.readers.readers} reader{rep.readers.readers === 1 ? "" : "s"}, {rep.readers.distinct_models} distinct
              model{rep.readers.distinct_models === 1 ? "" : "s"}.
            </span>
          </p>
        ) : null}
        {r.notes?.map((n) => (
          <p key={n} className="mt-1.5 flex items-start gap-1.5 text-sm text-ink-2">
            <Info aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-3" />
            {n}
          </p>
        ))}

        <section className="mt-8">
          <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Stages</h2>
          <Stages stages={rep.stages} />
        </section>

        <section className="mt-8 grid gap-8 md:grid-cols-[1fr_280px]">
          <div>
            <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
              Open findings by category
            </h2>
            <table className="mt-2 w-full text-sm">
              <thead>
                <tr className="border-b border-line text-left text-xs text-ink-3">
                  <th className="py-1.5 font-medium">Category</th>
                  <th className="py-1.5 text-right font-medium">Score</th>
                  <th className="py-1.5 text-right font-medium">MUST</th>
                  <th className="py-1.5 text-right font-medium">SHOULD</th>
                  <th className="py-1.5 text-right font-medium">INFO</th>
                </tr>
              </thead>
              <tbody className="font-mono">
                {rep.categories.map((c) => (
                  <tr key={c.category} className="border-b border-line">
                    <td className="py-1.5 font-sans">{categoryLabel[c.category] ?? c.category}</td>
                    <td className="py-1.5 text-right">{c.score ?? "–"}</td>
                    <td className={c.must ? "py-1.5 text-right text-bad" : "py-1.5 text-right text-ink-3"}>{c.must}</td>
                    <td className="py-1.5 text-right">{c.should}</td>
                    <td className="py-1.5 text-right text-ink-3">{c.info}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="mt-2 text-xs text-ink-3">
              Score is passed checks divided by applicable checks. It is for metrics only. It does not decide the
              verdict.
            </p>
          </div>
          <div>
            <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Radar</h2>
            <Radar categories={rep.categories} />
          </div>
        </section>
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-surface px-3 py-2.5">
      <dt className="text-xs text-ink-3">{label}</dt>
      <dd className="mt-0.5 font-mono text-md text-ink">{value}</dd>
    </div>
  );
}

function duration(ms: number) {
  if (ms < 1000) return `${ms} ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)} s`;
  return `${Math.floor(s / 60)} min ${Math.round(s % 60)} s`;
}

// Stages shows each stage as a bar on one time axis, so the slow stage is plain to see.
function Stages({ stages }: { stages: StageTiming[] }) {
  if (stages.length === 0) return <p className="mt-2 text-sm text-ink-3">This run has no stage timings.</p>;
  const t0 = new Date(stages[0]!.started_at).getTime();
  const end = (s: StageTiming) => (s.finished_at ? new Date(s.finished_at).getTime() : Date.now());
  const span = Math.max(...stages.map(end)) - t0 || 1;
  return (
    <ol className="mt-2 space-y-1.5">
      {stages.map((s) => {
        const a = new Date(s.started_at).getTime() - t0;
        const d = end(s) - new Date(s.started_at).getTime();
        return (
          <li key={s.stage} className="grid grid-cols-[96px_1fr_72px] items-center gap-3 text-sm">
            <span className="capitalize">{s.stage}</span>
            <span className="relative h-2 rounded-full bg-sunken" title={`${s.stage}: ${duration(d)}`}>
              <span
                className="absolute inset-y-0 rounded-full bg-accent"
                style={{ left: `${(a / span) * 100}%`, width: `max(4px, ${(d / span) * 100}%)` }}
              />
            </span>
            <span className="text-right font-mono text-xs text-ink-2">{duration(d)}</span>
          </li>
        );
      })}
    </ol>
  );
}

// Radar draws the six category scores (SDD §8.7) on one 0–100 scale. The table beside it has
// the same numbers.
function Radar({ categories }: { categories: CategoryCount[] }) {
  const size = 300;
  const c = size / 2;
  const rad = c - 64;
  const n = categories.length;
  const at = (i: number, v: number) => {
    const a = -Math.PI / 2 + (i * 2 * Math.PI) / n;
    return [c + Math.cos(a) * rad * (v / 100), c + Math.sin(a) * rad * (v / 100)] as const;
  };
  const scored = categories.map((cat, i) => ({ cat, i, v: cat.score })).filter((x) => x.v !== undefined);
  const poly = scored.map(({ i, v }) => at(i, v!).join(",")).join(" ");
  return (
    <svg
      viewBox={`0 0 ${size} ${size}`}
      className="mt-2 w-full max-w-[280px]"
      role="img"
      aria-label="Score by category"
    >
      {[25, 50, 75, 100].map((ring) => (
        <polygon
          key={ring}
          points={categories.map((_, i) => at(i, ring).join(",")).join(" ")}
          fill="none"
          stroke="var(--line)"
          strokeWidth={1}
        />
      ))}
      {categories.map((cat, i) => {
        const [x, y] = at(i, 100);
        const [lx, ly] = at(i, 122);
        return (
          <g key={cat.category}>
            <line x1={c} y1={c} x2={x} y2={y} stroke="var(--line)" strokeWidth={1} />
            <text x={lx} y={ly} textAnchor="middle" dominantBaseline="middle" fontSize={10} fill="var(--ink-2)">
              {categoryLabel[cat.category] ?? cat.category}
            </text>
          </g>
        );
      })}
      {scored.length > 2 ? (
        <polygon
          points={poly}
          fill="color-mix(in srgb, var(--accent) 16%, transparent)"
          stroke="var(--accent)"
          strokeWidth={2}
        />
      ) : null}
      {scored.map(({ cat, i, v }) => {
        const [x, y] = at(i, v!);
        return (
          <circle key={cat.category} cx={x} cy={y} r={4} fill="var(--accent)" stroke="var(--surface)" strokeWidth={2}>
            <title>{`${categoryLabel[cat.category] ?? cat.category}: ${v}`}</title>
          </circle>
        );
      })}
    </svg>
  );
}
