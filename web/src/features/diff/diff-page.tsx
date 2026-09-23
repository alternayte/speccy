import { useBundleId } from "@/features/bundle/params";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft, Check, Plus, Sparkle } from "lucide-react";
import { useState } from "react";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { Button } from "@/components/ui/button";
import { useMe } from "@/features/account/me";
import { verdictText } from "@/features/bundle/verdict";
import type { ChangeStatus, FindingBrief } from "@/lib/api";
import { diffVersionsOptions, listVersionsOptions, summarizeDiffMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { SideBySide } from "./side-by-side";

export type DiffSearch = { from: string; to: string };

export function validateDiffSearch(s: Record<string, unknown>): DiffSearch {
  return { from: String(s.from ?? ""), to: String(s.to ?? "") };
}

const statusStyle: Record<ChangeStatus, string> = {
  added: "text-ok border-ok/40",
  removed: "text-bad border-bad/40",
  modified: "text-warn border-warn/40",
  unchanged: "text-ink-3 border-line",
};

function Status({ s }: { s: ChangeStatus }) {
  return (
    <span
      className={clsx("rounded-sm border px-1.5 py-px text-2xs font-medium tracking-wide uppercase", statusStyle[s])}
    >
      {s}
    </span>
  );
}

// DiffPage compares two versions by section of the main doc and by file (REQ-006).
export function DiffPage({ docId, search }: { docId: string; search: DiffSearch }) {
  const bundleId = useBundleId();
  const navigate = useNavigate();
  const [by, setBy] = useState<"section" | "file">("section");
  const guest = !!useMe().data?.guest;
  const versions = useQuery(listVersionsOptions({ path: { docId }, query: { limit: 100 } }));
  const diff = useQuery({
    ...diffVersionsOptions({ path: { docId }, query: { from: search.from, to: search.to } }),
    enabled: !!search.from && !!search.to,
  });
  const setVersion = (key: "from" | "to", id: string) =>
    navigate({
      to: "/bundles/$bundleId/docs/$docId/diff",
      params: { bundleId, docId },
      search: { ...search, [key]: id },
    });

  const picker = (key: "from" | "to", label: string) => (
    <label className="flex items-center gap-1.5 text-xs text-ink-2">
      {label}
      <select
        value={search[key]}
        onChange={(e) => setVersion(key, e.target.value)}
        className="h-7 rounded-md border border-line-strong bg-surface px-1.5 text-xs text-ink"
      >
        {versions.data?.items.map((v) => (
          <option key={v.id} value={v.id}>
            v{v.number} · {v.message}
          </option>
        ))}
      </select>
    </label>
  );

  const sections = diff.data?.sections.filter((s) => s.status !== "unchanged") ?? [];
  const files = diff.data?.files.filter((f) => f.status !== "unchanged") ?? [];

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[1200px] px-3 py-6 sm:px-6">
        <Link
          to="/bundles/$bundleId/docs/$docId"
          params={{ bundleId, docId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> Back to the bundle
        </Link>
        <h1 className="mt-2 text-xl font-semibold tracking-tight">Changes</h1>
        <div className="mt-3 flex flex-wrap items-center gap-3">
          {picker("from", "From")}
          {picker("to", "To")}
          <div
            role="tablist"
            aria-label="Group by"
            className="inline-flex rounded-md border border-line p-0.5 sm:ml-auto"
          >
            {(["section", "file"] as const).map((k) => (
              <button
                key={k}
                role="tab"
                type="button"
                aria-selected={by === k}
                onClick={() => setBy(k)}
                className={clsx(
                  "rounded-sm px-3 py-1 text-xs font-medium",
                  by === k ? "bg-sunken text-ink" : "text-ink-2 hover:text-ink",
                )}
              >
                {k === "section" ? "By section" : "By file"}
              </button>
            ))}
          </div>
        </div>

        {search.from && search.to && search.from !== search.to && !guest ? (
          <AISummary key={search.from + search.to} docId={docId} from={search.from} to={search.to} />
        ) : null}

        <div className="mt-5 space-y-4">
          {!search.from || !search.to ? (
            <Empty title="Choose two versions">Pick a version in From and in To to see what changed.</Empty>
          ) : diff.isPending ? (
            <Loading label="Comparing" />
          ) : diff.isError ? (
            <ErrorState message={problemMessage(diff.error)} />
          ) : by === "section" ? (
            sections.length === 0 ? (
              <Empty title="No section changed">The spec doc has the same text in both versions.</Empty>
            ) : (
              sections.map((s, i) => (
                <section key={i} className="overflow-hidden rounded-lg border border-line bg-surface">
                  <header className="flex items-center gap-2 border-b border-line px-3 py-2">
                    <Status s={s.status} />
                    <span className="min-w-0 truncate text-sm font-medium">
                      {s.heading_path.length ? s.heading_path.join(" › ") : "Before the first heading"}
                    </span>
                  </header>
                  <SideBySide ops={s.lines} />
                </section>
              ))
            )
          ) : files.length === 0 ? (
            <Empty title="No file changed" />
          ) : (
            files.map((f) => (
              <section key={f.path} className="overflow-hidden rounded-lg border border-line bg-surface">
                <header className="flex items-center gap-2 border-b border-line px-3 py-2">
                  <Status s={f.status} />
                  <span className="min-w-0 truncate font-mono text-xs">{f.path}</span>
                </header>
                {f.binary ? (
                  <p className="px-3 py-2 text-xs text-ink-3">This file is not text.</p>
                ) : (
                  <SideBySide ops={f.lines} />
                )}
              </section>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

// AISummary offers the AI diff summary: what changed in meaning, and the change in findings
// (REQ-007). It calls a model, so it runs only on request.
function AISummary({ docId, from, to }: { docId: string; from: string; to: string }) {
  const sum = useMutation(summarizeDiffMutation());
  const d = sum.data;
  return (
    <section className="mt-5 rounded-lg border border-line bg-surface p-4">
      {!d ? (
        <div className="flex flex-wrap items-center gap-3">
          <p className="min-w-0 flex-1 text-sm text-ink-2">
            Summarize what changed in meaning between these versions, and which findings were fixed or added.
          </p>
          <Button
            size="sm"
            variant="primary"
            icon={<Sparkle className="size-3.5" />}
            onClick={() => sum.mutate({ path: { docId }, query: { from, to } })}
            disabled={sum.isPending}
          >
            {sum.isPending ? "Summarizing" : "Summarize the change"}
          </Button>
          {sum.isError ? (
            <div className="w-full">
              <ErrorState message={problemMessage(sum.error)} />
            </div>
          ) : null}
        </div>
      ) : (
        <div className="space-y-3">
          <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Summary</h2>
          <p className="text-md">{d.summary}</p>
          {d.changes.length ? (
            <ul className="list-disc space-y-1 pl-5 text-sm text-ink-2">
              {d.changes.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
          ) : null}
          <p className="border-t border-line pt-3 text-sm">
            {d.from_verdict && d.to_verdict ? (
              <>
                Verdict: {verdictText[d.from_verdict]} →{" "}
                <span className="font-medium">{verdictText[d.to_verdict]}</span>.{" "}
              </>
            ) : null}
            {d.from_verdict
              ? `Fixed ${d.fixed.length} finding${d.fixed.length === 1 ? "" : "s"}, added ${d.added.length}.`
              : "One of these versions has no review, so Speccy cannot compare findings."}
          </p>
          {d.fixed.length || d.added.length ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <Briefs title="Fixed" icon={<Check aria-hidden className="size-3.5 text-ok" />} items={d.fixed} />
              <Briefs title="Added" icon={<Plus aria-hidden className="size-3.5 text-bad" />} items={d.added} />
            </div>
          ) : null}
        </div>
      )}
    </section>
  );
}

function Briefs({ title, icon, items }: { title: string; icon: React.ReactNode; items: FindingBrief[] }) {
  return (
    <div>
      <h3 className="flex items-center gap-1 text-xs font-medium text-ink-2">
        {icon} {title} <span className="font-mono text-ink-3">{items.length}</span>
      </h3>
      <ul className="mt-1 space-y-1">
        {items.slice(0, 12).map((f, i) => (
          <li key={i} className="text-xs">
            <span className="font-mono text-ink-3">{f.level}</span> {f.message}
          </li>
        ))}
        {items.length > 12 ? <li className="text-xs text-ink-3">and {items.length - 12} more</li> : null}
      </ul>
    </div>
  );
}
