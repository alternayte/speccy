import { useMutation } from "@tanstack/react-query";
import { clsx } from "clsx";
import {
  ChevronRight,
  Copy,
  FilePlus2,
  FileText,
  Folder,
  Link2,
  MoreHorizontal,
  Pencil,
  Trash2,
  Upload,
} from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { Menu, MenuItem } from "@/components/ui/menu";
import { ErrorState } from "@/components/ui/states";
import type { BundleFile, SpecDoc } from "@/lib/api";
import { deleteFile, putFileContent, renameFile } from "@/lib/api";
import { type Dropped, filesFromDrop, keepBothName } from "./drop";
import { problemMessage } from "@/lib/problem";
import { buildTree, markdownLink, type TreeNode } from "./file-tree";
import { DocStateIcon } from "./verdict";

type Pending = { kind: "new" } | { kind: "rename"; path: string } | { kind: "delete"; path: string } | null;

// Explorer lists the files of a bundle and changes them (REQ-002). Every change creates a version.
// It marks each spec doc with its profile and its verdict; a click on another spec doc opens it.
export function Explorer({
  docId,
  baseVersion,
  files,
  specDocs = [],
  onOpenDoc,
  selected,
  onSelect,
  onChanged,
  readOnly = false,
}: {
  docId: string;
  baseVersion: string;
  files: BundleFile[];
  specDocs?: SpecDoc[];
  onOpenDoc?: (id: string) => void;
  selected: string;
  onSelect: (path: string) => void;
  onChanged: (select?: string) => void;
  readOnly?: boolean;
}) {
  const main = files.find((f) => f.is_main_doc)?.path;
  // The other spec docs of the bundle are not in this doc's version, so the tree adds them.
  const docAt = useMemo(() => new Map(specDocs.map((d) => [d.path, d])), [specDocs]);
  const tree = useMemo(
    () => buildTree([...new Set([...files.map((f) => f.path), ...specDocs.map((d) => d.path)])]),
    [files, specDocs],
  );
  // A carried file is in the bundle because a doc references it. Speccy renders it and never
  // writes it back, so the explorer offers no change to it.
  const carriedBy = new Map(files.filter((f) => f.carried_by).map((f) => [f.path, f.carried_by!]));
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [pending, setPending] = useState<Pending>(null);
  const [name, setName] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const upload = useRef<HTMLInputElement>(null);
  // over is true while a drag hangs over the explorer, and clash holds a drop that would replace
  // files the bundle already has.
  const [over, setOver] = useState(false);
  const [clash, setClash] = useState<{ items: Dropped[]; folder: string; taken: string[] } | null>(null);

  const change = useMutation({
    mutationFn: async (op: () => Promise<unknown>) => op(),
    onSuccess: () => setPending(null),
  });

  const run = (op: () => Promise<{ error?: unknown }>, select?: string) =>
    change.mutate(async () => {
      const res = await op();
      if (res.error) throw res.error;
      onChanged(select);
    });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!pending) return;
    if (pending.kind === "new") {
      const path = name.trim();
      run(
        () =>
          putFileContent({
            path: { docId },
            query: { path, base_version: baseVersion },
            body: new Blob([""]),
          }),
        path,
      );
    } else if (pending.kind === "rename") {
      const to = name.trim();
      run(
        () => renameFile({ path: { docId }, body: { from: pending.path, to, base_version: baseVersion } }),
        selected === pending.path ? to : undefined,
      );
    } else {
      run(() => deleteFile({ path: { docId }, query: { path: pending.path, base_version: baseVersion } }), main);
    }
  };

  // write puts each dropped file in the bundle. Each write builds on the version before it.
  const write = (items: Dropped[], folder: string, keepBoth: boolean) => {
    if (items.length === 0) return;
    const taken = new Set(files.map((f) => f.path));
    change.mutate(async () => {
      let base = baseVersion;
      let last = "";
      for (const item of items) {
        let path = folder + item.path;
        if (keepBoth && taken.has(path)) path = keepBothName(path, taken);
        taken.add(path);
        last = path;
        const res = await putFileContent({ path: { docId }, query: { path, base_version: base }, body: item.file });
        if (res.error) throw res.error;
        base = res.data!.version.id;
      }
      setClash(null);
      onChanged(last);
    });
  };

  const drop = async (e: React.DragEvent) => {
    e.preventDefault();
    setOver(false);
    if (readOnly) return;
    const items = await filesFromDrop(e.dataTransfer);
    if (items.length === 0) return;
    const folder = folderUnder(e.target as HTMLElement) ?? defaultFolder(selected);
    const have = new Set(files.map((f) => f.path));
    const taken = items.map((i) => folder + i.path).filter((p) => have.has(p));
    if (taken.length > 0) setClash({ items, folder, taken });
    else write(items, folder, false);
  };

  const uploadFiles = (list: FileList | null) => {
    // Copy now: the caller clears the input, which empties the live FileList.
    const picked = list ? Array.from(list) : [];
    if (picked.length === 0) return;
    const folder = selected.includes("/") ? selected.slice(0, selected.lastIndexOf("/") + 1) : "assets/";
    change.mutate(async () => {
      // Each upload builds on the version the previous one made.
      let base = baseVersion;
      let last = "";
      for (const f of picked) {
        last = folder + f.name;
        const res = await putFileContent({
          path: { docId },
          query: { path: last, base_version: base },
          body: f,
        });
        if (res.error) throw res.error;
        base = res.data!.version.id;
      }
      onChanged(last);
    });
  };

  const copy = async (path: string) => {
    await navigator.clipboard?.writeText(markdownLink(path));
    setCopied(path);
    setTimeout(() => setCopied(null), 1500);
  };

  const row = (n: TreeNode, depth: number): React.ReactNode => {
    const pad = { paddingLeft: `calc(var(--space-2) + ${depth} * var(--space-4))` };
    if (n.kind === "folder") {
      const isOpen = open[n.path] ?? true;
      return (
        <li key={n.path} data-folder={n.path}>
          <button
            type="button"
            aria-expanded={isOpen}
            onClick={() => setOpen((o) => ({ ...o, [n.path]: !isOpen }))}
            className="flex h-7 w-full items-center gap-1.5 pr-2 text-left text-sm text-ink-2 hover:bg-sunken"
            style={pad}
          >
            <ChevronRight aria-hidden className={clsx("size-3.5 transition-transform", isOpen && "rotate-90")} />
            <Folder aria-hidden className="size-3.5" />
            <span title={n.path || n.name} className="truncate">
              {n.name}
            </span>
          </button>
          {isOpen ? <ul>{n.children.map((c) => row(c, depth + 1))}</ul> : null}
        </li>
      );
    }
    const active = n.path === selected;
    const spec = docAt.get(n.path);
    const other = !!spec && spec.id !== docId;
    return (
      <li key={n.path} className="group relative">
        <button
          type="button"
          aria-current={active ? "true" : undefined}
          onClick={() => (other ? onOpenDoc?.(spec.id) : onSelect(n.path))}
          className={clsx(
            "flex h-7 w-full items-center gap-1.5 pr-8 text-left text-sm",
            active ? "bg-accent-soft text-ink" : "text-ink-2 hover:bg-sunken hover:text-ink",
          )}
          style={{ paddingLeft: `calc(var(--space-2) + ${depth} * var(--space-4) + var(--space-5))` }}
        >
          <FileText aria-hidden className="size-3.5 shrink-0" />
          <span title={n.path || n.name} className="truncate">
            {n.name}
          </span>
          {spec ? (
            <>
              <span className="shrink-0 rounded-sm border border-line px-1 font-mono text-2xs tracking-wide text-ink-2 uppercase">
                {spec.profile_key}
              </span>
              <DocStateIcon doc={spec} />
            </>
          ) : null}
          {carriedBy.has(n.path) ? (
            <Link2
              aria-label={`Carried: ${carriedBy.get(n.path)} references it`}
              className="size-3 shrink-0 text-ink-3"
            />
          ) : null}
          {copied === n.path ? <span className="ml-auto text-2xs text-accent">Copied</span> : null}
        </button>
        <div
          className={clsx(
            "absolute top-0.5 right-1 opacity-0 group-hover:opacity-100 focus-within:opacity-100",
            other && "hidden",
          )}
        >
          <Menu
            trigger={
              <button
                type="button"
                aria-label={`Actions for ${n.path}`}
                className="grid size-6 place-items-center rounded-sm text-ink-3 hover:bg-line hover:text-ink"
              >
                <MoreHorizontal className="size-3.5" />
              </button>
            }
          >
            <MenuItem icon={<Copy className="size-3.5" />} onSelect={() => copy(n.path)}>
              Copy markdown link
            </MenuItem>
            {readOnly || carriedBy.has(n.path) ? null : (
              <>
                <MenuItem
                  icon={<Pencil className="size-3.5" />}
                  onSelect={() => {
                    setName(n.path);
                    setPending({ kind: "rename", path: n.path });
                  }}
                >
                  Rename or move
                </MenuItem>
                <MenuItem
                  danger
                  icon={<Trash2 className="size-3.5" />}
                  onSelect={() => setPending({ kind: "delete", path: n.path })}
                >
                  Delete
                </MenuItem>
              </>
            )}
          </Menu>
        </div>
      </li>
    );
  };

  return (
    <nav
      aria-label="Bundle files"
      onDragOver={(e) => {
        if (readOnly) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false);
      }}
      onDrop={drop}
      className={clsx("flex h-full min-h-0 flex-col", over && "outline-2 -outline-offset-2 outline-accent")}
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-line px-2">
        <span className="px-1 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Files</span>
        <div className={clsx("ml-auto flex gap-0.5", readOnly && "hidden")}>
          <Button
            variant="ghost"
            size="sm"
            aria-label="New file"
            icon={<FilePlus2 className="size-3.5" />}
            onClick={() => {
              setName("assets/");
              setPending({ kind: "new" });
            }}
          />
          <Button
            variant="ghost"
            size="sm"
            aria-label="Upload files"
            icon={<Upload className="size-3.5" />}
            onClick={() => upload.current?.click()}
          />
          <input
            ref={upload}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              uploadFiles(e.target.files);
              e.target.value = "";
            }}
          />
        </div>
      </div>
      {change.isError && !pending ? (
        <div className="p-2">
          <ErrorState message={problemMessage(change.error)} />
        </div>
      ) : null}
      <ul className="min-h-0 flex-1 overflow-y-auto py-1">{tree.map((n) => row(n, 0))}</ul>

      <Dialog
        open={clash !== null}
        onOpenChange={(o) => {
          if (!o) setClash(null);
        }}
        title={`Replace ${clash?.taken.length ?? 0} file${clash?.taken.length === 1 ? "" : "s"}?`}
        description="The bundle already has these files. A replaced file lands in the next version, so the diff and the verdict show it."
      >
        <ul className="max-h-40 overflow-y-auto font-mono text-xs text-ink-2">
          {clash?.taken.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
        <div className="mt-3 flex justify-end gap-2">
          <Button size="sm" onClick={() => clash && write(clash.items, clash.folder, true)}>
            Keep both
          </Button>
          <Button size="sm" variant="primary" onClick={() => clash && write(clash.items, clash.folder, false)}>
            Replace
          </Button>
        </div>
      </Dialog>

      <Dialog
        open={pending !== null}
        onOpenChange={(o) => {
          if (!o) {
            setPending(null);
            change.reset();
          }
        }}
        title={pending?.kind === "new" ? "New file" : pending?.kind === "rename" ? "Rename or move" : "Delete file"}
        description={
          pending?.kind === "delete"
            ? `Delete ${pending.path}? Speccy keeps it in the earlier versions.`
            : "Use a path inside the bundle, with / between folders."
        }
      >
        <form onSubmit={submit} className="space-y-4">
          {pending?.kind !== "delete" ? (
            <div>
              <Label htmlFor="file-path">Path</Label>
              <Input id="file-path" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
            </div>
          ) : null}
          {change.isError ? <ErrorState message={problemMessage(change.error)} /> : null}
          <div className="flex justify-end gap-2">
            <Button onClick={() => setPending(null)}>Cancel</Button>
            <Button
              type="submit"
              variant={pending?.kind === "delete" ? "danger" : "primary"}
              disabled={change.isPending || (pending?.kind !== "delete" && name.trim() === "")}
            >
              {pending?.kind === "new" ? "Create" : pending?.kind === "rename" ? "Rename" : "Delete"}
            </Button>
          </div>
        </form>
      </Dialog>
    </nav>
  );
}

// folderUnder is the folder of the row the pointer is over, with its trailing slash.
function folderUnder(el: HTMLElement | null): string | null {
  const row = el?.closest<HTMLElement>("[data-folder]");
  if (!row) return null;
  const folder = row.dataset.folder ?? "";
  return folder === "" ? "" : `${folder}/`;
}

// defaultFolder is where a drop lands with no folder under the pointer: beside the open file, or
// assets/ for the main doc at the top of the bundle.
function defaultFolder(selected: string): string {
  return selected.includes("/") ? selected.slice(0, selected.lastIndexOf("/") + 1) : "assets/";
}
