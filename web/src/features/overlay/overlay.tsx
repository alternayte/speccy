import { clsx } from "clsx";
import { Ban, CircleHelp, Link2Off, OctagonX, PenLine, type LucideIcon } from "lucide-react";
import { useCallback, useLayoutEffect, useState } from "react";
import type { Finding } from "@/lib/api";

export type FindingLayer = NonNullable<Finding["layer"]>;

// SDD §13.2: five layers. Colour is never the only signal: each layer has an icon and an
// underline style of its own.
export const layers: { key: FindingLayer; label: string; icon: LucideIcon; hint: string }[] = [
  { key: "risk", label: "Risk", icon: OctagonX, hint: "MUST findings" },
  { key: "ambiguous", label: "Ambiguous", icon: CircleHelp, hint: "Readers disagree, or the doc does not say" },
  { key: "contradicted", label: "Contradicted", icon: Ban, hint: "A source or a linked doc says otherwise" },
  { key: "unverified", label: "Unverified", icon: Link2Off, hint: "No source confirms the claim" },
  { key: "slop", label: "Writing", icon: PenLine, hint: "Lint findings on the writing" },
];

export const layerIcon = Object.fromEntries(layers.map((l) => [l.key, l.icon])) as Record<FindingLayer, LucideIcon>;

const storageKey = "speccy.overlay";

// defaultLayers are on until the reader chooses: the findings that block and the ones that
// need a decision. The writing, claim, and conflict layers are one click away.
const defaultLayers: FindingLayer[] = ["risk", "ambiguous"];

// useLayers keeps the switched-on layers per browser.
export function useLayers() {
  const [on, setOn] = useState<Set<FindingLayer>>(() => {
    try {
      const raw = localStorage.getItem(storageKey);
      if (raw) return new Set(JSON.parse(raw) as FindingLayer[]);
    } catch {
      // storage can be blocked; the default applies
    }
    return new Set(defaultLayers);
  });
  const toggle = useCallback((k: FindingLayer) => {
    setOn((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      try {
        localStorage.setItem(storageKey, JSON.stringify([...next]));
      } catch {
        // ignore
      }
      return next;
    });
  }, []);
  return { on, toggle };
}

// OverlayLegend is the legend and the switch for each layer. It stays visible while any layer
// is on (SDD §13.2).
export function OverlayLegend({
  on,
  toggle,
  counts,
}: {
  on: Set<FindingLayer>;
  toggle: (k: FindingLayer) => void;
  counts: Partial<Record<FindingLayer, number>>;
}) {
  return (
    <div
      role="group"
      aria-label="Overlay layers"
      className="no-print sticky top-0 z-10 flex flex-wrap items-center gap-1.5 border-b border-line bg-surface/95 px-4 py-2 backdrop-blur-sm sm:px-6"
    >
      <span className="mr-1 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Overlay</span>
      {layers.map(({ key, label, icon: Icon, hint }) => {
        const active = on.has(key);
        return (
          <button
            key={key}
            type="button"
            aria-pressed={active}
            title={hint}
            onClick={() => toggle(key)}
            className={clsx(
              "inline-flex h-6 items-center gap-1.5 rounded-md border px-2 text-xs transition-colors",
              active ? "border-line-strong bg-surface text-ink" : "border-transparent text-ink-3 hover:text-ink-2",
            )}
          >
            <Icon aria-hidden className="size-3.5" style={{ color: active ? `var(--layer-${key})` : undefined }} />
            <span className={clsx(active && `layer-sample layer-sample-${key}`)}>{label}</span>
            <span className="font-mono text-2xs text-ink-3">{counts[key] ?? 0}</span>
          </button>
        );
      })}
    </div>
  );
}

export type Marker = { top: number; findings: Finding[] };

