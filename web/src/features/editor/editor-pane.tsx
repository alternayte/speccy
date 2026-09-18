import { EditorView } from "@codemirror/view";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { Columns2, Eye, FileCode2, Save } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ErrorState, Loading } from "@/components/ui/states";
import { getFileContent, putFileContent } from "@/lib/api";
import { problemCode, problemMessage } from "@/lib/problem";
import { CodeEditor } from "./code-editor";
import { Preview } from "./preview";
import { syncEditor, syncPreview } from "./scroll-sync";

export type View = "code" | "preview" | "split";

const imageExt = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "avif"]);

export function isMarkdown(path: string) {
  return /\.(md|markdown)$/i.test(path);
}

export function contentUrl(bundleId: string, path: string, version?: string) {
  const q = new URLSearchParams({ path });
  if (version) q.set("version", version);
  return `/api/v1/bundles/${bundleId}/files/content?${q}`;
}

type Loaded = { kind: "text"; text: string } | { kind: "binary" };

async function loadFile(bundleId: string, path: string, version: string): Promise<Loaded> {
  const res = await getFileContent({
    path: { bundleId },
    query: { path, version },
    parseAs: "blob",
    throwOnError: true,
  });
  const bytes = new Uint8Array(await (res.data as Blob).arrayBuffer());
  try {
    return { kind: "text", text: new TextDecoder("utf-8", { fatal: true }).decode(bytes) };
  } catch {
    return { kind: "binary" };
  }
}

