import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/states";
import type { GithubSource } from "@/lib/api";
import {
  adoptSkippedDocsMutation,
  listGithubSourcesOptions,
  listProfilesOptions,
  listSkippedDocsOptions,
  publishSourceMappingMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// SourceDocs lists, for each GitHub source, the markdown files the scan passed over. Accepting
// a type holds it in Speccy and makes the bundle: the repo takes no commit (REQ-133).
export function SourceDocs() {
  const sources = useQuery(listGithubSourcesOptions());
  const items = (sources.data?.items ?? []).filter((s) => !s.file);
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
  const items = skipped.data?.items ?? [];
  const total = skipped.data?.total ?? 0;
  if (items.length === 0) return null;
  const where = `${source.repo} · ${source.branch}${source.path === "." ? "" : ` · ${source.path}`}`;
  return (
    <section className="mt-6" aria-labelledby={`src-${source.id}`}>
      <h2 id={`src-${source.id}`} className="text-sm font-semibold text-ink">
        Docs in {where} that name no type
      </h2>
      <p className="mt-1 text-sm text-ink-2">
        Accept a type to review the doc. Speccy holds the type, and the repo takes no commit.
        {total > items.length ? ` ${total} files name no type. Narrow the source to a folder to see the rest.` : ""}
      </p>
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
      <ul className="mt-2 overflow-hidden rounded-lg border border-line bg-surface">
        {items.map((it) => {
          const key = picked[it.path] || it.adopted || it.guess || "";
          return (
            <li
              key={it.path}
              className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2 last:border-b-0"
            >
              <span className="w-full min-w-0 truncate font-mono text-xs text-ink-2 sm:w-auto sm:flex-1">
                {it.path}
              </span>
              <span className="text-2xs text-ink-3">{it.guess ? `guess: ${it.guess}` : "No guess"}</span>
              <select
                aria-label={`Doc type for ${it.path}`}
                value={key}
                onChange={(e) => setPicked({ ...picked, [it.path]: e.target.value })}
                className="h-7 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
              >
                <option value="">Pick a type</option>
                {(profiles.data?.items ?? []).map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.name}
                  </option>
                ))}
              </select>
              <Button
                size="sm"
                disabled={!key || adopt.isPending}
                onClick={() =>
                  adopt.mutate({ path: { sourceId: source.id }, body: { items: [{ path: it.path, profile: key }] } })
                }
              >
                {it.adopted ? "Change" : "Accept"}
              </Button>
            </li>
          );
        })}
      </ul>
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
