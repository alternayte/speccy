import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { GithubResolved } from "@/lib/api";
import {
  addGithubSourceMutation,
  listBundlesQueryKey,
  listGithubSourcesQueryKey,
  resolveGithubUrlMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// GitHubDialog takes a source URL, shows what it names, and makes the source on confirm
// (REQ-128). Speccy resolves before it acts, so a person sees the branch, the doc and the
// profile before a source exists.
export function GitHubDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [url, setUrl] = useState("");
  const [found, setFound] = useState<GithubResolved | null>(null);
  const [profile, setProfile] = useState("");
  const qc = useQueryClient();

  const resolve = useMutation({
    ...resolveGithubUrlMutation(),
    onSuccess: (r) => {
      setFound(r);
      setProfile(r.profile ?? "");
    },
  });
  const add = useMutation({
    ...addGithubSourceMutation(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: listBundlesQueryKey() });
      qc.invalidateQueries({ queryKey: listGithubSourcesQueryKey() });
      close(false);
    },
  });

  const close = (o: boolean) => {
    if (!o) {
      setUrl("");
      setFound(null);
      setProfile("");
      resolve.reset();
      add.reset();
    }
    onOpenChange(o);
  };

  const what = (r: GithubResolved) => (r.file ? r.path : r.path === "." ? "the whole repo" : r.path + "/");

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
            placeholder="https://github.com/acme/specs/blob/main/docs/prd-payments.md"
            onChange={(e) => {
              setUrl(e.target.value);
              setFound(null);
              resolve.reset();
              add.reset();
            }}
          />
          <p className="mt-1 text-xs text-ink-3">
            A repo, a folder in one, or a single doc. <code>owner/name</code> works too.
          </p>
        </div>

        {found ? (
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
              <dt className="w-16 shrink-0 text-ink-3">{found.file ? "Doc" : "Folder"}</dt>
              <dd className="min-w-0 font-mono break-all text-ink">{what(found)}</dd>
            </div>
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

        <div className="flex justify-end gap-2">
          <Button type="button" onClick={() => close(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              resolve.isPending || add.isPending || url.trim() === "" || (found?.file === true && profile === "")
            }
          >
            {!found ? (resolve.isPending ? "Looking" : "Look") : add.isPending ? "Reading" : "Add"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
