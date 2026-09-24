import { useQuery } from "@tanstack/react-query";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { ProfileInsights } from "@/lib/api";
import { getInsightsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// InsightsPage shows the metrics of SDD §8.9 for each doc type (REQ-092).
export function InsightsPage() {
  const insights = useQuery(getInsightsOptions());
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[960px] space-y-8 px-4 py-8 sm:px-6">
        <header>
          <h1 className="text-xl font-semibold tracking-tight">Insights</h1>
          <p className="mt-1 text-sm text-ink-2">
            How fast specs become Build Ready, and which checks fail most, for each doc type. Medians over full reviews.
          </p>
        </header>
        {insights.isPending ? (
          <Loading label="Loading insights" />
        ) : insights.isError ? (
          <ErrorState message={problemMessage(insights.error)} />
        ) : insights.data.profiles.length === 0 ? (
          <Empty title="No doc types yet" />
        ) : (
          insights.data.profiles.map((p) => <ProfileCard key={p.key} p={p} />)
        )}
      </div>
    </div>
  );
}

function hours(h: number): string {
  if (!h) return "—";
  if (h < 1) return `${Math.round(h * 60)} min`;
  if (h < 48) return `${h.toFixed(1)} h`;
  return `${(h / 24).toFixed(1)} days`;
}

function ProfileCard({ p }: { p: ProfileInsights }) {
  // The third field says in words what a rate means, because a bare percentage does not.
  const stats: [string, string, string?][] = [
    ["Bundles", String(p.bundles)],
    ["Build Ready now", String(p.build_ready)],
    ["Standalone", String(p.standalone)],
    ["Reviews to Build Ready", p.runs_to_build_ready ? p.runs_to_build_ready.toFixed(1) : "—"],
    ["First review to Build Ready", hours(p.hours_to_build_ready)],
    ["In review to approved", hours(p.hours_to_approval)],
    // REQ-137: the only measure of the review against reality.
    [
      "False ready",
      p.blocked_sections.length || p.false_ready_rate ? `${Math.round(p.false_ready_rate * 100)}%` : "—",
      "Build Ready handoffs that came back blocked.",
    ],
    // The breach rate measures the review against the code: a profile whose requirements are
    // built wrong has a weak rubric.
    [
      "Breach rate",
      p.verified_trace_ids ? `${Math.round(p.breach_rate * 100)}%` : "—",
      p.verified_trace_ids
        ? `Of ${p.verified_trace_ids} verified trace IDs, the share that came back breached or missing.`
        : "Verified trace IDs that came back breached or missing. No verification run yet.",
    ],
  ];
  return (
    <section className="rounded-lg border border-line bg-surface">
      <h2 className="border-b border-line px-4 py-3 text-md font-semibold">
        {p.name} <span className="font-mono text-xs font-normal text-ink-3">{p.key}</span>
      </h2>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 px-4 py-4 sm:grid-cols-3">
        {stats.map(([k, v, what]) => (
          <div key={k}>
            <dt className="text-xs text-ink-2">{k}</dt>
            <dd className="mt-0.5 font-mono text-lg">{v}</dd>
            {what ? <dd className="mt-0.5 text-xs text-ink-3">{what}</dd> : null}
          </div>
        ))}
      </dl>
      <div className="grid gap-6 border-t border-line px-4 py-4 sm:grid-cols-2">
        <div>
          <h3 className="text-xs font-semibold text-ink-2">Top failing checks</h3>
          {p.top_failing.length === 0 ? (
            <p className="mt-1 text-sm text-ink-3">No failing checks.</p>
          ) : (
            <ol className="mt-2 space-y-1 text-sm">
              {p.top_failing.map((c) => (
                <li key={c.check_slug} className="flex justify-between gap-3">
                  <span className="truncate font-mono text-xs">{c.check_slug}</span>
                  <span className="font-mono text-xs text-ink-2">{c.count}</span>
                </li>
              ))}
            </ol>
          )}
        </div>
        <div>
          <h3 className="text-xs font-semibold text-ink-2">Sections that blocked a builder</h3>
          {p.blocked_sections.length === 0 ? (
            <p className="mt-1 text-sm text-ink-3">No builder was blocked.</p>
          ) : (
            <ol className="mt-2 space-y-1 text-sm">
              {p.blocked_sections.map((b) => (
                <li key={b.section} className="flex justify-between gap-3">
                  <span className="truncate text-xs">{b.section}</span>
                  <span className="font-mono text-xs text-ink-2">{b.count}</span>
                </li>
              ))}
            </ol>
          )}
        </div>
        <div>
          <h3 className="text-xs font-semibold text-ink-2">Waivers by check</h3>
          {p.waiver_rate.length === 0 ? (
            <p className="mt-1 text-sm text-ink-3">No waivers.</p>
          ) : (
            <ol className="mt-2 space-y-1 text-sm">
              {p.waiver_rate.map((w) => (
                <li key={w.check_slug} className="flex justify-between gap-3">
                  <span className="truncate font-mono text-xs">{w.check_slug}</span>
                  <span className="font-mono text-xs text-ink-2">
                    {w.waived} of {w.requested} approved
                  </span>
                </li>
              ))}
            </ol>
          )}
        </div>
      </div>
    </section>
  );
}
