import { forwardRef, useEffect, useRef, useState } from "react";
import { ErrorState } from "@/components/ui/states";
import { renderMarkdown } from "@/lib/api";
import { problemMessage } from "@/lib/problem";

// The server renders the preview with the review engine's parser (DEC-017).
export const Preview = forwardRef<
  HTMLDivElement,
  { markdown: string; bundleId: string; path: string; onOpenPath: (path: string) => void; onScroll?: () => void }
>(function Preview({ markdown, bundleId, path, onOpenPath, onScroll }, ref) {
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const docRef = useRef<HTMLDivElement>(null);
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
    const nodes = docRef.current?.querySelectorAll<HTMLElement>("pre.mermaid");
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
  }, [html, theme]);

  const click = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest<HTMLAnchorElement>("a[data-bundle-path]");
    if (!a) return;
    e.preventDefault();
    onOpenPath(a.dataset.bundlePath!);
  };

  return (
    <div ref={ref} onScroll={onScroll} className="print-only-doc h-full overflow-y-auto bg-surface">
      <div className="mx-auto max-w-[calc(var(--measure)+var(--space-16))] px-6 py-10 sm:px-8">
        {error ? <ErrorState message={error} /> : null}
        {html === null && !error ? <p className="text-sm text-ink-3">Rendering</p> : null}
        {html !== null ? (
          // The server escapes doc text and drops raw HTML (SDD §14.3).
          <article ref={docRef} className="doc" onClick={click} dangerouslySetInnerHTML={{ __html: html }} />
        ) : null}
      </div>
    </div>
  );
});
