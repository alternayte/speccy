import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import { importBundleMutation, listBundlesQueryKey } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

type Mode = "file" | "text";

export function ImportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [mode, setMode] = useState<Mode>("file");
  const [file, setFile] = useState<File | null>(null);
  const [text, setText] = useState("---\ntype: prd\ntitle: \n---\n\n# \n");
  const [name, setName] = useState("");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const importing = useMutation({
    ...importBundleMutation(),
    onSuccess: (b) => {
      qc.invalidateQueries({ queryKey: listBundlesQueryKey() });
      onOpenChange(false);
      navigate({ to: "/bundles/$bundleId", params: { bundleId: b.id } });
    },
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    importing.mutate({
      body: {
        ...(name ? { name } : {}),
        ...(mode === "file" && file ? { file } : {}),
        ...(mode === "text" ? { text } : {}),
      },
    });
  };
  const ready = mode === "file" ? file !== null : text.trim() !== "";

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) importing.reset();
        onOpenChange(o);
      }}
      title="Import a bundle"
      description="Speccy writes the bundle to a new folder. Commit the folder yourself."
    >
      <form onSubmit={submit} className="space-y-4">
        <div role="tablist" aria-label="Import from" className="inline-flex rounded-md border border-line p-0.5">
          {(["file", "text"] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="tab"
              aria-selected={mode === m}
              onClick={() => setMode(m)}
              className={clsx(
                "rounded-sm px-3 py-1 text-xs font-medium",
                mode === m ? "bg-sunken text-ink" : "text-ink-2 hover:text-ink",
              )}
            >
              {m === "file" ? "A .md or .zip file" : "Pasted text"}
            </button>
          ))}
        </div>

        {mode === "file" ? (
          <div>
            <Label htmlFor="import-file">File</Label>
            <input
              id="import-file"
              type="file"
              accept=".md,.markdown,.zip"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
              className="block w-full text-sm text-ink-2 file:mr-3 file:rounded-md file:border file:border-line-strong file:bg-surface file:px-3 file:py-1.5 file:text-sm file:text-ink"
            />
            <p className="mt-1 text-xs text-ink-3">
              The main doc needs a <code>type</code> field in its frontmatter.
            </p>
          </div>
        ) : (
          <div>
            <Label htmlFor="import-text">Markdown</Label>
            <Textarea id="import-text" rows={9} value={text} onChange={(e) => setText(e.target.value)} />
          </div>
        )}

        <div>
          <Label htmlFor="import-name">Folder name (optional)</Label>
          <Input
            id="import-name"
            placeholder="From the file name or the title"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </div>

        {importing.isError ? <ErrorState message={problemMessage(importing.error)} /> : null}

        <div className="flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={!ready || importing.isPending}>
            {importing.isPending ? "Importing" : "Import"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
