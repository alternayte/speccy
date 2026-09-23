import { useMutation, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ExternalLink, GitPullRequestArrow } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { SpecDoc } from "@/lib/api";
import { discardDraftMutation, getSpecDocOptions, publishBundleMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// GitHubControl shows where a GitHub bundle comes from, and publishes its draft as a pull
// request (REQ-123). Speccy never changes the branch itself.
export function GitHubControl({ bundle, canEdit }: { bundle: SpecDoc; canEdit: boolean }) {
  const gh = bundle.github;
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const done = () => qc.invalidateQueries({ queryKey: getSpecDocOptions({ path: { docId: bundle.id } }).queryKey });
  const publish = useMutation({ ...publishBundleMutation(), onSuccess: done });
  const discard = useMutation({
    ...discardDraftMutation(),
    onSuccess: () => {
      done();
      setOpen(false);
    },
  });
  if (!gh) return null;
  const where = `${gh.repo} · ${gh.branch}${gh.path === "." ? "" : ` · ${gh.path}`}`;
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        title={where}
        className={clsx(
          "inline-flex h-7 items-center gap-1.5 rounded-md border px-2 text-xs font-medium",
          gh.draft ? "border-warn/40 bg-warn-soft text-warn" : "border-line-strong bg-surface text-ink hover:bg-sunken",
        )}
      >
        <GitPullRequestArrow aria-hidden className="size-3.5" />
        <span className="hidden sm:inline">{gh.draft ? (gh.pr_url ? "PR open" : "Unpublished") : "GitHub"}</span>
        <span className="sr-only sm:hidden">GitHub</span>
      </button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) publish.reset();
        }}
        title={gh.draft ? "Publish the changes" : "From GitHub"}
        description={where}
      >
        <div className="space-y-3 text-sm">
          {!gh.draft ? (
            <p className="text-ink-2">
              This bundle matches GitHub. An edit here stays unpublished until you open a pull request.
            </p>
          ) : (
            <p className="text-ink-2">
              This bundle has changes that are not on GitHub. Publish opens a pull request into {gh.branch}. The branch
              changes only when someone merges it.
            </p>
          )}
          {gh.ahead ? (
            <p className="rounded-md border border-warn/40 bg-warn-soft px-2.5 py-2 text-xs text-ink">
              GitHub changed after these edits started. The pull request may show conflicts, or you can discard the
              edits and take GitHub's text.
            </p>
          ) : null}
          {gh.pr_url ? (
            <p>
              <a
                href={gh.pr_url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 text-accent underline"
              >
                Open the pull request <ExternalLink aria-hidden className="size-3.5" />
              </a>
            </p>
          ) : null}
          {publish.data ? (
            <p className="text-ok">
              Opened{" "}
              <a href={publish.data.pr_url} target="_blank" rel="noreferrer" className="underline">
                pull request #{publish.data.pr_number}
              </a>
              .
            </p>
          ) : null}
          {gh.draft && canEdit && !publish.data ? (
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                publish.mutate({ path: { docId: bundle.id }, body: { message: message || undefined } });
              }}
            >
              <div>
                <Label htmlFor="pr-message">Commit message and pull request title</Label>
                <Input
                  id="pr-message"
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder={`Update ${bundle.title}`}
                />
              </div>
              {publish.isError ? <ErrorState message={problemMessage(publish.error)} /> : null}
              {discard.isError ? <ErrorState message={problemMessage(discard.error)} /> : null}
              <div className="flex justify-between gap-2">
                <Button
                  onClick={() => {
                    if (
                      window.confirm(
                        "Discard the unpublished changes? The text from GitHub becomes current again, as a new version.",
                      )
                    )
                      discard.mutate({ path: { docId: bundle.id } });
                  }}
                  disabled={discard.isPending}
                >
                  Discard the changes
                </Button>
                <Button type="submit" variant="primary" disabled={publish.isPending}>
                  {publish.isPending ? "Publishing" : "Open a pull request"}
                </Button>
              </div>
            </form>
          ) : null}
        </div>
      </Dialog>
    </>
  );
}
