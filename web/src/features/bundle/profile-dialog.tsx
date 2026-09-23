import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { SpecDoc } from "@/lib/api";
import { listProfilesOptions, setBundleProfileMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// ProfileDialog changes the doc type of a bundle's main doc. A doc Speccy owns takes the type
// in its frontmatter; a doc in a repo keeps the type in Speccy, and the repo takes no commit.
export function ProfileDialog({
  bundle,
  open,
  onOpenChange,
}: {
  bundle: SpecDoc;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const profiles = useQuery({ ...listProfilesOptions(), enabled: open });
  const [key, setKey] = useState(bundle.profile_key);
  const qc = useQueryClient();
  const save = useMutation({
    ...setBundleProfileMutation(),
    onSuccess: () => {
      qc.invalidateQueries();
      onOpenChange(false);
    },
  });
  const repo = bundle.source_kind === "github";
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) save.reset();
        onOpenChange(o);
      }}
      title="Change doc type"
      description={
        repo
          ? "Speccy keeps the type for this doc. The repo takes no commit."
          : "Speccy writes the type into the doc's frontmatter as a new version."
      }
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate({ path: { docId: bundle.id }, body: { profile: key } });
        }}
      >
        <div>
          <Label htmlFor="doc-type">Doc type</Label>
          <select
            id="doc-type"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            className="mt-1 block h-8 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-ink"
          >
            {(profiles.data?.items ?? []).map((p) => (
              <option key={p.key} value={p.key}>
                {p.name}
              </option>
            ))}
          </select>
          <p className="mt-1 text-xs text-ink-3">The next review uses the checks of this profile.</p>
        </div>
        {save.isError ? <ErrorState message={problemMessage(save.error)} /> : null}
        <div className="flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={key === bundle.profile_key || save.isPending}>
            {save.isPending ? "Saving" : "Change"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
