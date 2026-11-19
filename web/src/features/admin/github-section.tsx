import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, GitBranch, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { GithubApp } from "@/lib/api";
import {
  deleteGithubAppMutation,
  deleteGithubConnectionMutation,
  deleteGithubSourceMutation,
  getGithubConnectionOptions,
  getGithubConnectionQueryKey,
  listGithubSourcesOptions,
  listGithubSourcesQueryKey,
  setGithubConnectionMutation,
  startGithubAppMutation,
  syncGithubSourceMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { relativeTime as timeAgo } from "@/features/bundle/time";
import { problemMessage } from "@/lib/problem";
import { GitHubDialog } from "@/features/bundle/github-dialog";
import { useMe } from "@/features/account/me";
import { Section } from "./admin-page";

// GitHubSection connects the workspace to GitHub (docs/specs/github-app.md): a private GitHub
// App in hosted mode, or a fine-grained token, never both. It also lists the repos Speccy reads
// bundles from (REQ-123). In local mode a token here replaces the gh login.
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
  const sync = useMutation({ ...syncGithubSourceMutation(), onSuccess: refresh });
  const drop = useMutation({ ...deleteGithubSourceMutation(), onSuccess: refresh });
  const configured = !!conn.data?.configured;
  const error = sync.error ?? drop.error;

  return (
    <>
      <GitHubConnection />
      <Section title="GitHub sources">
        {sources.isPending ? (
          <Loading label="Loading sources" />
        ) : sources.isError ? (
          <div className="p-3">
            <ErrorState message={problemMessage(sources.error)} />
          </div>
        ) : sources.data.items.length === 0 ? (
          <Empty title="No GitHub sources">
            Paste the address of a repo, a folder, or one doc. Speccy reads the bundles in it, and keeps them in step:
            at once on a push when the GitHub App is connected, and every 5 minutes in any case. Edits in Speccy are
            drafts until an author publishes them as a pull request.
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
              : "Connect GitHub first."}
          </p>
        </div>
        <GitHubDialog open={adding} onOpenChange={setAdding} />
      </Section>
    </>
  );
}

// GitHubConnection shows the one GitHub connection, and makes one: the App first in hosted
// mode, or a token.
export function GitHubConnection() {
  const conn = useQuery(getGithubConnectionOptions());
  const [returned] = useState(() => new URLSearchParams(window.location.search).get("github_error") ?? "");
  if (conn.isPending) return <Loading label="Loading the GitHub connection" />;
  if (conn.isError)
    return (
      <Section title="Connection">
        <div className="p-3">
          <ErrorState message={problemMessage(conn.error)} />
        </div>
      </Section>
    );
  const c = conn.data;
  return (
    <>
      {returned ? <ErrorState message={returned} /> : null}
      {c.kind === "app" && c.app ? (
        <AppCard app={c.app} />
      ) : c.kind === "token" ? (
        <TokenForm />
      ) : c.app_available ? (
        <>
          <CreateApp />
          <TokenForm alternative />
        </>
      ) : (
        <TokenForm />
      )}
    </>
  );
}

// AppCard is the connected App: its owner, whether it is installed, and the way out.
function AppCard({ app }: { app: GithubApp }) {
  const qc = useQueryClient();
  const disconnect = useMutation({
    ...deleteGithubAppMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: getGithubConnectionQueryKey() }),
  });
  return (
    <Section title="GitHub App">
      <div className="space-y-3 p-4 text-sm">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="font-medium">{app.slug}</span>
          <span className="text-ink-2">
            on {app.owner_is_org ? "the org" : "the personal account"} {app.owner}
          </span>
          <span className={app.installed ? "text-ok" : "text-warn"}>
            {app.installed ? "Installed" : "Not installed"}
          </span>
        </div>
        <p className="text-ink-2">
          {app.installed
            ? "Speccy reads the repos the App can reach, and syncs a source at once on a push. A pull request that a person publishes goes to GitHub as that person."
            : `Install the App on ${app.owner}, and choose the repos Speccy may read.`}{" "}
          {app.owner_is_org
            ? `Members of ${app.owner} sign in with GitHub, with no invite.`
            : `${app.owner} signs in with GitHub.`}
        </p>
        <div className="flex flex-wrap gap-2">
          <a href={app.install_url}>
            <Button variant={app.installed ? "secondary" : "primary"} size="sm">
              {app.installed ? "Change the repos" : "Install the App"}
            </Button>
          </a>
          <a href={app.settings_url} target="_blank" rel="noreferrer">
            <Button size="sm" icon={<ExternalLink className="size-3.5" />}>
              App settings on GitHub
            </Button>
          </a>
          {app.from_env ? null : (
            <Button
              size="sm"
              variant="ghost"
              disabled={disconnect.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    "Disconnect the GitHub App? Speccy forgets its keys and every person's GitHub link. The sources stay, and stop syncing until GitHub is connected again. The App stays on GitHub: delete it from its settings page.",
                  )
                )
                  disconnect.mutate({});
              }}
            >
              Disconnect
            </Button>
          )}
        </div>
        {app.missing?.length ? (
          <div className="rounded border border-warn/40 bg-warn/5 p-3 text-ink-1">
            <p className="font-medium">The App lacks {app.missing.join(", ")}.</p>
            <p className="mt-1 text-ink-2">
              Speccy needs them to review pull requests. GitHub does not let Speccy change an App, so add them on the
              App's settings page under Permissions &amp; events. Then accept the new permissions on the installation.
            </p>
          </div>
        ) : null}
        {app.from_env ? (
          <p className="text-xs text-ink-3">The SPECCY_GITHUB_APP_ variables set this App, so it cannot change here.</p>
        ) : null}
        {disconnect.isError ? <ErrorState message={problemMessage(disconnect.error)} /> : null}
      </div>
    </Section>
  );
}

