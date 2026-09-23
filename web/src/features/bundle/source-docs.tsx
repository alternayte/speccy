import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/states";
import type { ConfirmedLink, GithubSource, Profile, SourceSkippedDoc } from "@/lib/api";
import { useLinkOffer } from "./adopt-link";
import {
  adoptSkippedDocsMutation,
  dismissDocMutation,
  listDismissedDocsOptions,
  listGithubSourcesOptions,
  listProfilesOptions,
  listSkippedDocsOptions,
  publishSourceMappingMutation,
  undismissDocMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// SourceDocs lists, for each GitHub source, the markdown files the scan passed over. Accepting
// a type holds it in Speccy and makes the bundle: the repo takes no commit (REQ-133).
export function SourceDocs() {
  const sources = useQuery(listGithubSourcesOptions());
  const items = sources.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <>
      {items.map((s) => (
        <SourceSection key={s.id} source={s} />
      ))}
    </>
  );
}

function SourceSection({ source }: { source: GithubSource }) {
  const qc = useQueryClient();
  const skipped = useQuery(listSkippedDocsOptions({ path: { sourceId: source.id } }));
  const profiles = useQuery(listProfilesOptions());
  const [picked, setPicked] = useState<Record<string, string>>({});
  const adopt = useMutation({ ...adoptSkippedDocsMutation(), onSuccess: () => qc.invalidateQueries() });
  const mapping = useMutation({ ...publishSourceMappingMutation(), onSuccess: () => qc.invalidateQueries() });
  const dismissed = useQuery(listDismissedDocsOptions());
  const [showDismissed, setShowDismissed] = useState(false);
  const dismiss = useMutation({ ...dismissDocMutation(), onSuccess: () => qc.invalidateQueries() });
  const undismiss = useMutation({ ...undismissDocMutation(), onSuccess: () => qc.invalidateQueries() });
  const marked = (dismissed.data?.items ?? []).filter((d) => d.source_id === source.id);
  const items = skipped.data?.items ?? [];
  const total = skipped.data?.total ?? 0;
  if (items.length === 0 && marked.length === 0) return null;
  const where = `${source.repo} · ${source.branch}${source.path === "." ? "" : ` · ${source.path}`}`;
  return (
    <section className="mt-6" aria-labelledby={`src-${source.id}`}>
      <h2 id={`src-${source.id}`} className="text-sm font-semibold text-ink">
        Docs in {where} that name no type
      </h2>
      {items.length > 0 ? (
        <p className="mt-1 text-sm text-ink-2">
          Accept a type to review the doc. Speccy holds the type, and the repo takes no commit.
          {total > items.length ? ` ${total} files name no type. Narrow the source to a folder to see the rest.` : ""}
        </p>
      ) : null}
      {adopt.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(adopt.error)} />
        </div>
      ) : null}
      {mapping.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(mapping.error)} />
        </div>
      ) : null}
      {items.length > 0 ? (
        <ul className="mt-2 overflow-hidden rounded-lg border border-line bg-surface">
          {items.map((it) => (
            <SourceSkippedRow
              key={it.path}
              sourceId={source.id}
              doc={it}
              profileKey={picked[it.path] || it.adopted || it.guess || ""}
              profiles={profiles.data?.items ?? []}
              onPick={(k) => setPicked({ ...picked, [it.path]: k })}
              onDismiss={() => dismiss.mutate({ body: { path: it.path, source_id: source.id } })}
              dismissing={dismiss.isPending}
              onAdopt={(profile, link) =>
                adopt.mutate({
                  path: { sourceId: source.id },
                  body: { items: [{ path: it.path, profile, ...(link ? { link } : {}) }] },
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
                  <span title={d.path} className="min-w-0 truncate font-mono text-xs text-ink-2">
                    {d.path}
                  </span>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={undismiss.isPending}
                    onClick={() => undismiss.mutate({ query: { path: d.path, source_id: source.id } })}
                  >
                    Undo
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
      {(source.adopted ?? 0) > 0 ? (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            disabled={mapping.isPending}
            onClick={() => mapping.mutate({ path: { sourceId: source.id } })}
          >
            Write the mapping to the repo
          </Button>
          <span className="text-xs text-ink-3">
            It opens one pull request that adds the types to .speccy.yaml. speccy init --github adds the Action too.
          </span>
          {mapping.data ? (
            <a href={mapping.data.pr_url} target="_blank" rel="noreferrer" className="text-xs text-accent">
              Pull request #{mapping.data.pr_number}
            </a>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

// SourceSkippedRow is one doc in a repo that names no type, with the link Speccy offers when a
// person accepts a type. Speccy keeps the type and the link; the repo takes no commit.
function SourceSkippedRow({
  sourceId,
  doc,
  profileKey,
  profiles,
  onPick,
  onDismiss,
  dismissing,
  onAdopt,
  adopting,
}: {
  sourceId: string;
  doc: SourceSkippedDoc;
  profileKey: string;
  profiles: Profile[];
  onPick: (key: string) => void;
  onDismiss: () => void;
  dismissing: boolean;
  onAdopt: (profile: string, link?: ConfirmedLink) => void;
  adopting: boolean;
}) {
  const offer = useLinkOffer(doc.path, profileKey, { source_id: sourceId });
  return (
    <li className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2 last:border-b-0">
      <span title={doc.path} className="w-full min-w-0 truncate font-mono text-xs text-ink-2 sm:w-auto sm:flex-1">
        {doc.path}
      </span>
      <span className="text-2xs text-ink-3">{doc.guess ? `guess: ${doc.guess}` : "No guess"}</span>
      <select
        aria-label={`Doc type for ${doc.path}`}
        value={profileKey}
        onChange={(e) => onPick(e.target.value)}
        className="h-7 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
      >
        <option value="">Pick a type</option>
        {profiles.map((p) => (
          <option key={p.key} value={p.key}>
            {p.name}
          </option>
        ))}
      </select>
      <Button size="sm" variant="ghost" disabled={dismissing} onClick={onDismiss}>
        Not a spec
      </Button>
      <Button size="sm" disabled={!profileKey || adopting} onClick={() => onAdopt(profileKey, offer.link)}>
        {doc.adopted ? "Change" : "Accept"}
      </Button>
      {offer.view}
    </li>
  );
}
