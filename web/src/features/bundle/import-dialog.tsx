import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import {
  guessProfileMutation,
  importBundleMutation,
  listBundlesQueryKey,
  listProfilesOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

type Mode = "file" | "text";

export function ImportDialog({
  open,
  onOpenChange,
  dropped,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // dropped is a file a drop could not place, so the dialog opens with it in hand.
  dropped?: File | null;
}) {
  const [mode, setMode] = useState<Mode>("file");
  const [file, setFile] = useState<File | null>(null);
  const profiles = useQuery({ ...listProfilesOptions(), enabled: open });
  // profile is the doc type Speccy writes into a file that names none.
  const [profileKey, setProfileKey] = useState("");
  const [guessed, setGuessed] = useState<string>();
  const guess = useMutation({
    ...guessProfileMutation(),
    onSuccess: (g) => {
      setGuessed(g.profile);
      if (g.profile) setProfileKey(g.profile);
    },
  });
  const ask = guess.mutate;
  const look = useCallback(
    (text: string) => {
      if (!text.trim()) return;
      ask({ body: { text } });
    },
    [ask],
  );
  useEffect(() => {
    if (!open) return;
    if (dropped) {
      setMode("file");
      setFile(dropped);
      void dropped.text().then(look);
    }
  }, [open, dropped, look]);
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
        ...(profileKey ? { profile: profileKey } : {}),
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
              onChange={(e) => {
                const f = e.target.files?.[0] ?? null;
                setFile(f);
                setGuessed(undefined);
                setProfileKey("");
                if (f && /\.(md|markdown)$/i.test(f.name)) void f.text().then(look);
              }}
              className="block w-full text-sm text-ink-2 file:mr-3 file:rounded-md file:border file:border-line-strong file:bg-surface file:px-3 file:py-1.5 file:text-sm file:text-ink"
            />
            <p className="mt-1 text-xs text-ink-3">
              A file that names no <code>type</code> gets the one you pick below.
            </p>
          </div>
        ) : (
          <div>
            <Label htmlFor="import-text">Markdown</Label>
            <Textarea
              id="import-text"
              rows={9}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onBlur={(e) => look(e.target.value)}
            />
          </div>
        )}

        <div>
          <Label htmlFor="import-type">Doc type</Label>
          <select
            id="import-type"
            value={profileKey}
            onChange={(e) => setProfileKey(e.target.value)}
            className="mt-1 block h-8 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-ink"
          >
            <option value="">The type in the file</option>
            {(profiles.data?.items ?? []).map((p) => (
              <option key={p.key} value={p.key}>
                {p.name}
              </option>
            ))}
          </select>
          <p className="mt-1 text-xs text-ink-3">
            {profileKey
              ? guessed === profileKey
                ? `Speccy guessed ${profileKey.toUpperCase()} from the headings. It adds the type line to the top of the file.`
                : `Speccy adds the ${profileKey.toUpperCase()} type line to the top of the file.`
              : "Speccy reads the type from the file's frontmatter."}
          </p>
        </div>

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