// CreateApp makes a private GitHub App from a manifest: Speccy builds it, the browser posts it
// to GitHub, and GitHub sends the admin back with the App's keys.
function CreateApp() {
  const [org, setOrg] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const start = useMutation({
    ...startGithubAppMutation(),
    onSuccess: (res) => {
      const form = document.createElement("form");
      form.method = "post";
      form.action = res.action_url;
      const field = document.createElement("input");
      field.type = "hidden";
      field.name = "manifest";
      field.value = res.manifest;
      form.appendChild(field);
      document.body.appendChild(form);
      form.submit();
    },
  });
  return (
    <Section title="GitHub App">
      <form
        className="space-y-3 p-4"
        onSubmit={(e) => {
          e.preventDefault();
          start.mutate({ body: { org: org.trim() || undefined, api_url: apiUrl.trim() || undefined } });
        }}
      >
        <p className="text-sm text-ink-2">
          Speccy makes a private GitHub App for this workspace. GitHub asks you to confirm it, then sends you back here
          to install it. Members of the org sign in with GitHub, and a pull request goes to GitHub as the person who
          published it.
        </p>
        <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
          <div>
            <Label htmlFor="gh-org">Org</Label>
            <Input
              id="gh-org"
              value={org}
              onChange={(e) => setOrg(e.target.value)}
              placeholder="Empty: your personal account"
            />
          </div>
          <div>
            <Label htmlFor="gh-app-api">GitHub Enterprise Server API (optional)</Label>
            <Input
              id="gh-app-api"
              value={apiUrl}
              onChange={(e) => setApiUrl(e.target.value)}
              placeholder="https://api.github.com"
            />
          </div>
          <Button type="submit" variant="primary" disabled={start.isPending}>
            {start.isPending ? "Opening GitHub" : "Create the App on GitHub"}
          </Button>
        </div>
        {start.isError ? <ErrorState message={problemMessage(start.error)} /> : null}
      </form>
    </Section>
  );
}

// TokenForm sets or removes the fine-grained token. As the alternative to the App, it is the
// way for an org that cannot add an App.
function TokenForm({ alternative = false }: { alternative?: boolean }) {
  const qc = useQueryClient();
  const local = useMe().data?.mode === "local";
  const conn = useQuery(getGithubConnectionOptions());
  const [token, setToken] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [login, setLogin] = useState<string>();
  const refresh = () => {
    qc.invalidateQueries({ queryKey: getGithubConnectionQueryKey() });
    qc.invalidateQueries({ queryKey: listGithubSourcesQueryKey() });
  };
  const save = useMutation({
    ...setGithubConnectionMutation(),
    onSuccess: (c) => {
      setToken("");
      setLogin(c.login);
      refresh();
    },
  });
  const remove = useMutation({ ...deleteGithubConnectionMutation(), onSuccess: refresh });
  const configured = conn.data?.kind === "token";
  return (
    <Section title={alternative ? "Or a token" : "GitHub token"}>
      <form
        className="space-y-3 p-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate({ body: { token, api_url: apiUrl || undefined } });
        }}
      >
        <p className="text-sm text-ink-2">
          {configured
            ? `A token ending in ${conn.data?.token_last4} is set${login ? `, for ${login}` : ""}. Every pull request goes to GitHub as its owner.`
            : alternative
              ? "Use a token when the org cannot add a GitHub App. Speccy then polls every 5 minutes, every pull request goes to GitHub as the token's owner, and GitHub sign-in is off."
              : local
                ? "No token is set, so Speccy reads GitHub with your gh login."
                : "No token is set."}{" "}
          Use a fine-grained personal access token with read and write access to Contents and Pull requests on the repos
          Speccy reads.
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
        {remove.isError ? <ErrorState message={problemMessage(remove.error)} /> : null}
      </form>
    </Section>
  );
}
