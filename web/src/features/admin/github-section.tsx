import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GitBranch, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import {
  deleteGithubConnectionMutation,
  deleteGithubSourceMutation,
  getGithubConnectionOptions,
  getGithubConnectionQueryKey,
  listGithubSourcesOptions,
  listGithubSourcesQueryKey,
  setGithubConnectionMutation,
  syncGithubSourceMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { relativeTime as timeAgo } from "@/features/bundle/time";
import { problemMessage } from "@/lib/problem";
import { GitHubDialog } from "@/features/bundle/github-dialog";
import { useMe } from "@/features/account/me";
import { Section } from "./admin-page";

// GitHubSection sets the workspace token (DEC-019) and the repos Speccy reads bundles from
// (REQ-123). In local mode a token here replaces the gh login.
export function GitHubSection() {
  const qc = useQueryClient();
  const local = useMe().data?.mode === "local";
  const conn = useQuery(getGithubConnectionOptions());
  const sources = useQuery(listGithubSourcesOptions());
  const refresh = () => {
    qc.invalidateQueries({ queryKey: getGithubConnectionQueryKey() });
    qc.invalidateQueries({ queryKey: listGithubSourcesQueryKey() });
  };
  const [adding, setAdding] = useState(false);
  const [token, setToken] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [login, setLogin] = useState<string>();
  const save = useMutation({
    ...setGithubConnectionMutation(),
    onSuccess: (c) => {
      setToken("");
      setLogin(c.login);
      refresh();
    },
  });
  const remove = useMutation({ ...deleteGithubConnectionMutation(), onSuccess: refresh });
  const sync = useMutation({ ...syncGithubSourceMutation(), onSuccess: refresh });
  const drop = useMutation({ ...deleteGithubSourceMutation(), onSuccess: refresh });
  const configured = !!conn.data?.configured;
  const error = sync.error ?? drop.error ?? remove.error;

  return (
    <>
      <Section title="GitHub token">
        <form
          className="space-y-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate({ body: { token, api_url: apiUrl || undefined } });
          }}
        >
          <p className="text-sm text-ink-2">
            {configured
              ? `A token ending in ${conn.data?.token_last4} is set${login ? `, for ${login}` : ""}.`
              : local
                ? "No token is set, so Speccy reads GitHub with your gh login."
                : "No token is set."}{" "}
            Use a fine-grained personal access token with read and write access to Contents and Pull requests on the
            repos Speccy reads.
          </p>
          <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
            <div>
              <Label htmlFor="gh-token">{configured ? "New token" : "Token"}</Label>
              <Input
                id="gh-token"
                type="password"
                autoComplete="off"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="github_pat_…"
              />
            </div>
            <div>
              <Label htmlFor="gh-api">API address (optional)</Label>
              <Input
                id="gh-api"
                value={apiUrl}
                onChange={(e) => setApiUrl(e.target.value)}
                placeholder={conn.data?.api_url ?? "https://api.github.com"}
              />
            </div>
            <div className="flex gap-1.5">
              <Button type="submit" variant="primary" disabled={!token.trim() || save.isPending}>
                {save.isPending ? "Checking" : "Save"}
              </Button>
              {configured ? (
                <Button onClick={() => remove.mutate({})} disabled={remove.isPending}>
                  Remove
                </Button>
              ) : null}
            </div>
          </div>
          {save.isError ? <ErrorState message={problemMessage(save.error)} /> : null}
        </form>
      </Section>

      <Section title="GitHub sources">
        {sources.isPending ? (
          <Loading label="Loading sources" />
        ) : sources.isError ? (
          <div className="p-3">
            <ErrorState message={problemMessage(sources.error)} />
          </div>
        ) : sources.data.items.length === 0 ? (
          <Empty title="No GitHub sources">
            Paste the address of a repo, a folder, or one doc. Speccy reads the bundles in it, and keeps them in step
            every 5 minutes. Edits in Speccy are drafts until an author publishes them as a pull request.
          </Empty>
        ) : (
          <ul className="divide-y divide-line">
            {sources.data.items.map((s) => (
              <li key={s.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5 text-sm">
                <GitBranch aria-hidden className="size-4 shrink-0 text-ink-3" />
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">
                    {s.repo} <span className="font-mono text-xs text-ink-2">{s.branch}</span>{" "}
                    <span className="font-mono text-xs text-ink-3">{s.path === "." ? "/" : s.path}</span>
                  </p>
                  <p className="truncate text-xs text-ink-3">
                    {s.bundles} bundle{s.bundles === 1 ? "" : "s"}
                    {s.head_commit ? ` · ${s.head_commit.slice(0, 7)}` : ""}
                    {s.synced_at ? ` · read ${timeAgo(s.synced_at)}` : ""}
                  </p>
                  {s.error ? <p className="mt-0.5 text-xs text-bad">{s.error}</p> : null}
                </div>
                <Button
                  size="sm"
                  icon={<RefreshCw className="size-3.5" />}
                  onClick={() => sync.mutate({ path: { sourceId: s.id } })}
                  disabled={sync.isPending}
                >
                  Sync
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={`Remove ${s.repo}`}
                  icon={<Trash2 className="size-3.5" />}
                  onClick={() => {
                    if (
                      window.confirm(
                        `Stop reading ${s.repo}? Its bundles are archived. Their reviews and threads stay.`,
                      )
                    )
                      drop.mutate({ path: { sourceId: s.id } });
                  }}
                />
              </li>
            ))}
          </ul>
        )}
        {error ? (
          <div className="border-t border-line p-3">
            <ErrorState message={problemMessage(error)} />
          </div>
        ) : null}
        <div className="flex flex-wrap items-center gap-3 border-t border-line p-4">
          <Button variant="primary" disabled={!configured && !local} onClick={() => setAdding(true)}>
            Add a source
          </Button>
          <p className="text-xs text-ink-3">
            {configured || local
              ? "Paste a repo, a folder, or a doc address. Speccy shows what it found before it reads it."
              : "Set a token first."}
          </p>
        </div>
        <GitHubDialog open={adding} onOpenChange={setAdding} />
      </Section>
    </>
  );
}
