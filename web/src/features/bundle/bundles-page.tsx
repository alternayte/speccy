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
  dismissDocMutation,
  listDismissedDocsOptions,
  listBundlesOptions,
  listProfilesOptions,
  listSkippedOptions,
  undismissDocMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemCode, problemMessage } from "@/lib/problem";
import { importBundle } from "@/lib/api";
import type { Bundle, ConfirmedLink, Profile } from "@/lib/api";
import { useLinkOffer } from "./adopt-link";
import { listBundlesQueryKey } from "@/lib/api/@tanstack/react-query.gen";
import { type DroppedBundle, bundlesFromDrop, filesFromDrop, isMarkdown, isZip } from "./drop";
import { GitHubDialog } from "./github-dialog";
import { SourceDocs } from "./source-docs";
import { ImportDialog } from "./import-dialog";
import { NewBundleDialog } from "./new-bundle-dialog";
import { relativeTime } from "./time";
import { BundleStatePill } from "./verdict";

export function BundlesPage() {
  const bundles = useQuery({ ...listBundlesOptions({ query: { limit: 100 } }), refetchInterval: 3000 });
  const [importing, setImporting] = useState(false);
  const [fromGitHub, setFromGitHub] = useState(false);
  const [creating, setCreating] = useState(false);
  const hosted = useMe().data?.mode === "hosted";
  const qc = useQueryClient();
  const [over, setOver] = useState(false);
  // dropped is a folder with several markdown files, or a file whose type Speccy cannot guess:
  // the import dialog asks for the type of each.
  const [dropped, setDropped] = useState<DroppedBundle | null>(null);

  // A drop makes the bundles of each folder, and of each loose markdown or .zip file. A folder
  // with one markdown file imports at once; with more, the dialog asks which are specs.
  const make = useMutation({
    mutationFn: async (dt: DataTransfer) => {
      const groups = bundlesFromDrop(await filesFromDrop(dt));
      if (groups.length === 0) throw new Error("Drop a folder, a markdown file, or a .zip file.");
      for (const group of groups) {
        if (isZip(group.files[0]!.path)) {
          await importBundle({ body: { name: group.name, file: group.files[0]!.file }, throwOnError: true });
          continue;
        }
        const markdown = group.files.filter((f) => isMarkdown(f.path));
        if (markdown.length === 0) throw new Error(`${group.name} has no markdown file, so it is not a bundle.`);
        if (markdown.length > 1) {
          setDropped(group);
          setImporting(true);
          return;
        }
        try {
          await importBundle({
            body: {
              name: group.name,
              files: group.files.map((d) => new File([d.file], d.path, { type: d.file.type })),
            },
            throwOnError: true,
          });
        } catch (err) {
          // No type in the file, and no profile fits its headings: ask for the type.
          if (problemCode(err) === "no_profile" || problemCode(err) === "no_main_doc") {
            setDropped(group);
            setImporting(true);
            return;
          }
          throw err;
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
              Each bundle is a folder with one or more spec docs and their assets.
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
                  Speccy found no folder with a spec doc. A spec doc is a markdown file with a <code>type</code> field
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
                    className="flex flex-col gap-1.5 px-4 py-3 transition-colors hover:bg-sunken sm:grid sm:grid-cols-[1fr_11rem_12rem_14rem_6rem] sm:items-center sm:gap-x-4 sm:gap-y-1"
                  >
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-ink">{b.title}</span>
                      <span className="block truncate font-mono text-xs text-ink-3">{b.slug}</span>
                    </span>
                    {/* The title leads the row. Below it, the state and the spec docs sit on
                        one line, so a narrow screen keeps the same order as a wide one. */}
                    <span className="flex items-center gap-2 sm:contents">
                      <span className="whitespace-nowrap">
                        {b.state !== "not_build_ready" && b.docs.some((d) => d.run_error) ? (
                          <span className="text-xs text-bad">Cannot review</span>
                        ) : (
                          <BundleStatePill state={b.state} />
                        )}
                      </span>
                      <span className="min-w-0 truncate">
                        {b.docs.length === 1 ? (
                          <span className="rounded-sm border border-line px-1.5 py-0.5 font-mono text-2xs tracking-wide text-ink-2 uppercase">
                            {b.docs[0]!.profile_key}
                          </span>
                        ) : (
                          <span className="text-xs text-ink-3">
                            {b.docs.length} spec docs: {b.docs.map((d) => d.profile_key.toUpperCase()).join(", ")}
                          </span>
                        )}
                      </span>
                    </span>
                    <span className="col-start-1 hidden truncate text-xs text-ink-2 sm:col-start-auto sm:block">
                      {rowAction(b)}
                    </span>
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
  const dismissed = useQuery(listDismissedDocsOptions());
  const [picked, setPicked] = useState<Record<string, string>>({});
  const [showDismissed, setShowDismissed] = useState(false);
  const dismiss = useMutation({ ...dismissDocMutation(), onSuccess: () => qc.invalidateQueries() });
  const undismiss = useMutation({ ...undismissDocMutation(), onSuccess: () => qc.invalidateQueries() });
  const marked = (dismissed.data?.items ?? []).filter((d) => !d.source_id);
  const adopt = useMutation({
    ...adoptSkippedMutation(),
    onSuccess: async (b) => {
      await qc.invalidateQueries();
      navigate({ to: "/bundles/$bundleId/docs/$docId", params: { bundleId: b.bundle_id, docId: b.id } });
    },
  });
  const items = skipped.data?.items ?? [];
  if (items.length === 0 && marked.length === 0) return null;
  return (
    <section className="mt-6" aria-labelledby="skipped">
      <h2 id="skipped" className="text-sm font-semibold text-ink">
        Markdown files that are not bundles yet
      </h2>
      {items.length > 0 ? (
        <p className="mt-1 text-sm text-ink-2">
          These files name no type. Adopt one to write the type into it and review it.
        </p>
      ) : null}
      {adopt.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(adopt.error)} />
        </div>
      ) : null}
      {items.length > 0 ? (
        <ul className="mt-2 overflow-hidden rounded-lg border border-line bg-surface">
          {items.map((it) => (
            <LocalSkippedRow
              key={it.path}
              path={it.path}
              profileKey={picked[it.path] ?? it.profile ?? profiles.data?.items[0]?.key ?? ""}
              profiles={profiles.data?.items ?? []}
              onPick={(k) => setPicked({ ...picked, [it.path]: k })}
              onDismiss={() => dismiss.mutate({ body: { path: it.path } })}
              dismissing={dismiss.isPending}
              onAdopt={(link) =>
                adopt.mutate({
                  body: {
                    path: it.path,
                    profile: picked[it.path] ?? it.profile ?? profiles.data?.items[0]?.key ?? "",
                    ...(link ? { link } : {}),
                  },
                })
              }
              adopting={adopt.isPending}
            />
          ))}
        </ul>
      ) : null}
      {marked.length > 0 ? (
        <div className="mt-2 text-xs text-ink-3">
          {marked.length} file{marked.length === 1 ? "" : "s"} marked not a spec.{" "}
          <button type="button" onClick={() => setShowDismissed((v) => !v)} className="text-accent">
            {showDismissed ? "hide" : "show"}
          </button>
          {showDismissed ? (
            <ul className="mt-1 space-y-1">
              {marked.map((d) => (
                <li key={d.path} className="flex flex-wrap items-center gap-2">
                  <span className="min-w-0 truncate font-mono text-xs text-ink-2">{d.path}</span>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={undismiss.isPending}
                    onClick={() => undismiss.mutate({ query: { path: d.path } })}
                  >
                    Undo
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

// LocalSkippedRow is one markdown file on disk that names no type, with the link Speccy offers
// when it is adopted.
function LocalSkippedRow({
  path,
  profileKey,
  profiles,
  onPick,
  onDismiss,
  dismissing,
  onAdopt,
  adopting,
}: {
  path: string;
  profileKey: string;
  profiles: Profile[];
  onPick: (key: string) => void;
  onDismiss: () => void;
  dismissing: boolean;
  onAdopt: (link?: ConfirmedLink) => void;
  adopting: boolean;
}) {
  const offer = useLinkOffer(path, profileKey, { local: true });
  return (
    <li className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2 last:border-b-0">
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-2">{path}</span>
      <select
        aria-label={`Doc type for ${path}`}
        value={profileKey}
        onChange={(e) => onPick(e.target.value)}
        className="h-7 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
      >
        {profiles.map((p) => (
          <option key={p.key} value={p.key}>
            {p.name}
          </option>
        ))}
      </select>
      <Button size="sm" variant="ghost" disabled={dismissing} onClick={onDismiss}>
        Not a spec
      </Button>
      <Button size="sm" disabled={!profileKey || adopting} onClick={() => onAdopt(offer.link)}>
        Adopt
      </Button>
      {offer.view}
    </li>
  );
}

// rowAction is the next action a bundle row shows: the one of the spec doc that makes the
// row's state, so a Not Build Ready row never says to hand the bundle over.
function rowAction(b: Bundle): string {
  const worst = b.docs.find((d) => d.next_action && d.verdict?.result === "not_build_ready");
  return (worst ?? b.docs.find((d) => d.next_action))?.next_action?.sentence ?? "";
}
