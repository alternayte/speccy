import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import { useMe } from "@/features/account/me";
import type { AddedSource, GithubResolved, PullDocChoice, SuggestedLink } from "@/lib/api";
import {
  addGithubSourceMutation,
  listBundlesQueryKey,
  listGithubSourcesQueryKey,
  resolveGithubUrlMutation,
  suggestLinksMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// GitHubDialog takes a source URL, shows what it names, and makes the source on confirm
// (REQ-128). Speccy resolves before it acts, so a person sees the branch, the folder and the
// profile before a source exists. A source always reads a folder: the URL of one doc reads the
// doc's folder and opens that doc. A pull request URL lists the markdown files it changes, each
// with a doc type or "Not a spec", as import does (docs/specs/pr-source-and-github-review.md).
export function GitHubDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [url, setUrl] = useState("");
  const me = useMe().data;
  const prOnly = me?.mode === "hosted" && me.role !== "admin";
  const [found, setFound] = useState<GithubResolved | null>(null);
  const [profile, setProfile] = useState("");
  // The picks of a pull request URL: a doc type per file, "" for not a spec.
  const [picked, setPicked] = useState<Record<string, string>>({});
  const [links, setLinks] = useState<SuggestedLink[]>([]);
  const [unchecked, setUnchecked] = useState<Record<string, boolean>>({});
  const suggest = useMutation({ ...suggestLinksMutation(), onSuccess: (s) => setLinks(s.items) });
  const linkKey = (l: SuggestedLink) => `${l.from}>${l.to}`;
  const pickDoc = (path: string, key: string) => {
    const next = { ...picked, [path]: key };
    setPicked(next);
    suggest.mutate({ body: { docs: Object.entries(next).map(([p, k]) => ({ path: p, profile: k })) } });
  };
  const qc = useQueryClient();
  const navigate = useNavigate();
  // done holds an add that did not open a page: a folder another source reads, or a first sync
  // that failed (#67).
  const [done, setDone] = useState<AddedSource | null>(null);

  const open_ = (r: AddedSource) => {
    close(false);
    if (r.doc_id && r.bundle_id)
      navigate({ to: "/bundles/$bundleId/docs/$docId", params: { bundleId: r.bundle_id, docId: r.doc_id } });
    else if (r.bundle_id) navigate({ to: "/bundles/$bundleId", params: { bundleId: r.bundle_id } });
  };

  const resolve = useMutation({
    ...resolveGithubUrlMutation(),
    onSuccess: (r) => {
      setFound(r);
      setProfile(r.profile ?? "");
      if (r.pull) {
        const next = Object.fromEntries((r.docs ?? []).map((d) => [d.path, d.profile ?? ""]));
        setPicked(next);
        setUnchecked({});
        suggest.mutate({ body: { docs: Object.entries(next).map(([p, k]) => ({ path: p, profile: k })) } });
      }
    },
  });
  const add = useMutation({
    ...addGithubSourceMutation(),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: listBundlesQueryKey() });
      qc.invalidateQueries({ queryKey: listGithubSourcesQueryKey() });
      if (r.already_added || r.source.error) setDone(r);
      else open_(r);
    },
  });

  const close = (o: boolean) => {
    if (!o) {
      setUrl("");
      setFound(null);
      setProfile("");
      setPicked({});
      setLinks([]);
      setDone(null);
      resolve.reset();
      add.reset();
    }
    onOpenChange(o);
  };

  const folderOf = (r: GithubResolved) => (r.file ? r.path.split("/").slice(0, -1).join("/") || "." : r.path);
  const what = (r: GithubResolved) => (folderOf(r) === "." ? "the whole repo" : folderOf(r) + "/");

  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title="Read a repo on GitHub"
      description="Speccy reads the docs through the GitHub API. It never writes to the branch; an edit here becomes a pull request."
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!found) {
            resolve.mutate({ body: { url } });
            return;
          }
          if (found.pull) {
            const docs: PullDocChoice[] = (found.docs ?? []).map((d) => {
              const link = links.find((l) => l.from === d.path && !unchecked[linkKey(l)] && picked[l.to]);
              return {
                path: d.path,
                profile: picked[d.path] ?? "",
                ...(link ? { link: { kind: link.kind, target: link.to } } : {}),
              };
            });
            add.mutate({ body: { url, docs } });
            return;
          }
          add.mutate({ body: { url, ...(profile ? { profile } : {}) } });
        }}
        className="space-y-4"
      >
        <div>
          <Label htmlFor="gh-url">Address</Label>
          <Input
            id="gh-url"
            value={url}
            autoFocus
            placeholder={
              prOnly
                ? "https://github.com/acme/specs/pull/42"
                : "https://github.com/acme/specs/blob/main/docs/prd-payments.md"
            }
            onChange={(e) => {
              setUrl(e.target.value);
              setFound(null);
              setDone(null);
              resolve.reset();
              add.reset();
            }}
          />
          <p className="mt-1 text-xs text-ink-3">
            {prOnly ? (
              "A pull request you opened or review. Speccy checks it with your linked GitHub account. An admin adds repos, folders and single docs."
            ) : (
              <>
                A repo, a folder in one, a single doc, or a pull request. <code>owner/name</code> works too.
              </>
            )}
          </p>
        </div>

        {found?.pull ? (
          <PullFound
            found={found}
            picked={picked}
            onPick={pickDoc}
            links={links.filter((l) => picked[l.from] && picked[l.to])}
            unchecked={unchecked}
            onToggle={(l, on) => setUnchecked({ ...unchecked, [linkKey(l)]: !on })}
            linkKey={linkKey}
          />
        ) : found ? (
          <dl className="space-y-1.5 rounded-md border border-line bg-sunken px-3 py-2 text-xs">
            <div className="flex gap-2">
              <dt className="w-16 shrink-0 text-ink-3">Repo</dt>
              <dd className="font-mono text-ink">{found.repo}</dd>
            </div>
            <div className="flex gap-2">
              <dt className="w-16 shrink-0 text-ink-3">Branch</dt>
              <dd className="font-mono text-ink">{found.branch}</dd>
            </div>
            <div className="flex gap-2">
              <dt className="w-16 shrink-0 text-ink-3">Folder</dt>
              <dd className="min-w-0 font-mono break-all text-ink">{what(found)}</dd>
            </div>
            {found.file ? (
              <div className="flex gap-2">
                <dt className="w-16 shrink-0 text-ink-3">Opens</dt>
                <dd className="min-w-0 font-mono break-all text-ink">{found.path}</dd>
              </div>
            ) : null}
            {found.title ? (
              <div className="flex gap-2">
                <dt className="w-16 shrink-0 text-ink-3">Title</dt>
                <dd className="text-ink">{found.title}</dd>
              </div>
            ) : null}
          </dl>
        ) : null}

        {found?.file ? (
          <div>
            <Label htmlFor="gh-profile">Doc type</Label>
            <select
              id="gh-profile"
              value={profile}
              onChange={(e) => setProfile(e.target.value)}
              className="h-9 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-ink"
            >
              <option value="">Pick a type</option>
              {found.profiles.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
            <p className="mt-1 text-xs text-ink-3">
              {found.guessed
                ? "The doc names no type. Speccy guessed this one from its headings, and writes nothing into the repo."
                : "The doc names this type, or the repo maps it."}
            </p>
          </div>
        ) : null}

        {resolve.isError ? <ErrorState message={problemMessage(resolve.error)} /> : null}
        {add.isError ? <ErrorState message={problemMessage(add.error)} /> : null}
        {done?.source.error ? (
          <ErrorState message={`Speccy added the source, and its first sync failed: ${done.source.error}`} />
        ) : done?.already_added ? (
          <p role="status" className="rounded-md border border-line bg-sunken px-3 py-2 text-sm text-ink">
            Already added. The source for {done.source.path === "." ? "the whole repo" : `${done.source.path}/`} reads
            this {found?.file ? "doc" : "folder"}.
          </p>
        ) : null}

        <div className="flex justify-end gap-2">
          <Button type="button" onClick={() => close(false)}>
            Cancel
          </Button>
          {done ? (
            <Button type="button" variant="primary" disabled={!done.bundle_id} onClick={() => open_(done)}>
              Open
            </Button>
          ) : (
            <Button
              type="submit"
              variant="primary"
              disabled={
                resolve.isPending ||
                add.isPending ||
                url.trim() === "" ||
                (found?.file === true && profile === "") ||
                (found?.pull !== undefined && !Object.values(picked).some((k) => k !== ""))
              }
            >
              {!found ? (resolve.isPending ? "Looking" : "Look") : add.isPending ? "Reading" : "Add"}
            </Button>
          )}
        </div>
      </form>
    </Dialog>
  );
}

