import { useMutation, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/states";
import { withdrawAcknowledgementMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// Withdraw takes one Acknowledgement out of a spec doc's sidecar: a trace entry, or standalone.
// It takes effect at once, with no approval, because it only makes the verdict stricter. The
// caller shows it only to a person who can edit that doc; the server checks it again.
export function Withdraw({
  docId,
  docTitle,
  traceId,
  onDone,
  className,
}: {
  docId: string;
  // docTitle names the spec doc whose sidecar changes, where more than one doc is on screen.
  docTitle?: string;
  // traceId names the trace entry. Without it, Withdraw takes out the standalone entry.
  traceId?: string;
  onDone: (message: string) => void;
  className?: string;
}) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const withdraw = useMutation({
    ...withdrawAcknowledgementMutation(),
    onSuccess: (r) => {
      setOpen(false);
      const where = r.version ? ` as version ${r.version.number}` : "";
      onDone(
        traceId
          ? `Withdrew the acknowledgement of ${traceId}${where}. It is a gap again.`
          : `Withdrew the standalone acknowledgement${where}. links.has-upstream applies again.`,
      );
      qc.invalidateQueries();
    },
  });
  const title = traceId ? `Withdraw the acknowledgement of ${traceId}?` : "Withdraw the standalone acknowledgement?";
  const sidecar = docTitle ? `the sidecar of ${docTitle}` : "the sidecar";
  const effect = traceId
    ? `Speccy takes ${traceId} out of ${sidecar} now, with no approval. ${traceId} is a gap again, and the verdict counts it until someone answers it again.`
    : "Speccy takes standalone: out of the sidecar now, with no approval. links.has-upstream fails again until the doc links its upstream doc, or a new standalone acknowledgement is approved.";
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className={clsx("text-xs text-ink-3 hover:text-bad", className)}
      >
        Withdraw
      </button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          if (!o) withdraw.reset();
          setOpen(o);
        }}
        title={title}
        description={effect}
        footer={
          <>
            <Button onClick={() => setOpen(false)}>Cancel</Button>
            <Button
              variant="danger"
              disabled={withdraw.isPending}
              onClick={() =>
                withdraw.mutate({
                  path: { docId },
                  body: traceId ? { kind: "trace", trace_id: traceId } : { kind: "standalone" },
                })
              }
            >
              {withdraw.isPending ? "Withdrawing" : "Withdraw"}
            </Button>
          </>
        }
      >
        {withdraw.isError ? <ErrorState message={problemMessage(withdraw.error)} /> : null}
      </Dialog>
    </>
  );
}
