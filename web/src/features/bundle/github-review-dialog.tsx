import { useMutation } from "@tanstack/react-query";
import { ExternalLink, Link2, MessageSquareShare } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ErrorState, Loading } from "@/components/ui/states";
import type { GithubReviewItem, GithubReviewRequest, SpecDoc } from "@/lib/api";
import { planGithubReviewMutation, postGithubReviewMutation } from "@/lib/api/@tanstack/react-query.gen";
import { authMessage, linkGitHub } from "@/lib/auth";
import { problemMessage } from "@/lib/problem";
import { verdictText } from "./verdict";

type Preset = GithubReviewRequest["preset"];

const presets: { value: Preset; label: string; help: string }[] = [
  { value: "questions", label: "Questions", help: "What blocks Build Ready and needs the author." },
  { value: "must", label: "MUST findings", help: "Every open MUST finding." },
  { value: "all", label: "All findings", help: "Every open finding." },
];

const kindText: Record<GithubReviewItem["kind"], string> = {
  material_gap: "Material gap",
  blocking_thread: "Blocking thread",
  decision: "Decision",
  finding: "Finding",
};

// GitHubReview prepares a pending review on the pull request of a pull request source, as the
// person: a checklist of the questions screen items, the MUST findings or all findings, grouped by
// spec doc. The person unticks what they do not want and confirms. Only they see the review on
// GitHub until they submit it there (docs/specs/pr-source-and-github-review.md).
export function GitHubReview({ bundle }: { bundle: SpecDoc }) {
  const pr = bundle.github?.pull;
  const [open, setOpen] = useState(false);
  const [preset, setPreset] = useState<Preset>("questions");
  const [off, setOff] = useState<Record<string, boolean>>({});
  const plan = useMutation(planGithubReviewMutation());
  const post = useMutation(postGithubReviewMutation());
  const url = pr?.url ?? "";
  // The checklist follows the preset. A new preset ticks every item again.
  useEffect(() => {
    if (!open || !url) return;
    setOff({});
    post.reset();
    plan.mutate({ body: { url, preset } });
    // plan and post are stable mutations; only the dialog and the preset start a new plan.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, preset, url]);
  if (!pr || pr.state !== "open" || bundle.github?.read_only) return null;
  const items = (plan.data?.docs ?? []).flatMap((d) => d.items);
  const picked = items.filter((it) => !off[it.key]);
  return (
    <>
      <Button
        size="sm"
        variant="ghost"
        icon={<MessageSquareShare className="size-3.5" />}
        onClick={() => setOpen(true)}
        title={`Prepare a GitHub review of pull request #${pr.number}`}
      >
        <span className="hidden sm:inline">Prepare GitHub review</span>
        <span className="sr-only sm:hidden">Prepare GitHub review</span>
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title="Prepare a GitHub review"
        description={`Pull request #${pr.number}: ${pr.title}. Speccy writes a pending review as you. Only you see it until you submit it on GitHub.`}
        wide
      >
        <div className="space-y-4 text-sm">
          <fieldset className="flex flex-wrap gap-x-4 gap-y-2">
            <legend className="sr-only">What goes into the review</legend>
            {presets.map((p) => (
              <label key={p.value} className="flex cursor-pointer items-start gap-2" title={p.help}>
                <input
                  type="radio"
                  name="preset"
                  className="mt-1 accent-[var(--color-accent)]"
                  checked={preset === p.value}
                  onChange={() => setPreset(p.value)}
                />
                <span>
                  <span className="font-medium">{p.label}</span>
                  <span className="block text-xs text-ink-2">{p.help}</span>
                </span>
              </label>
            ))}
          </fieldset>

          {plan.isPending ? <Loading label="Reading the pull request" /> : null}
          {plan.isError ? <ErrorState message={problemMessage(plan.error)} /> : null}

          {plan.data ? (
            <>
              {plan.data.docs.map((d) => (
                <section key={d.doc_id} aria-labelledby={`ghr-${d.doc_id}`}>
                  <h3 id={`ghr-${d.doc_id}`} className="flex flex-wrap items-baseline gap-x-2 text-sm font-semibold">
                    <span className="font-mono text-xs">{d.path}</span>
                    <span className="text-xs font-normal text-ink-2">
                      {d.profile}
                      {d.verdict ? ` · ${verdictText[d.verdict]}` : " · Not reviewed"}
                    </span>
                  </h3>
                  {d.items.length === 0 ? (
                    <p className="mt-1 text-xs text-ink-3">
                      {d.verdict ? "Nothing of this kind to send." : "Review this doc first."}
                    </p>
                  ) : (
                    <ul className="mt-1.5 divide-y divide-line rounded-md border border-line">
                      {d.items.map((it) => (
                        <ReviewItem
                          key={it.key}
                          item={it}
                          on={!off[it.key]}
                          onToggle={(on) => setOff({ ...off, [it.key]: !on })}
                        />
                      ))}
                    </ul>
                  )}
                </section>
              ))}
              {plan.data.already > 0 ? (
                <p className="text-xs text-ink-3">
                  {plan.data.already} item{plan.data.already === 1 ? " is" : "s are"} on the pull request or in your
                  pending review already, so Speccy leaves {plan.data.already === 1 ? "it" : "them"} out.
                </p>
              ) : null}
              {!plan.data.can_post ? (
                plan.data.link_required ? (
                  <LinkFirst />
                ) : (
                  <ErrorState message={plan.data.reason ?? "You cannot post this review."} />
                )
              ) : null}
            </>
          ) : null}

          {post.isError ? <ErrorState message={problemMessage(post.error)} /> : null}
          {post.data ? (
            <p role="status" className="rounded-md border border-line bg-sunken px-3 py-2 text-ink">
              Speccy added {post.data.added} comment{post.data.added === 1 ? "" : "s"} to your pending review
              {post.data.removed > 0 ? ` and took out ${post.data.removed} whose finding is gone` : ""}.{" "}
              <a
                href={post.data.review_url || url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 text-accent underline"
              >
                Check it and submit it on GitHub <ExternalLink aria-hidden className="size-3.5" />
              </a>
            </p>
          ) : null}

          <div className="flex justify-end gap-2">
            <Button onClick={() => setOpen(false)}>{post.data ? "Close" : "Cancel"}</Button>
            {!post.data ? (
              <Button
                variant="primary"
                disabled={!plan.data?.can_post || picked.length === 0 || post.isPending}
                onClick={() =>
                  post.mutate({
                    body: { url, preset, exclude: items.filter((it) => off[it.key]).map((it) => it.key) },
                  })
                }
              >
                {post.isPending
                  ? "Writing"
                  : `Write ${picked.length} item${picked.length === 1 ? "" : "s"} to my pending review`}
              </Button>
            ) : null}
          </div>
        </div>
      </Dialog>
    </>
  );
}

// ReviewItem is one comment of the checklist: where it goes, and its text.
function ReviewItem({ item, on, onToggle }: { item: GithubReviewItem; on: boolean; onToggle: (on: boolean) => void }) {
  const where = item.line > 0 ? `${item.path}:${item.line}` : item.path;
  return (
    <li className="px-3 py-2">
      <label className="flex cursor-pointer items-start gap-2">
        <input type="checkbox" className="mt-1" checked={on} onChange={(e) => onToggle(e.target.checked)} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-baseline gap-x-2 text-xs text-ink-2">
            <span className="font-medium text-ink">
              {item.level ? `${item.level} · ` : ""}
              {kindText[item.kind]}
            </span>
            <span className="font-mono">{where}</span>
            <span>
              {item.inline ? "On the line" : "In the review body: the pull request does not change this line"}
            </span>
            {item.existing_comments > 0 ? (
              <span className="text-warn">
                {item.existing_comments} comment{item.existing_comments === 1 ? "" : "s"} on this line already
              </span>
            ) : null}
          </span>
          <span className="mt-1 block text-sm whitespace-pre-wrap text-ink">{readable(item.body)}</span>
        </span>
      </label>
    </li>
  );
}

// readable drops the small markup lines of a comment, which GitHub renders: the check line and
// links.
function readable(body: string): string {
  return body
    .split("\n\n")
    .filter((p) => !p.startsWith("<sub>"))
    .join("\n\n");
}

// LinkFirst asks the person to link GitHub, so the review goes to GitHub as them.
function LinkFirst() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  return (
    <div className="space-y-2 rounded-md border border-line bg-sunken p-3">
      <p>Link your GitHub account first. The review goes to GitHub as you.</p>
      <Button
        size="sm"
        variant="primary"
        icon={<Link2 className="size-3.5" />}
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await linkGitHub(window.location.pathname + window.location.search);
          } catch (err) {
            setError(authMessage(err));
            setBusy(false);
          }
        }}
      >
        {busy ? "Opening GitHub" : "Link GitHub"}
      </Button>
      {error ? <ErrorState message={error} /> : null}
    </div>
  );
}