// PullFound shows the pull request a URL names, and asks for a doc type for each markdown file
// it changes. Speccy writes nothing into the repo: a type is held in Speccy.
function PullFound({
  found,
  picked,
  onPick,
  links,
  unchecked,
  onToggle,
  linkKey,
}: {
  found: GithubResolved;
  picked: Record<string, string>;
  onPick: (path: string, key: string) => void;
  links: SuggestedLink[];
  unchecked: Record<string, boolean>;
  onToggle: (l: SuggestedLink, on: boolean) => void;
  linkKey: (l: SuggestedLink) => string;
}) {
  const pr = found.pull!;
  const docs = found.docs ?? [];
  return (
    <div className="space-y-3">
      <dl className="space-y-1.5 rounded-md border border-line bg-sunken px-3 py-2 text-xs">
        <div className="flex gap-2">
          <dt className="w-16 shrink-0 text-ink-3">Pull</dt>
          <dd className="min-w-0 text-ink">
            <span className="font-mono">#{pr.number}</span> {pr.title}
            {pr.draft ? <span className="ml-1 text-ink-3">(draft)</span> : null}
          </dd>
        </div>
        <div className="flex gap-2">
          <dt className="w-16 shrink-0 text-ink-3">Author</dt>
          <dd className="font-mono text-ink">{pr.author}</dd>
        </div>
        <div className="flex gap-2">
          <dt className="w-16 shrink-0 text-ink-3">Branch</dt>
          <dd className="min-w-0 font-mono break-all text-ink">
            {pr.head_branch} → {pr.base_branch}
          </dd>
        </div>
      </dl>
      {pr.state !== "open" ? (
        <ErrorState message={`Pull request #${pr.number} is ${pr.state}. Speccy follows an open pull request.`} />
      ) : docs.length === 0 ? (
        <p role="status" className="rounded-md border border-line bg-sunken px-3 py-2 text-sm text-ink">
          This pull request changes no markdown file.
        </p>
      ) : (
        <fieldset>
          <legend className="text-sm font-medium text-ink">Doc types</legend>
          <ul className="mt-1.5 divide-y divide-line rounded-md border border-line">
            {docs.map((d) => (
              <li key={d.path} className="flex flex-wrap items-center gap-2 px-3 py-2">
                <span title={d.path} className="w-full min-w-0 truncate font-mono text-xs text-ink sm:w-auto sm:flex-1">
                  {d.path}
                </span>
                <select
                  aria-label={`Doc type for ${d.path}`}
                  value={picked[d.path] ?? ""}
                  onChange={(e) => onPick(d.path, e.target.value)}
                  className="h-7 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
                >
                  <option value="">Not a spec</option>
                  {found.profiles.map((k) => (
                    <option key={k} value={k}>
                      {k}
                    </option>
                  ))}
                </select>
                <span className="w-full text-2xs text-ink-3">
                  {d.title ? `${d.title}. ` : ""}
                  {d.guessed
                    ? d.profile
                      ? `Speccy guessed ${d.profile} from the headings. It writes nothing into the repo.`
                      : "No profile fits its headings. Pick one, or leave it out."
                    : `The file names ${d.profile}, or the repo maps it.`}
                </span>
              </li>
            ))}
          </ul>
          <p className="mt-1 text-2xs text-ink-3">
            Each doc becomes a bundle with its own review. The person who opened the pull request is its author.
          </p>
        </fieldset>
      )}
      {links.length > 0 ? (
        <fieldset>
          <legend className="text-sm font-medium text-ink">Links</legend>
          {links.map((l) => (
            <label key={linkKey(l)} className="mt-1.5 flex items-center gap-2 text-sm text-ink-2">
              <input type="checkbox" checked={!unchecked[linkKey(l)]} onChange={(e) => onToggle(l, e.target.checked)} />
              <span>
                <code>{l.from}</code> {l.kind} <code>{l.to}</code>
              </span>
            </label>
          ))}
          <p className="mt-1 text-2xs text-ink-3">Speccy keeps the link. It writes nothing into the repo.</p>
        </fieldset>
      ) : null}
    </div>
  );
}
