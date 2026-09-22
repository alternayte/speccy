import { forwardRef, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { clsx } from "clsx";
import { ErrorState } from "@/components/ui/states";
import { Gutter, layerCounts, OverlayLegend, useLayers, useOverlay } from "@/features/overlay/overlay";
import { type Finding, renderMarkdown } from "@/lib/api";
import { blockAt, caretInSource, slice, splice, visiblePrefix } from "./block-edit";
import type { Target } from "./control-bar";
import { problemMessage } from "@/lib/problem";

type Edit = {
  start: number;
  end: number;
  suffix: string;
  value: string;
  caret: number;
  // seq rises when the caret must move: the block opens, or a control writes it.
  seq: number;
  // sel is the selection inside the open block, for the control bar.
  sel: [number, number];
  top: number;
  left: number;
  width: number;
  height: number;
  // el is the rendered block the box covers. It hides while the box is open, so the two do
  // not draw on top of each other.
  el: HTMLElement;
};

// The server renders the preview with the review engine's parser (DEC-017).
export const Preview = forwardRef<
  HTMLDivElement,
  {
    markdown: string;
    bundleId: string;
    path: string;
    onOpenPath: (path: string) => void;
    onScroll?: () => void;
    // onChange makes the preview editable: a click opens the markdown of the block it lands on,
    // and a commit gives the whole file back with that block replaced.
    onChange?: (markdown: string) => void;
    // onTarget reports the open block to the control bar, or null when none is open.
    onTarget?: (t: Target | null) => void;
    // findings are the current review's findings; the overlay shows those of this file (SDD §13.2).
    findings?: Finding[];
    onOpenFinding?: (f: Finding) => void;
  }
>(function Preview(
  { markdown, bundleId, path, onOpenPath, onScroll, onChange, onTarget, findings, onOpenFinding },
  ref,
) {
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [article, setArticle] = useState<HTMLElement | null>(null);
  const { on, toggle } = useLayers();
  const mine = useMemo(() => (findings ?? []).filter((f) => f.anchor.file === path && f.layer), [findings, path]);
  const markers = useOverlay(article, html, mine, on);
  const [theme, setTheme] = useState(() => document.documentElement.classList.contains("dark"));
  // editing holds the open block: its range in the file, its markdown, and where it sits.
  const [editing, setEditing] = useState<Edit | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const on = () => setTheme(document.documentElement.classList.contains("dark"));
    window.addEventListener("speccy:theme", on);
    return () => window.removeEventListener("speccy:theme", on);
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    const timer = setTimeout(async () => {
      const res = await renderMarkdown({ body: { markdown, bundle_id: bundleId, path }, signal: ctrl.signal });
      if (ctrl.signal.aborted) return;
      if (res.error || !res.data) {
        setError(problemMessage(res.error));
        return;
      }
      setError(null);
      setHtml(res.data.html);
    }, 120);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [markdown, bundleId, path]);

  // REQ-004: Mermaid loads only when a doc has a diagram.
  useEffect(() => {
    const nodes = article?.querySelectorAll<HTMLElement>("pre.mermaid");
    if (!nodes || nodes.length === 0) return;
    let cancelled = false;
    import("mermaid").then(({ default: mermaid }) => {
      if (cancelled) return;
      mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: theme ? "dark" : "neutral" });
      mermaid.run({ nodes: Array.from(nodes), suppressErrors: true });
    });
    return () => {
      cancelled = true;
    };
  }, [article, html, theme]);

  // The control bar acts on the open block: it needs its text, the selection, and a way to write.
  useEffect(() => {
    if (!onTarget) return;
    if (!editing) {
      onTarget(null);
      return;
    }
    onTarget({
      value: editing.value,
      start: editing.sel[0],
      end: editing.sel[1],
      apply: (value, caret) => setEditing((e) => (e ? { ...e, value, caret, seq: e.seq + 1, sel: [caret, caret] } : e)),
    });
  }, [editing, onTarget]);

  // The caret moves when the block opens and when a control writes it, not on every keystroke.
  const caret = useRef(0);
  caret.current = editing?.caret ?? 0;
  const caretKey = editing ? `${editing.start}:${editing.seq}` : "";
  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !caretKey) return;
    el.focus();
    el.setSelectionRange(caret.current, caret.current);
  }, [caretKey]);

  // The rendered block hides while its box is open. Both drew on the same lines before.
  useLayoutEffect(() => {
    const el = editing?.el;
    if (!el) return;
    el.style.visibility = "hidden";
    return () => {
      el.style.visibility = "";
    };
  }, [editing?.el]);

  // The box grows with its text, so no line hides under the block below.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !editing) return;
    el.style.height = "auto";
    el.style.height = `${Math.max(el.scrollHeight, editing.height)}px`;
  }, [editing]);

  // A new render of the same file closes the open block: its range may have moved.
  useEffect(() => setEditing(null), [path]);

  const click = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest<HTMLAnchorElement>("a[data-bundle-path]");
    if (a) {
      e.preventDefault();
      onOpenPath(a.dataset.bundlePath!);
      return;
    }
    if (!onChange || editing) return;
    const block = blockAt(e.target as HTMLElement);
    if (!block) return;
    const raw = slice(markdown, block);
    // The block range ends with the blank line that follows it. The editor holds the text only,
    // and the commit puts the blank line back, so typing at the end stays inside the block.
    const source = raw.replace(/\s+$/, "");
    setEditing({
      start: block.start,
      end: block.end,
      suffix: raw.slice(source.length),
      value: source,
      caret: caretInSource(source, visiblePrefix(block.el, e.clientX, e.clientY)),
      seq: 0,
      sel: [0, 0],
      top: block.el.offsetTop,
      left: block.el.offsetLeft,
      width: block.el.offsetWidth,
      height: block.el.offsetHeight,
      el: block.el,
    });
  };

  const commit = useCallback(() => {
    if (!editing || !onChange) return;
    if (editing.value + editing.suffix !== slice(markdown, editing)) {
      onChange(splice(markdown, editing, editing.value + editing.suffix));
    }
    setEditing(null);
  }, [editing, markdown, onChange]);

  return (
    <div ref={ref} onScroll={onScroll} className="print-only-doc h-full overflow-y-auto bg-surface">
      {findings && mine.length > 0 ? <OverlayLegend on={on} toggle={toggle} counts={layerCounts(mine)} /> : null}
      <div className="mx-auto max-w-[calc(var(--measure-wide)+var(--space-16))] px-6 py-10 sm:px-8">
        {error ? <ErrorState message={error} /> : null}
        {html === null && !error ? <p className="text-sm text-ink-3">Rendering</p> : null}
        {html !== null ? (
          <div className="relative">
            {onOpenFinding ? <Gutter markers={markers} onOpen={onOpenFinding} /> : null}
            {/* The server escapes doc text and drops raw HTML (SDD §14.3). */}
            <article
              ref={setArticle}
              className={clsx("doc doc-wide", onChange && "cursor-text")}
              onClick={click}
              dangerouslySetInnerHTML={{ __html: html }}
            />
            {editing ? (
              <textarea
                ref={box}
                value={editing.value}
                aria-label="Edit this block"
                spellCheck
                onChange={(e) => setEditing({ ...editing, value: e.target.value })}
                onSelect={(e) => {
                  const el = e.currentTarget;
                  setEditing((x) => (x ? { ...x, sel: [el.selectionStart, el.selectionEnd] } : x));
                }}
                onBlur={commit}
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    e.stopPropagation();
                    setEditing(null);
                  }
                }}
                style={{ top: editing.top, left: editing.left, width: editing.width }}
                className="absolute z-10 resize-none rounded-sm bg-surface px-1 font-mono text-sm leading-doc text-ink outline-2 outline-focus"
              />
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
});
