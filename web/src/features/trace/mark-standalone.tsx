import { useMutation, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import { requestWaiverMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// minReason is the waiver mechanism's shortest reason (REQ-072).
const minReason = 20;

// MarkStandalone answers a links.has-upstream finding: the doc has no upstream doc, for a
// reason. It is an Acknowledgement, so it closes at once, with no approval, and writes
// standalone: to the doc's sidecar (SDD §9.4).
export function MarkStandalone({
  docId,
  findingId,
  onDone,
  onCancel,
  className,
}: {
  docId: string;
  findingId: string;
  onDone: (message: string) => void;
  onCancel?: () => void;
  className?: string;
}) {
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const ask = useMutation({
    ...requestWaiverMutation(),
    // An Acknowledgement needs no approval: the finding closes at once.
    onSuccess: () => {
      qc.invalidateQueries();
      onDone("Marked the doc standalone. links.has-upstream passes.");
    },
  });
  return (
    <form
      className={clsx("rounded-md border border-line bg-sunken p-2 text-xs", className)}
      onSubmit={(e) => {
        e.preventDefault();
        ask.mutate({ path: { docId }, body: { finding_id: findingId, reason: reason.trim(), standalone: true } });
      }}
    >
      <label className="block text-ink-2">
        Why this doc has no upstream doc
        <Textarea
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={2}
          placeholder="For example: an internal change with no product requirement."
          className="mt-1 font-sans text-xs"
          autoFocus
        />
        <span className="mt-1 block text-ink-3">
          At least {minReason} characters. It closes the finding at once and writes{" "}
          <code className="text-ink">standalone:</code> to the sidecar.
        </span>
      </label>
      {ask.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(ask.error)} />
        </div>
      ) : null}
      <div className="mt-2 flex justify-end gap-1.5">
        {onCancel ? (
          <Button size="sm" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
        <Button size="sm" type="submit" variant="primary" disabled={reason.trim().length < minReason || ask.isPending}>
          Mark it standalone
        </Button>
      </div>
    </form>
  );
}
