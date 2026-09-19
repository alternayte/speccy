import { forwardRef, useEffect, useMemo, useState } from "react";
import { ErrorState } from "@/components/ui/states";
import { Gutter, layerCounts, OverlayLegend, useLayers, useOverlay } from "@/features/overlay/overlay";
import { type Finding, renderMarkdown } from "@/lib/api";
import { problemMessage } from "@/lib/problem";

// The server renders the preview with the review engine's parser (DEC-017).
export const Preview = forwardRef<
  HTMLDivElement,
  {
    markdown: string;
    bundleId: string;
    path: string;
    onOpenPath: (path: string) => void;
    onScroll?: () => void;
    // findings are the current review's findings; the overlay shows those of this file (SDD §13.2).
    findings?: Finding[];
    onOpenFinding?: (f: Finding) => void;
  }
>(function Preview({ markdown, bundleId, path, onOpenPath, onScroll, findings, onOpenFinding }, ref) {
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [article, setArticle] = useState<HTMLElement | null>(null);
  const { on, toggle } = useLayers();
  const mine = useMemo(() => (findings ?? []).filter((f) => f.anchor.file === path && f.layer), [findings, path]);
  const markers = useOverlay(article, html, mine, on);
  const [theme, setTheme] = useState(() => document.documentElement.classList.contains("dark"));

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

  const click = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest<HTMLAnchorElement>("a[data-bundle-path]");
    if (!a) return;
    e.preventDefault();
    onOpenPath(a.dataset.bundlePath!);
  };

  return (
    <div ref={ref} onScroll={onScroll} className="print-only-doc h-full overflow-y-auto bg-surface">
      {findings && mine.length > 0 ? <OverlayLegend on={on} toggle={toggle} counts={layerCounts(mine)} /> : null}
      <div className="mx-auto max-w-[calc(var(--measure)+var(--space-16))] px-6 py-10 sm:px-8">
        {error ? <ErrorState message={error} /> : null}
        {html === null && !error ? <p className="text-sm text-ink-3">Rendering</p> : null}
        {html !== null ? (
          <div className="relative">
            {onOpenFinding ? <Gutter markers={markers} onOpen={onOpenFinding} /> : null}
            {/* The server escapes doc text and drops raw HTML (SDD §14.3). */}
            <article ref={setArticle} className="doc" onClick={click} dangerouslySetInnerHTML={{ __html: html }} />
          </div>
        ) : null}
      </div>
    </div>
  );
});
