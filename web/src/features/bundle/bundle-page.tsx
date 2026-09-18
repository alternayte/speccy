import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { Download, FolderTree, History, Printer } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { ErrorState, Loading } from "@/components/ui/states";
import { EditorPane, type View } from "@/features/editor/editor-pane";
import { getBundleOptions, listFilesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { Explorer } from "./explorer";
import type { BundleSearch } from "./search";
import { VersionsPanel } from "./versions-panel";

type Panel = "files" | "versions" | null;

export function BundlePage({ bundleId, search }: { bundleId: string; search: BundleSearch }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  // Local mode: files on disk change without Speccy. Poll for new versions (REQ-005).
  const bundle = useQuery({ ...getBundleOptions({ path: { bundleId } }), refetchInterval: 2000 });
  const version = bundle.data?.current_version;
  const files = useQuery({
    ...listFilesOptions({ path: { bundleId }, query: { version: version?.id } }),
    enabled: !!version,
    // Keep the old list while a new version loads, so the editor stays mounted.
    placeholderData: keepPreviousData,
  });
  const [dirty, setDirty] = useState(false);
  const [panel, setPanel] = useState<Panel>(null);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const selected = search.file ?? bundle.data?.main_doc ?? "";
  const view: View = search.view ?? "split";

  const setSearch = useCallback(
    (next: BundleSearch) => navigate({ to: "/bundles/$bundleId", params: { bundleId }, search: next, replace: true }),
    [navigate, bundleId],
  );

  const select = (path: string) => {
    if (path === selected) return;
    if (dirty && !window.confirm("This file has unsaved edits. Leave it and discard them?")) return;
    setDirty(false);
    setPanel(null);
    setSearch({ ...search, file: path });
  };

  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
  }, [qc, bundleId]);

  if (bundle.isPending) return <Loading label="Loading the bundle" />;
  if (bundle.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(bundle.error)} />
      </div>
    );
  const b = bundle.data;
  const file = files.data?.items.find((f) => f.path === selected);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="no-print flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
        <div className="flex items-center gap-1 lg:hidden">
          <Button
            variant="ghost"
            size="sm"
            aria-label="Files"
            icon={<FolderTree className="size-4" />}
            onClick={() => setPanel(panel === "files" ? null : "files")}
          />
        </div>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-md font-semibold tracking-tight">{b.title}</h1>
          <p className="truncate font-mono text-2xs text-ink-3">
            {b.slug} · <span className="uppercase">{b.profile_key}</span> · v{b.current_version.number}
          </p>
        </div>
        <div className="flex items-center gap-1.5">
          <Button
            variant="ghost"
            size="sm"
            className="xl:hidden"
            aria-label="Versions"
            icon={<History className="size-4" />}
            onClick={() => setPanel(panel === "versions" ? null : "versions")}
          />
          <a
            href={`/api/v1/bundles/${bundleId}/export`}
            className="inline-flex h-7 items-center gap-1.5 rounded-md border border-line-strong bg-surface px-2 text-xs font-medium text-ink hover:bg-sunken"
          >
            <Download aria-hidden className="size-3.5" />
            <span className="hidden sm:inline">Export .zip</span>
          </a>
          <Button
            size="sm"
            icon={<Printer className="size-3.5" />}
            onClick={() => {
              if (view === "code") setSearch({ ...search, view: "preview" });
              setTimeout(() => window.print(), 300);
            }}
            title="Print, or save as PDF"
          >
            <span className="hidden sm:inline">PDF</span>
          </Button>
        </div>
      </div>

      <div className="relative flex min-h-0 flex-1">
        <aside
          className={clsx(
            "no-print w-[var(--rail)] shrink-0 border-r border-line bg-surface",
            panel === "files" ? "absolute inset-y-0 left-0 z-20 shadow-pop" : "hidden lg:block",
          )}
        >
          {files.isError ? (
            <div className="p-2">
              <ErrorState message={problemMessage(files.error)} />
            </div>
          ) : files.data ? (
            <Explorer
              bundleId={bundleId}
              baseVersion={b.current_version.id}
              files={files.data.items}
              selected={selected}
              onSelect={select}
              onChanged={(path) => {
                refresh();
                if (path) setSearch({ ...search, file: path });
              }}
            />
          ) : (
            <Loading label="Loading files" />
          )}
        </aside>

        <main className="min-w-0 flex-1">
          {files.data && !file ? (
            <div className="p-6">
              <ErrorState
                message={`Version ${b.current_version.number} of the bundle has no file ${selected}.`}
                action={
                  <Button size="sm" onClick={() => setSearch({ view: search.view })}>
                    Open the main doc
                  </Button>
                }
              />
            </div>
          ) : file ? (
            <EditorPane
              key={selected}
              bundleId={bundleId}
              path={selected}
              version={{ id: files.data!.version.id, number: files.data!.version.number }}
              sha={file.sha256}
              view={view}
              onViewChange={(v) => setSearch({ ...search, view: v })}
              onOpenPath={select}
              onSaved={refresh}
              onDirtyChange={setDirty}
            />
          ) : (
            <Loading />
          )}
        </main>

        <aside
          className={clsx(
            "no-print w-[var(--rail)] shrink-0 border-l border-line bg-surface",
            panel === "versions" ? "absolute inset-y-0 right-0 z-20 shadow-pop" : "hidden xl:block",
          )}
        >
          <VersionsPanel bundleId={bundleId} current={b.current_version.id} />
        </aside>
      </div>
    </div>
  );
}
