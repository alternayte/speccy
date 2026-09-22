import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, FolderPlus, GitBranch, Plus } from "lucide-react";
import { useState } from "react";
import { useMe } from "@/features/account/me";
import { Button } from "@/components/ui/button";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { useNavigate } from "@tanstack/react-router";
import {
  adoptSkippedMutation,
  listBundlesOptions,
  listProfilesOptions,
  listSkippedOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemCode, problemMessage } from "@/lib/problem";
import { importBundle, putFileContent } from "@/lib/api";
import { listBundlesQueryKey } from "@/lib/api/@tanstack/react-query.gen";
import { bundlesFromDrop, filesFromDrop, isZip, mainDocOf } from "./drop";
import { GitHubDialog } from "./github-dialog";
import { SourceDocs } from "./source-docs";
import { ImportDialog } from "./import-dialog";
import { NewBundleDialog } from "./new-bundle-dialog";
import { relativeTime } from "./time";
import { VerdictPill } from "./verdict";

export function BundlesPage() {
  const bundles = useQuery({ ...listBundlesOptions({ query: { limit: 100 } }), refetchInterval: 3000 });
  const [importing, setImporting] = useState(false);
  const [fromGitHub, setFromGitHub] = useState(false);
  const [creating, setCreating] = useState(false);
  const hosted = useMe().data?.mode === "hosted";
  const qc = useQueryClient();
  const [over, setOver] = useState(false);
  // dropped is a file whose type Speccy cannot guess: the import dialog asks for one.
  const [dropped, setDropped] = useState<File | null>(null);

  // A drop makes one bundle per folder, and one per loose markdown or .zip file. The folder's
  // main doc starts the bundle, and its other files follow as assets.
  const make = useMutation({
    mutationFn: async (dt: DataTransfer) => {
      const groups = bundlesFromDrop(await filesFromDrop(dt));
      if (groups.length === 0) throw new Error("Drop a folder, a markdown file, or a .zip file.");
      for (const group of groups) {
        const main = isZip(group.files[0]!.path) ? group.files[0]! : mainDocOf(group.files);
        if (!main) throw new Error(`${group.name} has no markdown file, so it is not a bundle.`);
        let created;
        try {
          created = await importBundle({ body: { name: group.name, file: main.file }, throwOnError: true });
        } catch (err) {
          // No type in the file, and no profile fits its headings: ask for the type.
          if (problemCode(err) === "no_profile" && group.files.length === 1) {
            setDropped(main.file);
            setImporting(true);
            return;
          }
          throw err;
        }
        let base = created.data.current_version.id;
        for (const item of group.files) {
          if (item === main) continue;
          const res = await putFileContent({
            path: { bundleId: created.data.id },
            query: { path: item.path, base_version: base },
            body: item.file,
          });
          if (res.error) throw res.error;
          base = res.data!.version.id;
        }
      }
    },
    onSettled: () => qc.invalidateQueries({ queryKey: listBundlesQueryKey() }),
  });

  return (
    <div
      className="h-full overflow-y-auto"
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        make.mutate(e.dataTransfer);
      }}
    >
      <div className="mx-auto max-w-[960px] px-4 py-8 sm:px-6">
        <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3">
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Bundles</h1>
            <p className="mt-1 text-sm text-ink-2">
              {hosted
                ? "Each bundle is one spec: a main doc and its assets."
                : "Each bundle is a folder with one main doc and its assets."}
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button icon={<GitBranch className="size-4" />} onClick={() => setFromGitHub(true)}>
              From GitHub
            </Button>
            <Button icon={<FolderPlus className="size-4" />} onClick={() => setImporting(true)}>
              Import
            </Button>
            <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreating(true)}>
              New
            </Button>
          </div>
        </div>

        {over ? (
          <p className="mt-4 rounded-lg border-2 border-dashed border-accent bg-accent-soft/40 px-4 py-6 text-center text-sm text-ink">
            Drop a folder to make a bundle of it, or a markdown or .zip file for a single-file bundle.
          </p>
        ) : null}
        {make.isPending ? <p className="mt-4 text-sm text-ink-2">Making the bundles</p> : null}
        {make.isError ? (
          <div className="mt-4">
            <ErrorState message={problemMessage(make.error)} />
          </div>
        ) : null}

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
              {hosted ? (
                <>No bundle is visible to you yet. Create a bundle from a template, or import a file.</>
              ) : (
                <>
                  Speccy found no folder with a main doc. A main doc is a markdown file with a <code>type</code> field
                  in its frontmatter. Create a bundle from a template, add one on disk, or import a file.
                </>
              )}
            </Empty>
          ) : (
            <ul className="divide-y divide-line">
              {bundles.data.items.map((b) => (
                <li key={b.id}>
                  <Link
                    to="/bundles/$bundleId"
                    params={{ bundleId: b.id }}
                    className="flex flex-col gap-1.5 px-4 py-3 transition-colors hover:bg-sunken sm:grid sm:grid-cols-[1fr_11rem_4rem_14rem_3rem_6rem] sm:items-center sm:gap-x-4 sm:gap-y-1"
                  >
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-ink">{b.title}</span>
                      <span className="block truncate font-mono text-xs text-ink-3">{b.slug}</span>
                    </span>
                    {/* The title leads the row. Below it, the state and the type sit on one
                        line, so a narrow screen keeps the same order as a wide one. */}
                    <span className="flex items-center gap-2 sm:contents">
                      <span className="whitespace-nowrap">
                        {b.run_error ? (
                          <span className="text-xs text-bad">Cannot review</span>
                        ) : (
                          <VerdictPill verdict={b.verdict} />
                        )}
                      </span>
                      <span className="whitespace-nowrap">
                        <span className="rounded-sm border border-line px-1.5 py-0.5 font-mono text-2xs tracking-wide text-ink-2 uppercase">
                          {b.profile_key}
                        </span>
                      </span>
                    </span>
                    <span className="col-start-1 hidden truncate text-xs text-ink-2 sm:col-start-auto sm:block">
                      {b.next_action?.sentence ?? ""}
                    </span>
                    <span className="hidden text-xs text-ink-2 sm:block">v{b.current_version.number}</span>
                    <span className="hidden text-xs text-ink-3 sm:block">{relativeTime(b.updated_at)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>

        <SkippedDocs />
        <SourceDocs />

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
      <GitHubDialog open={fromGitHub} onOpenChange={setFromGitHub} />
      <ImportDialog
        open={importing}
        dropped={dropped}
        onOpenChange={(o) => {
          setImporting(o);
          if (!o) setDropped(null);
        }}
      />
      <NewBundleDialog open={creating} onOpenChange={setCreating} />
    </div>
  );
}

// SkippedDocs lists the markdown files the local scan passed over, and adopts one with the
// type Speccy guesses. The same line goes into the file as speccy init writes.
function SkippedDocs() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const skipped = useQuery(listSkippedOptions());
  const profiles = useQuery(listProfilesOptions());
  const [picked, setPicked] = useState<Record<string, string>>({});
  const adopt = useMutation({
    ...adoptSkippedMutation(),
    onSuccess: async (b) => {
      await qc.invalidateQueries();
      navigate({ to: "/bundles/$bundleId", params: { bundleId: b.id } });
    },
  });
  const items = skipped.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <section className="mt-6" aria-labelledby="skipped">
      <h2 id="skipped" className="text-sm font-semibold text-ink">
        Markdown files that are not bundles yet
      </h2>
      <p className="mt-1 text-sm text-ink-2">
        These files name no type. Adopt one to write the type into it and review it.
      </p>
      {adopt.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(adopt.error)} />
        </div>
      ) : null}
      <ul className="mt-2 overflow-hidden rounded-lg border border-line bg-surface">
        {items.map((it) => {
          const key = picked[it.path] ?? it.profile ?? profiles.data?.items[0]?.key ?? "";
          return (
            <li
              key={it.path}
              className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2 last:border-b-0"
            >
              <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-2">{it.path}</span>
              <select
                aria-label={`Doc type for ${it.path}`}
                value={key}
                onChange={(e) => setPicked({ ...picked, [it.path]: e.target.value })}
                className="h-7 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
              >
                {(profiles.data?.items ?? []).map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.name}
                  </option>
                ))}
              </select>
              <Button
                size="sm"
                disabled={!key || adopt.isPending}
                onClick={() => adopt.mutate({ body: { path: it.path, profile: key } })}
              >
                Adopt
              </Button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