// useOverlay underlines the text of each finding in a switched-on layer, with the CSS Custom
// Highlight API, and returns a gutter marker per block. The rendered doc is the server's HTML,
// so the overlay does not change its DOM.
export function useOverlay(
  article: HTMLElement | null,
  html: string | null,
  findings: Finding[],
  on: Set<FindingLayer>,
): Marker[] {
  const [markers, setMarkers] = useState<Marker[]>([]);
  // Images and diagrams load after the HTML and move the blocks: measure again on resize.
  const [size, setSize] = useState(0);
  useLayoutEffect(() => {
    if (!article) return;
    const ro = new ResizeObserver(() => setSize(article.offsetHeight));
    ro.observe(article);
    return () => ro.disconnect();
  }, [article]);

  useLayoutEffect(() => {
    const registry = typeof CSS !== "undefined" && "highlights" in CSS ? CSS.highlights : undefined;
    const ranges: Record<string, Range[]> = {};
    const byBlock = new Map<HTMLElement, Finding[]>();
    if (article && html) {
      const blocks = Array.from(article.querySelectorAll<HTMLElement>("[data-src-start]"));
      for (const f of findings) {
        if (!f.layer || !on.has(f.layer) || f.waived || f.anchor.detached) continue;
        const block = innermost(blocks, f.anchor.start);
        if (!block) continue;
        byBlock.set(block, [...(byBlock.get(block) ?? []), f]);
        // A finding on a whole heading (a section) gets its gutter icon only: an underlined
        // heading competes with the text the reader came for.
        const r = /^\s*#{1,6}\s/.test(f.anchor.quote) ? null : findText(block, f.anchor.quote);
        if (r) (ranges[f.layer] ??= []).push(r);
      }
    }
    if (registry) {
      for (const l of layers) {
        const rs = ranges[l.key];
        if (rs?.length) registry.set(`ov-${l.key}`, new Highlight(...rs));
        else registry.delete(`ov-${l.key}`);
      }
    }
    const next: Marker[] = [];
    byBlock.forEach((fs, el) => next.push({ top: el.offsetTop, findings: fs }));
    next.sort((a, b) => a.top - b.top);
    setMarkers(next);
    return () => {
      if (registry) for (const l of layers) registry.delete(`ov-${l.key}`);
    };
  }, [article, html, findings, on, size]);

  return markers;
}

// innermost returns the smallest block whose source range holds the offset.
function innermost(blocks: HTMLElement[], offset: number): HTMLElement | undefined {
  let best: HTMLElement | undefined;
  let size = Infinity;
  for (const el of blocks) {
    const s = Number(el.dataset.srcStart);
    const e = Number(el.dataset.srcEnd);
    if (s <= offset && offset < e && e - s < size) {
      best = el;
      size = e - s;
    }
  }
  return best;
}

// A quote longer than this marks its block in the gutter only: underlining a whole section
// hides the text it is about.
const maxQuote = 300;

// findText finds the quote in the rendered text of a block. Markdown marks and runs of
// whitespace do not count, so "**REQ-001:** An agent" finds "REQ-001: An agent".
function findText(block: HTMLElement, quote: string): Range | null {
  const q = normalize(quote);
  if (q.length < 2 || q.length > maxQuote) return null;
  const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
  const pos: { node: Text; offset: number }[] = [];
  let text = "";
  let space = true;
  for (let n = walker.nextNode() as Text | null; n; n = walker.nextNode() as Text | null) {
    const v = n.data;
    for (let i = 0; i < v.length; i++) {
      const c = v[i]!;
      if (c === "*" || c === "_" || c === "`") continue;
      if (/\s/.test(c)) {
        if (space) continue;
        space = true;
        text += " ";
      } else {
        space = false;
        text += c;
      }
      pos.push({ node: n, offset: i });
    }
  }
  const at = text.indexOf(q);
  if (at < 0) return null;
  const start = pos[at];
  const end = pos[at + q.length - 1];
  if (!start || !end) return null;
  const r = document.createRange();
  r.setStart(start.node, start.offset);
  r.setEnd(end.node, end.offset + 1);
  return r;
}

function normalize(s: string): string {
  return s
    .replace(/[*_`]/g, "")
    .replace(/^\s*(#{1,6}|[-+]|\d+\.)\s+/gm, "")
    .replace(/\s+/g, " ")
    .trim();
}

// Gutter shows one marker per block that has findings, beside the block. A click opens the
// first finding; the rail lists the rest.
export function Gutter({ markers, onOpen }: { markers: Marker[]; onOpen: (f: Finding) => void }) {
  return (
    <div aria-hidden={markers.length === 0} className="no-print pointer-events-none absolute inset-y-0 -left-6 w-5">
      {markers.map((m) => {
        const first = m.findings[0]!;
        const kinds = [...new Set(m.findings.map((f) => f.layer!))];
        return (
          <button
            key={`${m.top}-${first.id}`}
            type="button"
            onClick={() => onOpen(first)}
            title={m.findings.map((f) => f.message).join("\n")}
            aria-label={`${m.findings.length} finding${m.findings.length === 1 ? "" : "s"}: ${first.message}`}
            className="pointer-events-auto absolute left-0 flex flex-col items-center gap-0.5 rounded-sm py-0.5 hover:bg-sunken"
            style={{ top: m.top + 4 }}
          >
            {kinds.slice(0, 3).map((k) => {
              const Icon = layerIcon[k];
              return <Icon key={k} className="size-3.5" style={{ color: `var(--layer-${k})` }} />;
            })}
          </button>
        );
      })}
    </div>
  );
}

// layerCounts counts the open findings of each layer for the legend.
export function layerCounts(findings: Finding[]) {
  const c: Partial<Record<FindingLayer, number>> = {};
  for (const f of findings) if (f.layer && !f.waived && !f.anchor.detached) c[f.layer] = (c[f.layer] ?? 0) + 1;
  return c;
}