export function EditorPane({
  bundleId,
  path,
  version,
  sha,
  view,
  onViewChange,
  onOpenPath,
  onSaved,
  onDirtyChange,
  focus,
}: {
  bundleId: string;
  path: string;
  // The current version of the bundle, and the hash of this file in it.
  version: { id: string; number: number };
  sha: string | undefined;
  view: View;
  onViewChange: (v: View) => void;
  onOpenPath: (path: string) => void;
  onSaved: () => void;
  onDirtyChange: (dirty: boolean) => void;
  // focus asks the pane to show a byte range of the file; seq changes on every request.
  focus?: { start: number; end: number; seq: number };
}) {
  const qc = useQueryClient();
  // loadVersion is the version the editor text came from. base is the version a save builds on.
  const [loadVersion, setLoadVersion] = useState(version);
  const [base, setBase] = useState(version);
  const [knownSha, setKnownSha] = useState(sha);
  const [saved, setSaved] = useState<string | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const editorView = useRef<EditorView | null>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const syncing = useRef<"editor" | "preview" | null>(null);
  const wide = useWide();

  const file = useQuery({
    queryKey: ["file", bundleId, path, loadVersion.id],
    queryFn: () => loadFile(bundleId, path, loadVersion.id),
    staleTime: Infinity,
  });

  useEffect(() => {
    if (file.data?.kind === "text") {
      setSaved(file.data.text);
      setText(file.data.text);
      setBase(loadVersion);
      setStale(false);
    }
  }, [file.data, loadVersion]);

  const dirty = text !== null && saved !== null && text !== saved;
  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  // A new bundle version: a change on disk, or a save in another pane.
  useEffect(() => {
    if (version.number <= base.number) {
      if (version.id === base.id && sha) setKnownSha(sha);
      return;
    }
    if (sha !== undefined && sha === knownSha) {
      setBase(version); // this file did not change
    } else if (!dirty) {
      setKnownSha(sha);
      setLoadVersion(version);
    } else {
      setStale(true);
    }
  }, [version, sha, base, knownSha, dirty]);

  const save = useMutation({
    mutationFn: async (body: string) => {
      const res = await putFileContent({
        path: { bundleId },
        query: { path, base_version: base.id },
        body: new Blob([body]),
        throwOnError: true,
      });
      return { res: res.data, body };
    },
    onSuccess: ({ res, body }) => {
      setSaved(body);
      setBase({ id: res.version.id, number: res.version.number });
      setKnownSha(undefined);
      qc.setQueryData(["file", bundleId, path, res.version.id], { kind: "text", text: body });
      onSaved();
    },
  });

  const doSave = useCallback(() => {
    if (text !== null && text !== saved && !save.isPending) save.mutate(text);
  }, [text, saved, save]);

  const reload = () => {
    save.reset();
    setStale(false);
    setKnownSha(sha);
    setLoadVersion({ ...version });
    qc.invalidateQueries({ queryKey: ["file", bundleId, path, version.id] });
  };

  const onEditorScroll = useCallback(() => {
    if (view !== "split" || syncing.current === "preview" || !editorView.current || !previewRef.current) return;
    syncing.current = "editor";
    syncPreview(editorView.current, previewRef.current);
    requestAnimationFrame(() => (syncing.current = null));
  }, [view]);

  const onPreviewScroll = useCallback(() => {
    if (view !== "split" || syncing.current === "editor" || !editorView.current || !previewRef.current) return;
    syncing.current = "preview";
    syncEditor(previewRef.current, editorView.current);
    requestAnimationFrame(() => (syncing.current = null));
  }, [view]);

  const attachView = useCallback(
    (v: EditorView | null) => {
      editorView.current?.scrollDOM.removeEventListener("scroll", onEditorScroll);
      editorView.current = v;
      v?.scrollDOM.addEventListener("scroll", onEditorScroll, { passive: true });
    },
    [onEditorScroll],
  );

  // Show the requested range: select it in the editor, and scroll the preview to its block.
  useEffect(() => {
    if (!focus || file.data?.kind !== "text") return;
    const text = file.data.text;
    const timer = setTimeout(() => {
      const view = editorView.current;
      if (view) {
        const from = Math.min(byteToIndex(text, focus.start), view.state.doc.length);
        const to = Math.min(byteToIndex(text, focus.end), view.state.doc.length);
        view.dispatch({
          selection: { anchor: from, head: to },
          effects: EditorView.scrollIntoView(from, { y: "center" }),
        });
        view.focus();
      }
      const preview = previewRef.current;
      if (preview) {
        let target: HTMLElement | null = null;
        preview.querySelectorAll<HTMLElement>("article [data-src-start]").forEach((el) => {
          if (Number(el.dataset.srcStart) <= focus.start && focus.start < Number(el.dataset.srcEnd)) target = el;
        });
        const el = target as HTMLElement | null;
        if (el) {
          el.scrollIntoView({ block: "center", behavior: "smooth" });
          el.classList.remove("flash");
          void el.offsetWidth; // restart the animation
          el.classList.add("flash");
        }
      }
    }, 80);
    return () => clearTimeout(timer);
  }, [focus, file.data]);

  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  if (imageExt.has(ext)) {
    return (
      <Frame path={path}>
        <div className="flex h-full items-center justify-center overflow-auto bg-sunken p-6">
          <img src={contentUrl(bundleId, path, version.id)} alt={path} className="max-h-full max-w-full" />
        </div>
      </Frame>
    );
  }
  if (file.isPending) return <Loading label={`Loading ${path}`} />;
  if (file.isError)
    return (
      <div className="p-4">
        <ErrorState message={problemMessage(file.error)} />
      </div>
    );
  if (file.data.kind === "binary")
    return (
      <Frame path={path}>
        <div className="p-6 text-sm text-ink-2">
          This file is not text.{" "}
          <a className="text-accent underline" href={contentUrl(bundleId, path, version.id)} download>
            Download it
          </a>
          .
        </div>
      </Frame>
    );

  const md = isMarkdown(path);
  // Split needs two columns. A narrow screen shows the code instead.
  const shown: View = !md ? "code" : view === "split" && !wide ? "code" : view;
  const conflict = save.isError && problemCode(save.error) === "version_conflict";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="no-print flex h-10 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <span className="min-w-0 truncate font-mono text-xs text-ink-2">{path}</span>
        {dirty ? (
          <span className="text-xs text-warn" aria-live="polite">
            Unsaved
          </span>
        ) : save.isSuccess && save.data.res.version.id === base.id ? (
          <span className="text-xs text-ink-3" aria-live="polite">
            Saved as v{base.number}
          </span>
        ) : null}
        <div className="ml-auto flex items-center gap-2">
          {md ? <ViewToggle view={shown} onChange={onViewChange} /> : null}
          <Button
            size="sm"
            variant={dirty ? "primary" : "secondary"}
            disabled={!dirty || save.isPending}
            onClick={doSave}
            icon={<Save className="size-3.5" />}
            title="Save (Ctrl+S or ⌘S)"
          >
            {save.isPending ? "Saving" : "Save"}
          </Button>
        </div>
      </div>

      {stale || conflict ? (
        <div className="no-print border-b border-line p-2">
          <ErrorState
            message={
              conflict
                ? problemMessage(save.error)
                : "This file changed on disk. Reload it to see the change. Reloading discards your unsaved edits."
            }
            action={
              <div className="flex gap-2">
                <Button size="sm" onClick={() => text && navigator.clipboard?.writeText(text)}>
                  Copy my text
                </Button>
                <Button size="sm" variant="primary" onClick={reload}>
                  Reload
                </Button>
              </div>
            }
          />
        </div>
      ) : save.isError ? (
        <div className="no-print border-b border-line p-2">
          <ErrorState message={problemMessage(save.error)} />
        </div>
      ) : null}

      <div className={clsx("grid min-h-0 flex-1", shown === "split" ? "grid-cols-2" : "grid-cols-1")}>
        {shown !== "preview" ? (
          <div className={clsx("no-print min-h-0", shown === "split" && "border-r border-line")}>
            <CodeEditor
              docKey={`${path}@${loadVersion.id}`}
              initial={file.data.text}
              path={path}
              onChange={setText}
              onSave={doSave}
              onView={attachView}
            />
          </div>
        ) : null}
        {shown !== "code" ? (
          <div className="min-h-0">
            <Preview
              ref={previewRef}
              markdown={text ?? file.data.text}
              bundleId={bundleId}
              path={path}
              onOpenPath={onOpenPath}
              onScroll={onPreviewScroll}
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}

// byteToIndex converts a UTF-8 byte offset, as the server reports it, to a string index.
function byteToIndex(text: string, byte: number): number {
  let bytes = 0;
  for (let i = 0; i < text.length; i++) {
    if (bytes >= byte) return i;
    const c = text.codePointAt(i)!;
    bytes += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    if (c >= 0x10000) i++;
  }
  return text.length;
}

const wideQuery = "(min-width: 1024px)";

function useWide() {
  const [wide, setWide] = useState(() => window.matchMedia(wideQuery).matches);
  useEffect(() => {
    const mq = window.matchMedia(wideQuery);
    const on = () => setWide(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return wide;
}

function Frame({ path, children }: { path: string; children: React.ReactNode }) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-10 shrink-0 items-center border-b border-line bg-surface px-3 font-mono text-xs text-ink-2">
        {path}
      </div>
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  );
}

const views: { v: View; label: string; icon: React.ReactNode }[] = [
  { v: "code", label: "Code", icon: <FileCode2 className="size-3.5" /> },
  { v: "split", label: "Split", icon: <Columns2 className="size-3.5" /> },
  { v: "preview", label: "Preview", icon: <Eye className="size-3.5" /> },
];

function ViewToggle({ view, onChange }: { view: View; onChange: (v: View) => void }) {
  return (
    <div role="radiogroup" aria-label="Editor view" className="inline-flex rounded-md border border-line p-0.5">
      {views.map(({ v, label, icon }) => (
        <button
          key={v}
          type="button"
          role="radio"
          aria-checked={view === v}
          onClick={() => onChange(v)}
          className={clsx(
            "items-center gap-1 rounded-sm px-2 py-0.5 text-xs font-medium",
            v === "split" ? "hidden lg:inline-flex" : "inline-flex",
            view === v ? "bg-sunken text-ink" : "text-ink-2 hover:text-ink",
          )}
        >
          {icon}
          {label}
        </button>
      ))}
    </div>
  );
}
