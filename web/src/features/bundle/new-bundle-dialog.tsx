import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { useState } from "react";
import { useMe } from "@/features/account/me";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import { createBundleMutation, listBundlesQueryKey, listProfilesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// NewBundleDialog creates a bundle from a profile's template (REQ-016).
export function NewBundleDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const profiles = useQuery({ ...listProfilesOptions(), enabled: open });
  const [key, setKey] = useState("");
  const [title, setTitle] = useState("");
  const [name, setName] = useState("");
  const hosted = useMe().data?.mode === "hosted";
  const qc = useQueryClient();
  const navigate = useNavigate();
  const create = useMutation({
    ...createBundleMutation(),
    onSuccess: (b) => {
      qc.invalidateQueries({ queryKey: listBundlesQueryKey() });
      onOpenChange(false);
      navigate({ to: "/bundles/$bundleId/docs/$docId", params: { bundleId: b.bundle_id, docId: b.id } });
    },
  });
  const chosen = key || profiles.data?.items[0]?.key || "";
  const folder = name || slug(title);

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) create.reset();
        onOpenChange(o);
      }}
      title="New bundle"
      description="Speccy writes a new folder with the template of the doc type. Fill in each section."
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate({ body: { profile: chosen, name: folder, title: title.trim() || undefined } });
        }}
      >
        <fieldset className="min-w-0">
          <legend className="mb-1 block text-xs font-medium text-ink-2">Doc type</legend>
          {profiles.isPending ? (
            <Loading label="Loading doc types" />
          ) : profiles.isError ? (
            <ErrorState message={problemMessage(profiles.error)} />
          ) : (
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              {profiles.data.items.map((p) => (
                <label
                  key={p.key}
                  className={clsx(
                    "flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 transition-colors",
                    chosen === p.key ? "border-accent bg-accent-soft" : "border-line hover:bg-sunken",
                  )}
                >
                  <input
                    type="radio"
                    name="profile"
                    value={p.key}
                    checked={chosen === p.key}
                    onChange={() => setKey(p.key)}
                    className="mt-1 accent-[var(--accent)]"
                  />
                  <span className="min-w-0">
                    <span className="block text-sm font-medium text-ink">{p.name}</span>
                    <span className="block font-mono text-2xs text-ink-3 uppercase">{p.key}</span>
                  </span>
                </label>
              ))}
            </div>
          )}
        </fieldset>
        <div>
          <Label htmlFor="new-title">Title</Label>
          <Input
            id="new-title"
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Payment retries"
          />
        </div>
        <div>
          <Label htmlFor="new-name">{hosted ? "Name" : "Folder name"}</Label>
          <Input id="new-name" value={folder} onChange={(e) => setName(e.target.value)} placeholder="payment-retries" />
        </div>
        {/* Size lives in the doc's frontmatter, and the template writes size: feature. It is
            the one field a person sets without knowing it exists, so the dialog says what it
            does before they meet it. */}
        <p className="text-xs text-ink-2">
          The new doc starts at <code className="font-mono">size: feature</code>, for one change a team ships. Change it
          in the frontmatter to <code className="font-mono">app</code> for a system with parts that call each other, or{" "}
          <code className="font-mono">initiative</code> for work several systems share. Size decides which headings the
          template requires and which checks run.
        </p>
        {create.isError ? <ErrorState message={problemMessage(create.error)} /> : null}
        <div className="flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={!chosen || !folder || create.isPending}>
            {create.isPending ? "Creating" : "Create"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function slug(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
}
