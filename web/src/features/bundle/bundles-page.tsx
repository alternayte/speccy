import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, FolderPlus, Plus } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { listBundlesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { ImportDialog } from "./import-dialog";
import { NewBundleDialog } from "./new-bundle-dialog";
import { relativeTime } from "./time";
import { VerdictPill } from "./verdict";

export function BundlesPage() {
  const bundles = useQuery({ ...listBundlesOptions({ query: { limit: 100 } }), refetchInterval: 3000 });
  const [importing, setImporting] = useState(false);
  const [creating, setCreating] = useState(false);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[960px] px-4 py-8 sm:px-6">
        <div className="flex items-end justify-between gap-4">
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Bundles</h1>
            <p className="mt-1 text-sm text-ink-2">Each bundle is a folder with one main doc and its assets.</p>
          </div>
          <div className="flex gap-2">
            <Button icon={<FolderPlus className="size-4" />} onClick={() => setImporting(true)}>
              Import
            </Button>
            <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreating(true)}>
              New
            </Button>
          </div>
        </div>

        <div className="mt-6 overflow-hidden rounded-lg border border-line bg-surface">
          {bundles.isPending ? (
            <Loading label="Loading bundles" />
          ) : bundles.isError ? (
            <div className="p-3">
              <ErrorState
                message={problemMessage(bundles.error)}
                action={
                  <Button size="sm" onClick={() => bundles.refetch()}>
                    Try again
                  </Button>
                }
              />
            </div>
          ) : bundles.data.items.length === 0 ? (
            <Empty title="No bundles yet">
              Speccy found no folder with a main doc. A main doc is a markdown file with a <code>type</code> field in
              its frontmatter. Create a bundle from a template, add one on disk, or import a file.
            </Empty>
          ) : (
            <ul className="divide-y divide-line">
              {bundles.data.items.map((b) => (
                <li key={b.id}>
                  <Link
                    to="/bundles/$bundleId"
                    params={{ bundleId: b.id }}
                    className="grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-1 px-4 py-3 transition-colors hover:bg-sunken sm:grid-cols-[1fr_11rem_4rem_3rem_3rem_6rem]"
                  >
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-ink">{b.title}</span>
                      <span className="block truncate font-mono text-xs text-ink-3">{b.slug}</span>
                    </span>
                    <span className="col-start-1 sm:col-start-auto">
                      {b.run_error ? (
                        <span className="text-xs text-bad">Cannot review</span>
                      ) : (
                        <VerdictPill verdict={b.verdict} />
                      )}
                    </span>
                    <span className="row-start-1 justify-self-end sm:row-start-auto sm:justify-self-start">
                      <span className="rounded-sm border border-line px-1.5 py-0.5 font-mono text-2xs tracking-wide text-ink-2 uppercase">
                        {b.profile_key}
                      </span>
                    </span>
                    <span className="hidden font-mono text-xs text-ink-2 sm:block" title="Score: for metrics only">
                      {b.verdict ? b.verdict.score : "–"}
                    </span>
                    <span className="hidden text-xs text-ink-2 sm:block">v{b.current_version.number}</span>
                    <span className="hidden text-xs text-ink-3 sm:block">{relativeTime(b.updated_at)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>

        {bundles.data && bundles.data.problems.length > 0 ? (
          <section className="mt-6" aria-labelledby="problems">
            <h2 id="problems" className="text-sm font-semibold text-ink">
              Folders that are not bundles
            </h2>
            <ul className="mt-2 space-y-2">
              {bundles.data.problems.map((p) => (
                <li
                  key={p.path + p.message}
                  className="flex gap-2 rounded-md border border-warn/30 bg-warn-soft px-3 py-2"
                >
                  <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-warn" />
                  <span className="min-w-0 text-sm">
                    <span className="font-mono text-xs text-ink-2">{p.path}</span>
                    <span className="block text-ink">{p.message}</span>
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}
      </div>
      <ImportDialog open={importing} onOpenChange={setImporting} />
      <NewBundleDialog open={creating} onOpenChange={setCreating} />
    </div>
  );
}
