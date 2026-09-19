import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Share2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ErrorState, Loading } from "@/components/ui/states";
import { OneTimeValue } from "@/features/account/account-page";
import type { Visibility } from "@/lib/api";
import {
  createShareLinkMutation,
  getBundleAccessOptions,
  getBundleAccessQueryKey,
  revokeShareLinkMutation,
  setVisibilityMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

const visibilities: { value: Visibility; label: string; help: string }[] = [
  { value: "private", label: "Private", help: "The authors, the named reviewers, and admins." },
  { value: "internal", label: "Internal", help: "Every member of the workspace." },
  { value: "link", label: "Link", help: "Every member, and anyone with the share link as a guest." },
];

// ShareDialog sets who can see a bundle (REQ-084) and makes or revokes its share link (REQ-085).
export function ShareDialog({ bundleId }: { bundleId: string }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [url, setUrl] = useState<string>();
  const access = useQuery({ ...getBundleAccessOptions({ path: { bundleId } }), enabled: open });
  const key = getBundleAccessQueryKey({ path: { bundleId } });
  const set = (data: unknown) => qc.setQueryData(key, data);
  const visibility = useMutation({ ...setVisibilityMutation(), onSuccess: set });
  const create = useMutation({
    ...createShareLinkMutation(),
    onSuccess: (res) => {
      setUrl(res.url);
      set(res.access);
    },
  });
  const revoke = useMutation({
    ...revokeShareLinkMutation(),
    onSuccess: (a) => {
      setUrl(undefined);
      set(a);
    },
  });
  const error = visibility.error ?? create.error ?? revoke.error;
  return (
    <>
      <Button size="sm" icon={<Share2 className="size-3.5" />} onClick={() => setOpen(true)}>
        <span className="hidden sm:inline">Share</span>
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setUrl(undefined);
        }}
        title="Share"
        description="Choose who can see this bundle. Only authors and admins can change this."
      >
        {access.isPending ? (
          <Loading label="Loading the access" />
        ) : access.isError ? (
          <ErrorState message={problemMessage(access.error)} />
        ) : (
          <div className="space-y-4">
            <fieldset className="space-y-2">
              <legend className="sr-only">Visibility</legend>
              {visibilities.map((v) => (
                <label key={v.value} className="flex cursor-pointer items-start gap-2.5 text-sm">
                  <input
                    type="radio"
                    name="visibility"
                    className="mt-1 accent-[var(--color-accent)]"
                    checked={access.data.visibility === v.value}
                    disabled={!access.data.can_edit || visibility.isPending}
                    onChange={() => visibility.mutate({ path: { bundleId }, body: { visibility: v.value } })}
                  />
                  <span>
                    <span className="font-medium">{v.label}</span>
                    <span className="block text-xs text-ink-2">{v.help}</span>
                  </span>
                </label>
              ))}
            </fieldset>
            <div className="border-t border-line pt-4">
              <p className="text-sm font-medium">Share link</p>
              <p className="mt-0.5 text-xs text-ink-2">
                {access.data.share_active
                  ? `A share link is active${access.data.share_expires_at ? ` until ${new Date(access.data.share_expires_at).toLocaleDateString()}` : ""}. Guests can read the spec. They cannot edit it or ask the AI.`
                  : "No share link is active."}
              </p>
              {url ? <OneTimeValue label="Copy the link now. Speccy shows it one time." value={url} /> : null}
              {access.data.can_edit ? (
                <div className="mt-3 flex gap-2">
                  <Button
                    size="sm"
                    variant="primary"
                    onClick={() => create.mutate({ path: { bundleId }, body: {} })}
                    disabled={create.isPending}
                  >
                    {access.data.share_active ? "Make a new link" : "Make a share link"}
                  </Button>
                  {access.data.share_active ? (
                    <Button size="sm" onClick={() => revoke.mutate({ path: { bundleId } })} disabled={revoke.isPending}>
                      Revoke the link
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </div>
            {error ? <ErrorState message={problemMessage(error)} /> : null}
          </div>
        )}
      </Dialog>
    </>
  );
}
