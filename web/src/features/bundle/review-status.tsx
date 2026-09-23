import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { BadgeCheck, UserPlus } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ErrorState, Loading } from "@/components/ui/states";
import type { ReviewStatus as Status } from "@/lib/api";
import {
  approveBundleMutation,
  getSpecDocOptions,
  getBundleStatusOptions,
  getBundleStatusQueryKey,
  listPeopleOptions,
  requestReviewMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

const statusStyle: Record<Status, { label: string; tone: string }> = {
  draft: { label: "Draft", tone: "border-line-strong text-ink-2" },
  in_review: { label: "In review", tone: "border-warn/50 text-warn" },
  approved: { label: "Approved", tone: "border-ok/50 text-ok" },
  superseded: { label: "Superseded", tone: "border-line text-ink-3" },
};

// ReviewStatus shows the bundle's status (§9.5) and the review actions: an author requests a
// review with reviewers (REQ-090); a reviewer approves (REQ-076).
export function ReviewStatus({
  docId,
  signedIn,
  register,
}: {
  docId: string;
  signedIn: boolean;
  // register hands the request-review opener to the control row (SDD §13.4).
  register?: (open: () => void) => void;
}) {
  const qc = useQueryClient();
  const status = useQuery({ ...getBundleStatusOptions({ path: { docId } }), refetchInterval: 5000 });
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    register?.(() => setAsking(true));
  }, [register]);
  const done = (s: unknown) => {
    qc.setQueryData(getBundleStatusQueryKey({ path: { docId } }), s);
    qc.invalidateQueries({ queryKey: getSpecDocOptions({ path: { docId } }).queryKey });
  };
  const approve = useMutation({ ...approveBundleMutation(), onSuccess: done });
  if (!status.data) return null;
  const s = status.data;
  const st = statusStyle[s.status];
  const approvedNow = s.approvals.length;
  return (
    <>
      <span
        className={clsx("inline-flex h-6 items-center rounded-full border px-2 text-2xs font-semibold", st.tone)}
        title={
          s.status === "in_review" || s.status === "approved"
            ? `${approvedNow} of ${s.required} approval${s.required === 1 ? "" : "s"}`
            : undefined
        }
      >
        {st.label}
        {s.status === "in_review" ? ` · ${approvedNow}/${s.required}` : ""}
      </span>
      {signedIn && s.can_request && !register ? (
        <Button size="sm" icon={<UserPlus className="size-3.5" />} onClick={() => setAsking(true)}>
          <span className="hidden sm:inline">{s.status === "draft" ? "Request review" : "Reviewers"}</span>
        </Button>
      ) : null}
      {signedIn && s.status === "in_review" && !s.can_request ? (
        <Button
          size="sm"
          variant="secondary"
          icon={<BadgeCheck className="size-3.5" />}
          disabled={!s.can_approve || approve.isPending}
          title={s.approve_blocked_by}
          onClick={() => approve.mutate({ path: { docId } })}
        >
          <span className="hidden sm:inline">Approve</span>
        </Button>
      ) : null}
      {approve.isError ? <span className="text-xs text-bad">{problemMessage(approve.error)}</span> : null}
      <RequestDialog
        open={asking}
        docId={docId}
        current={s.reviewers}
        draft={s.status === "draft"}
        onClose={() => setAsking(false)}
        onDone={(x) => {
          done(x);
          setAsking(false);
        }}
      />
    </>
  );
}

function RequestDialog({
  open,
  docId,
  current,
  draft,
  onClose,
  onDone,
}: {
  open: boolean;
  docId: string;
  current: string[];
  draft: boolean;
  onClose: () => void;
  onDone: (s: unknown) => void;
}) {
  const people = useQuery({ ...listPeopleOptions(), enabled: open });
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const request = useMutation({ ...requestReviewMutation(), onSuccess: onDone });
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      title={draft ? "Request a review" : "Add reviewers"}
      description="Reviewers see the bundle in their inbox. Approval needs a current Build Ready verdict."
    >
      {people.isPending ? (
        <Loading label="Loading people" />
      ) : people.isError ? (
        <ErrorState message={problemMessage(people.error)} />
      ) : (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            request.mutate({ path: { docId }, body: { reviewers: [...picked] } });
          }}
        >
          <ul className="max-h-64 space-y-1 overflow-y-auto">
            {people.data.items.map((p) => {
              const already = current.includes(p.id);
              return (
                <li key={p.id}>
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      className="accent-[var(--color-accent)]"
                      disabled={already}
                      checked={already || picked.has(p.id)}
                      onChange={(e) => {
                        const next = new Set(picked);
                        if (e.target.checked) next.add(p.id);
                        else next.delete(p.id);
                        setPicked(next);
                      }}
                    />
                    <span>{p.name}</span>
                    {p.email ? <span className="text-xs text-ink-3">{p.email}</span> : null}
                    {already ? <span className="text-xs text-ink-3">reviewer</span> : null}
                  </label>
                </li>
              );
            })}
          </ul>
          {request.isError ? <ErrorState message={problemMessage(request.error)} /> : null}
          <div className="flex justify-end gap-2">
            <Button type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={request.isPending || (!draft && picked.size === 0)}>
              {draft ? "Request the review" : "Add"}
            </Button>
          </div>
        </form>
      )}
    </Dialog>
  );
}
