import { EditorView } from "@codemirror/view";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { Columns2, Eye, FileCode2, MessageSquarePlus, PenLine, Save } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Divider, useDivider } from "@/components/ui/divider";
import { ErrorState, Loading } from "@/components/ui/states";
import { type Finding, getFileContent, putFileContent } from "@/lib/api";
import { problemCode, problemMessage } from "@/lib/problem";
import { CodeEditor } from "./code-editor";
import { ControlBar, type Target } from "./control-bar";
import { flashRange } from "./flash";
import { Preview } from "./preview";
import { ScrollLink } from "./scroll-sync";

export type View = "code" | "preview" | "split";

const imageExt = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "avif"]);

export function isMarkdown(path: string) {
  return /\.(md|markdown)$/i.test(path);
}

export function contentUrl(docId: string, path: string, version?: string) {
  const q = new URLSearchParams({ path });
  if (version) q.set("version", version);
  return `/api/v1/docs/${docId}/files/content?${q}`;
}

type Loaded = { kind: "text"; text: string } | { kind: "binary" };

async function loadFile(docId: string, path: string, version: string): Promise<Loaded> {
  const res = await getFileContent({
    path: { docId },
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
  docId,
  path,
  version,
  sha,
  view,
  onViewChange,
  onOpenPath,
  onSaved,
  onDirtyChange,
  focus,
  readOnly = false,
  profileKey,
  onComment,
  findings,
  onOpenFinding,
}: {
  docId: string;
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
  // readOnly is for a guest or a member who is not an author: no edits, no save.
  readOnly?: boolean;
  // profileKey drives the profile controls of the control bar.
  profileKey: string;
  // onComment opens a thread on the selected text (REQ-087), as a byte range of the saved file.
  onComment?: (sel: { file: string; start: number; end: number; quote: string }) => void;
  // findings feed the overlay in the preview (SDD §13.2).
  findings?: Finding[];
  onOpenFinding?: (f: Finding) => void;
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
  const [hint, setHint] = useState<string>();
  const previewRef = useRef<HTMLDivElement>(null);
  // The link keeps the two panes together and drops the echo of its own writes.
  const link = useRef(new ScrollLink());
  const wide = useWide();
  // The divider between the code and the preview in the split view.
  const split = useDivider({
    key: "speccy-split-width",
    from: "left",
    min: 280,
    max: 1400,
    // Half of what the code and the preview share, once the explorer and the rail have theirs.
    initial: () => Math.max(280, Math.round((window.innerWidth - 568) / 2)),
  });
  // target is the block the control bar writes into, and bar keeps the person's choice.
  const [target, setTarget] = useState<Target | null>(null);
  const [bar, setBar] = useState(() => {
    try {
      return localStorage.getItem("speccy-control-bar") === "on";
    } catch {
      return false;
    }
  });
  const toggleBar = () => {
    setBar((on) => {
      try {
        localStorage.setItem("speccy-control-bar", on ? "off" : "on");
      } catch {
        // Storage is not available: the choice lasts for this page load.
      }
      return !on;
    });
  };

  const file = useQuery({
    queryKey: ["file", docId, path, loadVersion.id],
    queryFn: () => loadFile(docId, path, loadVersion.id),
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
        path: { docId },
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
      qc.setQueryData(["file", docId, path, res.version.id], { kind: "text", text: body });
      onSaved();
    },
  });

  const doSave = useCallback(() => {
    if (!readOnly && text !== null && text !== saved && !save.isPending) save.mutate(text);
  }, [readOnly, text, saved, save]);

  const reload = () => {
    save.reset();
    setStale(false);
    setKnownSha(sha);
    setLoadVersion({ ...version });
    qc.invalidateQueries({ queryKey: ["file", docId, path, version.id] });
  };

  const onEditorScroll = useCallback(() => {
    if (view !== "split" || !editorView.current || !previewRef.current) return;
    link.current.fromEditor(editorView.current, previewRef.current);
  }, [view]);

  const onPreviewScroll = useCallback(() => {
    if (view !== "split" || !editorView.current || !previewRef.current) return;
    link.current.fromPreview(previewRef.current, editorView.current);
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
    // The preview renders on the server. A link that opens the page at a range can arrive
    // before the preview's blocks do, so the preview waits for them a little.
    let timer: ReturnType<typeof setTimeout>;
    const scrollPreview = (tries: number) => {
      const blocks = previewRef.current?.querySelectorAll<HTMLElement>("article [data-src-start]");
      if (!previewRef.current) return;
      if (!blocks?.length) {
        if (tries > 0) timer = setTimeout(() => scrollPreview(tries - 1), 100);
        return;
      }
      let target: HTMLElement | null = null;
      blocks.forEach((el) => {
        if (Number(el.dataset.srcStart) <= focus.start && focus.start < Number(el.dataset.srcEnd)) target = el;
      });
      const el = target as HTMLElement | null;
      if (el) {
        el.scrollIntoView({ block: "center", behavior: "smooth" });
        el.classList.remove("flash");
        void el.offsetWidth; // restart the animation
        el.classList.add("flash");
      }
    };
    timer = setTimeout(() => {
      const view = editorView.current;
      if (view) {
        const from = Math.min(byteToIndex(text, focus.start), view.state.doc.length);
        const to = Math.min(byteToIndex(text, focus.end), view.state.doc.length);
        // The wash, not the selection alone: the selection paints in the accent's soft tone,
        // which on the editor's surface is too faint to notice.
        flashRange(view, from, to);
        view.focus();
      }
      scrollPreview(30);
    }, 80);
    return () => clearTimeout(timer);
  }, [focus, file.data]);

  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  if (imageExt.has(ext)) {
    return (
      <Frame path={path}>
        <div className="flex h-full items-center justify-center overflow-auto bg-sunken p-6">
          <img src={contentUrl(docId, path, version.id)} alt={path} className="max-h-full max-w-full" />
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
          <a className="text-accent underline" href={contentUrl(docId, path, version.id)} download>
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
          {onComment && shown !== "preview" ? (
            <Button
              size="sm"
              variant="ghost"
              icon={<MessageSquarePlus className="size-3.5" />}
              title="Comment on the selected text"
              onClick={() => {
                const v = editorView.current;
                const sel = v?.state.selection.main;
                if (!v || !sel || sel.empty) {
                  setHint("Select some text in the code view first.");
                  return;
                }
                if (dirty) {
                  setHint("Save the file first, so the comment points at saved text.");
                  return;
                }
                const text = v.state.doc.toString();
                const enc = new TextEncoder();
                const start = enc.encode(text.slice(0, sel.from)).length;
                const quote = text.slice(sel.from, sel.to);
                setHint(undefined);
                onComment({ file: path, start, end: start + enc.encode(quote).length, quote });
              }}
            >
              <span className="hidden lg:inline">Comment</span>
            </Button>
          ) : null}
          {md && !readOnly && shown !== "code" ? (
            <Button
              size="sm"
              variant={bar ? "primary" : "ghost"}
              onClick={toggleBar}
              icon={<PenLine className="size-3.5" />}
              title="Writing controls"
              aria-pressed={bar}
            >
              <span className="hidden lg:inline">Controls</span>
            </Button>
          ) : null}
          {md ? <ViewToggle view={shown} onChange={onViewChange} /> : null}
          {readOnly ? (
            <span className="text-xs text-ink-3">Read only</span>
          ) : (
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
          )}
        </div>
      </div>

      {bar && md && !readOnly && shown !== "code" ? (
        <ControlBar
          target={target}
          profileKey={profileKey}
          markdown={text ?? ""}
          onInsertSection={(next) => setText(next)}
        />
      ) : null}
      {hint ? (
        <div className="no-print border-b border-line bg-sunken px-3 py-1.5 text-xs text-ink-2" role="status">
          {hint}
        </div>
      ) : null}
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

      <div className="flex min-h-0 min-w-0 flex-1">
        {shown !== "preview" ? (
          <div
            style={shown === "split" ? { width: split.width } : undefined}
            className={clsx("no-print min-h-0 min-w-0", shown === "split" ? "shrink-0" : "flex-1")}
          >
            <CodeEditor
              docKey={`${path}@${loadVersion.id}`}
              initial={file.data.text}
              path={path}
              onChange={setText}
              onSave={doSave}
              onView={attachView}
              readOnly={readOnly}
            />
          </div>
        ) : null}
        {shown === "split" ? <Divider label="Width of the editor" {...split.props} /> : null}
        {shown !== "code" ? (
          <div className="min-h-0 min-w-0 flex-1">
            <Preview
              ref={previewRef}
              markdown={text ?? file.data.text}
              docId={docId}
              path={path}
              onOpenPath={onOpenPath}
              onScroll={onPreviewScroll}
              onChange={readOnly || !isMarkdown(path) ? undefined : setText}
              onTarget={bar ? setTarget : undefined}
              findings={findings}
              onOpenFinding={onOpenFinding}
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
