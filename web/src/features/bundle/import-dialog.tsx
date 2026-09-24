import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { BundleList, ImportDoc, SuggestedLink } from "@/lib/api";
import {
  importBundleMutation,
  listBundlesQueryKey,
  listProfilesOptions,
  previewImportMutation,
  suggestLinksMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import type { DroppedBundle } from "./drop";

type Mode = "file" | "text";

// The upload the dialog works on: one file, the files of a dropped folder, or pasted text.
type Upload = { file: File } | { files: File[] } | { text: string };

// ImportDialog lists every markdown file of an import with a profile picker, prefilled with the
// type the file names or the profile its headings fit. Each file given a profile becomes a spec
// doc, and each folder becomes one bundle. The links Speccy offers between them are prechecked,
// and never made without a person.
export function ImportDialog({
  open,
  onOpenChange,
  dropped,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // dropped is a folder or a file a drop could not import alone, so the dialog opens with it.
  dropped?: DroppedBundle | null;
}) {
  const [mode, setMode] = useState<Mode>("file");
  const [upload, setUpload] = useState<Upload | null>(null);
  const [text, setText] = useState("---\ntype: prd\ntitle: \n---\n\n# \n");
  const [name, setName] = useState("");
  const profiles = useQuery({ ...listProfilesOptions(), enabled: open });
  const [docs, setDocs] = useState<ImportDoc[]>([]);
  const [picked, setPicked] = useState<Record<string, string>>({});
  const [links, setLinks] = useState<SuggestedLink[]>([]);
  const [unchecked, setUnchecked] = useState<Record<string, boolean>>({});
  const preview = useMutation({
    ...previewImportMutation(),
    onSuccess: (p) => {
      setDocs(p.docs);
      setPicked(Object.fromEntries(p.docs.map((d) => [d.path, d.type ?? d.guess ?? ""])));
      setLinks(p.links);
      setUnchecked({});
    },
  });
  const suggest = useMutation({ ...suggestLinksMutation(), onSuccess: (s) => setLinks(s.items) });
  const qc = useQueryClient();
  const navigate = useNavigate();
  // made holds an import the dialog does not leave at once: one with problems, or one that made
  // more than one bundle, so the person sees what the import made (#66).
  const [made, setMade] = useState<BundleList | null>(null);
  const openBundle = (id: string) => {
    setMade(null);
    onOpenChange(false);
    navigate({ to: "/bundles/$bundleId", params: { bundleId: id } });
  };
  const importing = useMutation({
    ...importBundleMutation(),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: listBundlesQueryKey() });
      const first = res.items[0];
      if (res.items.length === 1 && first && res.problems.length === 0) openBundle(first.id);
      else setMade(res);
    },
  });

  const look = (u: Upload) => {
    setUpload(u);
    setDocs([]);
    setLinks([]);
    importing.reset();
    preview.mutate({ body: body(u) });
  };
  useEffect(() => {
    if (!open || !dropped) return;
    setMode("file");
    setName(dropped.name);
    look({ files: dropped.files.map((d) => new File([d.file], d.path, { type: d.file.type })) });
    // look is stable in effect: it only starts a preview.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, dropped]);

  const pick = (path: string, key: string) => {
    const next = { ...picked, [path]: key };
    setPicked(next);
    suggest.mutate({ body: { docs: Object.entries(next).map(([p, profile]) => ({ path: p, profile })) } });
  };
  const linkKey = (l: SuggestedLink) => `${l.from}>${l.to}`;
  const specs = docs.filter((d) => picked[d.path]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!upload) return;
    importing.mutate({
      body: {
        ...body(upload),
        ...(name ? { name } : {}),
        docs: JSON.stringify(docs.map((d) => ({ path: d.path, profile: picked[d.path] ?? "" }))),
        links: JSON.stringify(links.filter((l) => !unchecked[linkKey(l)])),
      },
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          importing.reset();
          setMade(null);
          setUpload(null);
          setDocs([]);
        }
        onOpenChange(o);
      }}
      title="Import"
      description="The markdown files you give a doc type become the spec docs of one bundle per folder."
    >
      <form onSubmit={submit} className="space-y-4">
        {!dropped ? (
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
        ) : null}

        {dropped ? null : mode === "file" ? (
          <div>
            <Label htmlFor="import-file">File</Label>
            <input
              id="import-file"
              type="file"
              accept=".md,.markdown,.zip"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) look({ file: f });
              }}
              className="block w-full text-sm text-ink-2 file:mr-3 file:rounded-md file:border file:border-line-strong file:bg-surface file:px-3 file:py-1.5 file:text-sm file:text-ink"
            />
          </div>
        ) : (
          <div>
            <Label htmlFor="import-text">Markdown</Label>
            <Textarea
              id="import-text"
              rows={9}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onBlur={(e) => e.target.value.trim() && look({ text: e.target.value })}
            />
          </div>
        )}

        {preview.isPending ? <p className="text-xs text-ink-3">Reading the files</p> : null}
        {preview.isError ? <ErrorState message={problemMessage(preview.error)} /> : null}

        {docs.length > 0 ? (
          <fieldset>
            <legend className="text-sm font-medium text-ink">Doc types</legend>
            <ul className="mt-1.5 divide-y divide-line rounded-md border border-line">
              {docs.map((d) => (
                <li key={d.path} className="flex flex-wrap items-center gap-2 px-3 py-2">
                  <span
                    title={d.path}
                    className="w-full min-w-0 truncate font-mono text-xs text-ink sm:w-auto sm:flex-1"
                  >
                    {d.path}
                  </span>
                  <select
                    aria-label={`Doc type for ${d.path}`}
                    value={picked[d.path] ?? ""}
                    onChange={(e) => pick(d.path, e.target.value)}
                    className="h-7 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
                  >
                    <option value="">Not a spec</option>
                    {(profiles.data?.items ?? []).map((p) => (
                      <option key={p.key} value={p.key}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                  <span className="w-full text-2xs text-ink-3">
                    {d.type
                      ? `The file names ${d.type}.`
                      : d.guess
                        ? `Speccy guessed ${d.guess} from the headings, and writes the type line into the file.`
                        : "No profile fits its headings. Pick one, or leave it out."}
                  </span>
                </li>
              ))}
            </ul>
          </fieldset>
        ) : null}

        {links.length > 0 ? (
          <fieldset>
            <legend className="text-sm font-medium text-ink">Links</legend>
            {links.map((l) => (
              <label key={linkKey(l)} className="mt-1.5 flex items-center gap-2 text-sm text-ink-2">
                <input
                  type="checkbox"
                  checked={!unchecked[linkKey(l)]}
                  onChange={(e) => setUnchecked({ ...unchecked, [linkKey(l)]: !e.target.checked })}
                />
                <span>
                  <code>{l.from}</code> {l.kind} <code>{l.to}</code>
                </span>
              </label>
            ))}
            <p className="mt-1 text-2xs text-ink-3">Speccy writes the link into the frontmatter of the first doc.</p>
          </fieldset>
        ) : null}

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
        {made ? (
          <div role="status" className="space-y-2 rounded-md border border-line bg-sunken px-3 py-2 text-sm">
            <p className="text-ink">
              Imported {made.items.length} bundle{made.items.length === 1 ? "" : "s"}.
            </p>
            <ul className="space-y-1">
              {made.items.map((b) => (
                <li key={b.id}>
                  <button type="button" className="text-accent hover:underline" onClick={() => openBundle(b.id)}>
                    {b.title}
                  </button>
                  <span className="text-ink-3">
                    {" "}
                    · {b.docs.length} spec doc{b.docs.length === 1 ? "" : "s"}
                  </span>
                </li>
              ))}
            </ul>
            {made.problems.length > 0 ? (
              <ul className="space-y-1 text-xs">
                {made.problems.map((p) => (
                  <li key={p.path + p.message}>
                    <span className="font-mono text-ink-2">{p.path}</span>{" "}
                    <span className="text-warn">{p.message}</span>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}

        <div className="flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)}>{made ? "Close" : "Cancel"}</Button>
          {made ? null : (
            <Button type="submit" variant="primary" disabled={specs.length === 0 || importing.isPending}>
              {importing.isPending ? "Importing" : specs.length > 1 ? `Import ${specs.length} spec docs` : "Import"}
            </Button>
          )}
        </div>
      </form>
    </Dialog>
  );
}

function body(u: Upload) {
  if ("file" in u) return { file: u.file };
  if ("files" in u) return { files: u.files };
  return { text: u.text };
}
