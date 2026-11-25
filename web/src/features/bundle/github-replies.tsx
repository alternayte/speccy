import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, MessageSquareReply } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { Finding, GithubReply } from "@/lib/api";
import { requestWaiverMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// minNotNeeded is the shortest reason for "Not needed" that the server takes.
const minNotNeeded = 10;

// GithubReplies shows the replies on GitHub to the pull request comment of a finding or a
// questions item, read-only, oldest first. A person who can edit turns a reply into "Not needed"
// or "Ship it open" with the reply as the reason, so nobody copies it by hand
// (docs/specs/pr-source-and-github-review.md). onShip and onNotNeeded take the reply text;
// without onNotNeeded, a finding and a doc ID give an inline "Not needed" form.
export function GithubReplies({
  replies,
  finding,
  docId,
  onShip,
  onNotNeeded,
  onDone,
  className = "mx-4 mb-3",
}: {
  className?: string;
  replies?: GithubReply[];
  finding?: Finding;
  docId?: string;
  onShip?: (text: string) => void;
  onNotNeeded?: (text: string) => void;
  onDone?: (text: string) => void;
}) {
  const [form, setForm] = useState<string>();
  if (!replies?.length) return null;
  const canNotNeeded = onNotNeeded || (finding && docId);
  return (
    <div className={`${className} space-y-2`}>
      {replies.map((r) => (
        <div key={r.url} className="rounded-md border border-line bg-sunken px-3 py-2 text-xs">
          <p className="flex flex-wrap items-center gap-x-1.5 text-ink-2">
            <MessageSquareReply aria-hidden className="size-3.5" />
            <span className="font-mono text-ink">@{r.author}</span> on GitHub
            <a
              href={r.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-0.5 hover:text-ink"
            >
              {new Date(r.created_at).toLocaleDateString()} <ExternalLink aria-hidden className="size-3" />
            </a>
          </p>
          <p className="mt-1 text-sm whitespace-pre-wrap text-ink">{r.body}</p>
          {canNotNeeded || onShip ? (
            <p className="mt-1.5 flex gap-3">
              {canNotNeeded ? (
                <button
                  type="button"
                  className="font-medium text-accent hover:underline"
                  onClick={() => (onNotNeeded ? onNotNeeded(r.body) : setForm(r.body))}
                >
                  Not needed
                </button>
              ) : null}
              {onShip ? (
                <button
                  type="button"
                  className="font-medium text-accent hover:underline"
                  onClick={() => onShip(r.body)}
                >
                  Ship it open
                </button>
              ) : null}
            </p>
          ) : null}
        </div>
      ))}
      {form !== undefined && finding && docId ? (
        <NotNeededForm
          finding={finding}
          docId={docId}
          initial={form}
          onCancel={() => setForm(undefined)}
          onDone={(t) => {
            setForm(undefined);
            onDone?.(t);
          }}
        />
      ) : null}
    </div>
  );
}

// NotNeededForm answers that a finding does not apply to this doc, with a reason. It closes at
// once, with no approval.
function NotNeededForm({
  finding,
  docId,
  initial,
  onCancel,
  onDone,
}: {
  finding: Finding;
  docId: string;
  initial: string;
  onCancel: () => void;
  onDone: (text: string) => void;
}) {
  const qc = useQueryClient();
  const [reason, setReason] = useState(initial);
  const save = useMutation({
    ...requestWaiverMutation(),
    onSuccess: () => {
      qc.invalidateQueries();
      onDone("Marked not needed. The finding no longer counts in the verdict.");
    },
  });
  return (
    <form
      className="space-y-2 rounded-md border border-line p-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate({ path: { docId }, body: { finding_id: finding.id, reason: reason.trim(), not_needed: true } });
      }}
    >
      <label className="block text-xs font-medium text-ink" htmlFor={`nn-${finding.id}`}>
        Why the finding does not apply to this doc
      </label>
      <Textarea id={`nn-${finding.id}`} rows={3} value={reason} onChange={(e) => setReason(e.target.value)} />
      {save.isError ? <ErrorState message={problemMessage(save.error)} /> : null}
      <div className="flex justify-end gap-2">
        <Button size="sm" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          size="sm"
          variant="primary"
          type="submit"
          disabled={save.isPending || reason.trim().length < minNotNeeded}
        >
          Not needed
        </Button>
      </div>
    </form>
  );
}
