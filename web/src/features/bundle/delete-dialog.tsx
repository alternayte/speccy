import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import {
  deleteBundleMutation,
  deleteBundlePlanOptions,
  deleteGithubSourceMutation,
  listBundlesOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// DeleteDialog does the right thing for the kind of source that makes this bundle. A bundle
// Speccy holds goes for good once the person types its slug. A bundle a GitHub source makes
// comes back on the next sync, so the dialog offers to remove the source instead. A bundle a
// folder on disk makes is the folder's to remove.
export function DeleteBundleDialog({
  bundleId,
  open,
  onOpenChange,
}: {
  bundleId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const qc = useQueryClient();
  const go = useNavigate();
  const [typed, setTyped] = useState("");
  const plan = useQuery({ ...deleteBundlePlanOptions({ path: { bundleId } }), enabled: open });
  const done = () => {
    void qc.invalidateQueries({ queryKey: listBundlesOptions().queryKey });
    onOpenChange(false);
    void go({ to: "/" });
  };
  const del = useMutation({ ...deleteBundleMutation(), onSuccess: done });
  const delSource = useMutation({ ...deleteGithubSourceMutation(), onSuccess: done });
  const p = plan.data;
  const error = del.error ?? delSource.error ?? plan.error;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setTyped("");
        onOpenChange(o);
      }}
      title="Delete this bundle"
      description={p?.message}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          {p?.kind === "db" ? (
            <Button
              variant="danger"
              disabled={typed !== p.slug || del.isPending}
              onClick={() => del.mutate({ path: { bundleId }, query: { slug: p.slug } })}
            >
              Delete for good
            </Button>
          ) : null}
          {p?.kind === "github" && p.source_id ? (
            <Button
              variant="danger"
              disabled={delSource.isPending}
              onClick={() => delSource.mutate({ path: { sourceId: p.source_id! } })}
            >
              Remove the source
            </Button>
          ) : null}
        </>
      }
    >
      {plan.isPending ? <Loading label="Reading the bundle" /> : null}
      {error ? <ErrorState message={problemMessage(error)} /> : null}
      {p?.kind === "db" ? (
        <label className="block text-sm">
          <span className="text-ink-2">
            Type <span className="font-mono text-ink">{p.slug}</span> to confirm.
          </span>
          <Input
            className="mt-1"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            aria-label="The bundle slug"
          />
        </label>
      ) : null}
    </Dialog>
  );
}
