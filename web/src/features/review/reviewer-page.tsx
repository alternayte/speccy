import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Compass, MessageSquarePlus, Paperclip } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ErrorState, Loading } from "@/components/ui/states";
import { contentUrl } from "@/features/editor/editor-pane";
import { Preview } from "@/features/editor/preview";
import { type NewAnchor, ThreadsPanel } from "@/features/threads/threads-panel";
import { getBundleOptions, listFilesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { type Anchor, type BundleVerdict, getFileContent } from "@/lib/api";
import { problemMessage } from "@/lib/problem";

// statusLine says in words where the spec stands. Reviewer mode shows no score, no finding
// count and no verdict chip: a number invites a wrong reading.
export function statusLine(verdict: BundleVerdict | undefined): string {
  if (!verdict) return "The author is still writing this spec. Your comments help them finish it.";
  if (verdict.result === "build_ready") return "The author says this spec is ready to build.";
  if (verdict.result === "stale") return "The author changed this spec after the last check. They are still working.";
  return "The author is still working on this spec.";
}

// ReviewerPage is the whole surface for a person who cannot edit the bundle (reviewer mode):
// the main doc, the assets, one status line, and comments. Every author tool stays hidden.
export function ReviewerPage({ bundleId }: { bundleId: string }) {
  const bundle = useQuery({ ...getBundleOptions({ path: { bundleId } }), refetchInterval: 5000 });
  const version = bundle.data?.current_version;
  const main = bundle.data?.main_doc;
  const files = useQuery({
    ...listFilesOptions({ path: { bundleId }, query: { version: version?.id } }),
    enabled: !!version,
    placeholderData: keepPreviousData,
  });
  const doc = useQuery({
    queryKey: ["reviewer-doc", bundleId, version?.id],
    enabled: !!main && !!version,
    queryFn: async () => {
      const res = await getFileContent({
        path: { bundleId },
        query: { path: main!, version: version!.id },
        parseAs: "text",
        throwOnError: true,
      });
      return res.data as unknown as string;
    },
  });
  const docRef = useRef<HTMLDivElement>(null);
  const [pending, setPending] = useState<NewAnchor>();
  const [hint, setHint] = useState<string>();

  const comment = () => {
    const found = selectedBlock(docRef.current);
    if (!found) {
      setHint("Select the words you want to talk about first.");
      return;
    }
    setHint(undefined);
    setPending({
      kind: "text",
      anchor: {
        file: main!,
        start: found.start,
        end: found.end,
        quote: found.quote,
        prefix: "",
        suffix: "",
        heading_path: [],
      },
      label: found.label,
    });
  };

  if (bundle.isPending) return <Loading label="Opening the spec" />;
  if (bundle.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(bundle.error)} />
      </div>
    );
  const b = bundle.data;
  const assets = (files.data?.items ?? []).filter((f) => f.path !== b.main_doc);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-lg font-semibold tracking-tight">{b.title}</h1>
          <p className="truncate text-xs text-ink-2">{statusLine(b.verdict)}</p>
        </div>
        <Button size="sm" icon={<MessageSquarePlus className="size-3.5" />} onClick={comment}>
          Comment
        </Button>
        <Link
          to="/bundles/$bundleId/tour"
          params={{ bundleId }}
          className="inline-flex h-7 items-center gap-1.5 rounded-md border border-accent bg-accent px-2.5 text-xs font-medium text-accent-ink hover:brightness-110"
        >
          <Compass aria-hidden className="size-3.5" />
          Answer the questions
        </Link>
      </div>
      {hint ? (
        <div className="border-b border-line bg-sunken px-4 py-1.5 text-xs text-ink-2" role="status">
          {hint}
        </div>
      ) : null}

      <div className="flex min-h-0 flex-1 flex-col-reverse lg:flex-row">
        <div className="min-h-0 flex-1 overflow-y-auto">
          {doc.data !== undefined ? (
            <div ref={docRef}>
              <Preview markdown={doc.data} bundleId={bundleId} path={b.main_doc} onOpenPath={() => {}} />
            </div>
          ) : doc.isError ? (
            <div className="p-4">
              <ErrorState message={problemMessage(doc.error)} />
            </div>
          ) : (
            <Loading label="Loading the spec" />
          )}
          <section className="border-t border-line px-6 py-4">
            <h2 className="flex items-center gap-1.5 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
              <Paperclip aria-hidden className="size-3.5" /> Attachments
            </h2>
            {assets.length ? (
              <ul className="mt-2 space-y-1 text-sm">
                {assets.map((f) => (
                  <li key={f.path}>
                    <a
                      className="text-accent hover:underline"
                      href={contentUrl(bundleId, f.path, b.current_version.id)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {f.path}
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-2 text-sm text-ink-3">This spec has no attachments.</p>
            )}
          </section>
        </div>

        <aside className="max-h-[55%] shrink-0 overflow-y-auto border-line bg-surface max-lg:border-b lg:max-h-none lg:w-[380px] lg:border-l">
          <h2 className="flex h-10 items-center border-b border-line px-3 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
            Comments
          </h2>
          <ThreadsPanel
            bundleId={bundleId}
            member={false}
            reviewer
            pending={pending}
            onPendingDone={() => setPending(undefined)}
            onOpenAnchor={(a: Anchor) => scrollToAnchor(docRef.current, a)}
          />
        </aside>
      </div>
    </div>
  );
}

// selectedBlock maps the reader's selection to the markdown block that holds it. The preview
// carries the byte range of every block, so a comment points at saved text without a code view.
function selectedBlock(root: HTMLElement | null) {
  const sel = window.getSelection();
  if (!root || !sel || sel.isCollapsed || sel.rangeCount === 0) return undefined;
  const range = sel.getRangeAt(0);
  if (!root.contains(range.commonAncestorContainer)) return undefined;
  let node: Node | null = range.commonAncestorContainer;
  while (node && node !== root) {
    const el = node as HTMLElement;
    if (el.dataset?.srcStart !== undefined) {
      const start = Number(el.dataset.srcStart);
      const end = Number(el.dataset.srcEnd);
      const quote = el.textContent?.trim() ?? "";
      const words = sel.toString().trim();
      return { start, end, quote, label: words.length > 120 ? `${words.slice(0, 119)}…` : words };
    }
    node = el.parentElement;
  }
  return undefined;
}

function scrollToAnchor(root: HTMLElement | null, a: Anchor) {
  root?.querySelectorAll<HTMLElement>("article [data-src-start]").forEach((el) => {
    if (Number(el.dataset.srcStart) <= a.start && a.start < Number(el.dataset.srcEnd)) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      el.classList.remove("flash");
      void el.offsetWidth;
      el.classList.add("flash");
    }
  });
}
