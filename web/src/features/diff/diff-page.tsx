import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft } from "lucide-react";
import { useState } from "react";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { ChangeStatus } from "@/lib/api";
import { diffVersionsOptions, listVersionsOptions } from "@/lib/api/@tanstack/react-query.gen";
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
export function DiffPage({ bundleId, search }: { bundleId: string; search: DiffSearch }) {
  const navigate = useNavigate();
  const [by, setBy] = useState<"section" | "file">("section");
  const versions = useQuery(listVersionsOptions({ path: { bundleId }, query: { limit: 100 } }));
  const diff = useQuery({
    ...diffVersionsOptions({ path: { bundleId }, query: { from: search.from, to: search.to } }),
    enabled: !!search.from && !!search.to,
  });
  const setVersion = (key: "from" | "to", id: string) =>
    navigate({ to: "/bundles/$bundleId/diff", params: { bundleId }, search: { ...search, [key]: id } });

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
          to="/bundles/$bundleId"
          params={{ bundleId }}
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

        <div className="mt-5 space-y-4">
          {!search.from || !search.to ? (
            <Empty title="Choose two versions">Pick a version in From and in To to see what changed.</Empty>
          ) : diff.isPending ? (
            <Loading label="Comparing" />
          ) : diff.isError ? (
            <ErrorState message={problemMessage(diff.error)} />
          ) : by === "section" ? (
            sections.length === 0 ? (
              <Empty title="No section changed">The main doc has the same text in both versions.</Empty>
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
