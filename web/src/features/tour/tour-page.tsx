import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft, ChevronLeft, ChevronRight, CircleCheck } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { Preview } from "@/features/editor/preview";
import { levelStyle } from "@/features/bundle/verdict";
import { type Finding, getFileContent, type TourPoint } from "@/lib/api";
import {
  approveWaiverMutation,
  getBundleOptions,
  getTourOptions,
  getTourQueryKey,
  listFindingsOptions,
  markDecisionMutation,
  openBundleThreadMutation,
  postMessageMutation,
  rejectWaiverMutation,
  requestWaiverMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { useMe } from "@/features/account/me";
import { problemMessage } from "@/lib/problem";

const kindLabel: Record<TourPoint["kind"], string> = {
  blocking_thread: "Blocking thread",
  finding: "Needs a decision",
  waiver: "Waiver request",
  open_decision: "Open decision",
};

type Mode = "decide" | "waive" | "comment";

// TourPage steps through the points that need a human decision (SDD §13.3). The section of the
// current point is in focus; the rest of the doc is dimmed. Keys: j and k move, d decides,
// w waives, c comments, Esc leaves.
export function TourPage({ bundleId }: { bundleId: string }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const bundle = useQuery(getBundleOptions({ path: { bundleId } }));
  const tour = useQuery(getTourOptions({ path: { bundleId } }));
  const runId = tour.data?.run_id;
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const guest = !!useMe().data?.guest;
  const main = bundle.data?.main_doc;
  const version = bundle.data?.current_version.id;
  const doc = useQuery({
    queryKey: ["tour-doc", bundleId, version],
    enabled: !!main && !!version,
    queryFn: async () => {
      const res = await getFileContent({
        path: { bundleId },
        query: { path: main!, version: version! },
        parseAs: "text",
        throwOnError: true,
      });
      return res.data as unknown as string;
    },
  });
  const [wanted, setIndex] = useState(0);
  const [mode, setMode] = useState<Mode>();
  const points = tour.data?.points ?? [];
  // A decision removes its point: keep the index inside the list.
  const index = Math.max(0, Math.min(wanted, points.length - 1));
  const point = points[index];

  const move = useCallback(
    (d: number) => {
      setMode(undefined);
      setIndex(Math.max(0, Math.min(points.length - 1, index + d)));
    },
    [points.length, index],
  );
  const leave = useCallback(() => navigate({ to: "/bundles/$bundleId", params: { bundleId } }), [navigate, bundleId]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      const typing = t.closest("textarea, input, [contenteditable]");
      if (e.key === "Escape") {
        if (mode) setMode(undefined);
        else if (!typing) leave();
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey || !point) return;
      switch (e.key) {
        case "j":
          move(1);
          break;
        case "k":
          move(-1);
          break;
        case "d":
          if (!guest && point.kind !== "waiver") setMode("decide");
          break;
        case "w":
          if (!guest && point.kind === "finding") setMode("waive");
          break;
        case "c":
          setMode("comment");
          break;
        default:
          return;
      }
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode, point, move, leave, guest]);

  const done = () => {
    setMode(undefined);
    qc.invalidateQueries({ queryKey: getTourQueryKey({ path: { bundleId } }) });
    qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
  };

  if (tour.isPending || bundle.isPending) return <Loading label="Loading the tour" />;
  if (tour.isError || bundle.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(tour.error ?? bundle.error)} />
      </div>
    );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
        <Link
          to="/bundles/$bundleId"
          params={{ bundleId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> {bundle.data.title}
        </Link>
        <h1 className="text-md font-semibold tracking-tight">Tour</h1>
        {points.length ? (
          <span className="font-mono text-xs text-ink-3">
            {index + 1} of {points.length}
          </span>
        ) : null}
        <span className="ml-auto hidden text-xs text-ink-3 md:inline">
          <Kbd>j</Kbd> <Kbd>k</Kbd> move · <Kbd>d</Kbd> decide · <Kbd>w</Kbd> waive · <Kbd>c</Kbd> comment ·{" "}
          <Kbd>Esc</Kbd> leave
        </span>
      </div>

      {!point ? (
        <div className="p-6">
          <Empty title="Nothing needs a decision">
            {runId
              ? "No blocking threads, no findings that need a decision, no pending waivers, and no open decisions."
              : "Run a review first. The tour lists the points of the latest review that need a human decision."}
          </Empty>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col-reverse lg:flex-row">
          <div className="min-h-0 flex-1 border-t border-line lg:border-t-0 lg:border-r">
            {doc.data !== undefined ? (
              <FocusedDoc
                bundleId={bundleId}
                path={main!}
                markdown={doc.data}
                point={point}
                findings={findings.data?.items}
              />
            ) : doc.isError ? (
              <div className="p-4">
                <ErrorState message={problemMessage(doc.error)} />
              </div>
            ) : (
              <Loading label="Loading the doc" />
            )}
          </div>
          <aside className="max-h-[55%] shrink-0 overflow-y-auto bg-surface lg:max-h-none lg:w-[400px]">
            <PointCard
              key={point.key}
              bundleId={bundleId}
              point={point}
              mode={mode}
              setMode={setMode}
              guest={guest}
              onDone={done}
            />
            <div className="flex items-center justify-between gap-2 border-t border-line px-4 py-3">
              <Button
                size="sm"
                icon={<ChevronLeft className="size-3.5" />}
                onClick={() => move(-1)}
                disabled={index === 0}
              >
                Previous
              </Button>
              <ol className="flex flex-wrap justify-center gap-1" aria-label="Points">
                {points.map((p, i) => (
                  <li key={p.key}>
                    <button
                      type="button"
                      aria-label={`Point ${i + 1}`}
                      aria-current={i === index}
                      onClick={() => {
                        setMode(undefined);
                        setIndex(i);
                      }}
                      className={clsx(
                        "block size-2 rounded-full",
                        i === index ? "bg-accent" : "bg-line-strong hover:bg-ink-3",
                      )}
                    />
                  </li>
                ))}
              </ol>
              <Button
                size="sm"
                icon={<ChevronRight className="size-3.5" />}
                onClick={() => move(1)}
                disabled={index >= points.length - 1}
              >
                Next
              </Button>
            </div>
          </aside>
        </div>
      )}
    </div>
  );
}

function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded-sm border border-line-strong bg-sunken px-1 font-mono text-2xs text-ink-2">{children}</kbd>
  );
}

// FocusedDoc renders the main doc and dims every block outside the point's section.
function FocusedDoc({
  bundleId,
  path,
  markdown,
  point,
  findings,
}: {
  bundleId: string;
  path: string;
  markdown: string;
  point: TourPoint;
  findings?: Finding[];
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [rendered, setRendered] = useState(0);
  useEffect(() => {
    // The preview renders on the server; watch for its article to change.
    const root = ref.current;
    if (!root) return;
    const mo = new MutationObserver(() => setRendered((n) => n + 1));
    mo.observe(root, { childList: true, subtree: true });
    return () => mo.disconnect();
  }, []);

  useLayoutEffect(() => {
    const article = ref.current?.querySelector("article");
    if (!article) return;
    const want = point.anchor?.heading_path ?? [];
    const stack: string[] = [];
    let first: HTMLElement | undefined;
    let inFocus = want.length === 0;
    for (const el of Array.from(article.children) as HTMLElement[]) {
      const m = /^H([1-6])$/.exec(el.tagName);
      if (m) {
        const level = Number(m[1]);
        stack.length = level - 1;
        stack[level - 1] = el.textContent?.trim() ?? "";
      }
      const path = stack.filter((s) => s !== undefined);
      if (want.length) inFocus = want.every((w, i) => path[i] === w);
      el.classList.toggle("tour-dim", !inFocus);
      if (inFocus && !first) first = el;
    }
    // A text anchor points at one block: scroll to it. Otherwise scroll to the section.
    let target = first;
    if (point.anchor && point.anchor.quote) {
      article.querySelectorAll<HTMLElement>("[data-src-start]").forEach((el) => {
        if (Number(el.dataset.srcStart) <= point.anchor!.start && point.anchor!.start < Number(el.dataset.srcEnd))
          target = el;
      });
    }
    target?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [point, rendered]);

  return (
    <div ref={ref} className="h-full">
      <Preview markdown={markdown} bundleId={bundleId} path={path} onOpenPath={() => {}} findings={findings} />
    </div>
  );
}

function PointCard({
  bundleId,
  point,
  mode,
  setMode,
  guest,
  onDone,
}: {
  bundleId: string;
  point: TourPoint;
  mode?: Mode;
  setMode: (m?: Mode) => void;
  guest: boolean;
  onDone: () => void;
}) {
  const level = point.level ? levelStyle[point.level] : undefined;
  const approve = useMutation({ ...approveWaiverMutation(), onSuccess: onDone });
  const reject = useMutation({ ...rejectWaiverMutation(), onSuccess: onDone });
  return (
    <div className="p-4">
      <p className="flex items-center gap-1.5 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        {level ? <level.icon aria-hidden className={clsx("size-3.5", level.tone)} /> : null}
        {kindLabel[point.kind]}
        {point.check_slug ? (
          <span className="font-mono font-normal tracking-normal normal-case">{point.check_slug}</span>
        ) : null}
      </p>
      <h2 className="mt-2 text-lg leading-snug font-semibold tracking-tight">{point.ask}</h2>
      {point.context ? <p className="mt-2 text-sm text-ink-2">{point.context}</p> : null}
      {point.anchor?.quote && point.anchor.quote.length < 400 ? (
        <p className="mt-3 border-l-2 border-line-strong pl-2 font-mono text-xs text-ink-2">{point.anchor.quote}</p>
      ) : null}
      {point.anchor?.detached ? (
        <p className="mt-2 text-xs text-warn">
          The text changed, and Speccy cannot find this quote in the current version.
        </p>
      ) : null}

      {point.kind === "waiver" ? (
        point.can_approve ? (
          <div className="mt-4 flex gap-1.5">
            <Button
              variant="primary"
              size="sm"
              onClick={() => approve.mutate({ path: { waiverId: point.waiver_id! } })}
              disabled={approve.isPending}
            >
              Approve
            </Button>
            <Button
              size="sm"
              onClick={() => reject.mutate({ path: { waiverId: point.waiver_id! } })}
              disabled={reject.isPending}
            >
              Reject
            </Button>
          </div>
        ) : (
          <p className="mt-4 text-xs text-ink-3">The waiver policy does not let you approve this waiver.</p>
        )
      ) : null}
      {approve.isError || reject.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(approve.error ?? reject.error)} />
        </div>
      ) : null}

      {!mode ? (
        <div className="mt-4 flex flex-wrap gap-1.5">
          {!guest && point.kind !== "waiver" ? (
            <Button size="sm" variant="primary" onClick={() => setMode("decide")}>
              Decide <Kbd>d</Kbd>
            </Button>
          ) : null}
          {!guest && point.kind === "finding" ? (
            <Button size="sm" onClick={() => setMode("waive")}>
              Ask for a waiver <Kbd>w</Kbd>
            </Button>
          ) : null}
          <Button size="sm" onClick={() => setMode("comment")}>
            Comment <Kbd>c</Kbd>
          </Button>
        </div>
      ) : (
        <Act bundleId={bundleId} point={point} mode={mode} onCancel={() => setMode(undefined)} onDone={onDone} />
      )}
    </div>
  );
}

const prompts: Record<Mode, { label: string; placeholder: string; submit: string; min: number }> = {
  decide: {
    label: "Your decision",
    placeholder: "State the decision in one or two sentences.",
    submit: "Record the decision",
    min: 1,
  },
  waive: {
    label: "Reason for the waiver",
    placeholder: "Why this check does not apply here. At least 20 characters.",
    submit: "Ask for the waiver",
    min: 20,
  },
  comment: { label: "Comment", placeholder: "Write a comment for the team.", submit: "Post", min: 1 },
};

// Act writes the decision, the waiver request, or the comment. A decision or a comment goes to
// the point's thread, or to a new thread on the finding.
function Act({
  bundleId,
  point,
  mode,
  onCancel,
  onDone,
}: {
  bundleId: string;
  point: TourPoint;
  mode: Mode;
  onCancel: () => void;
  onDone: () => void;
}) {
  const [text, setText] = useState("");
  const open = useMutation(openBundleThreadMutation());
  const post = useMutation(postMessageMutation());
  const decide = useMutation(markDecisionMutation());
  const waive = useMutation(requestWaiverMutation());
  const [done, setDone] = useState(false);
  const busy = open.isPending || post.isPending || decide.isPending || waive.isPending;
  const error = open.error ?? post.error ?? decide.error ?? waive.error;
  const p = prompts[mode];

  const submit = async () => {
    if (mode === "waive") {
      await waive.mutateAsync({ path: { bundleId }, body: { finding_id: point.finding_id!, reason: text } });
    } else {
      let thread;
      if (point.thread_id) {
        thread = await post.mutateAsync({ path: { threadId: point.thread_id }, body: { body: text } });
      } else {
        const anchor = point.finding_id
          ? { anchor_kind: "finding" as const, anchor: { finding_id: point.finding_id, check_slug: point.check_slug } }
          : { anchor_kind: "section" as const, anchor: { heading_path: point.anchor?.heading_path ?? [] } };
        thread = await open.mutateAsync({
          path: { bundleId },
          body: { ...anchor, addressed_to: "humans", title: point.ask, body: text },
        });
      }
      if (mode === "decide") {
        const last = thread.messages[thread.messages.length - 1];
        if (last) await decide.mutateAsync({ path: { threadId: thread.id }, body: { message_id: last.id } });
      }
    }
    setDone(true);
    onDone();
  };

  if (done)
    return (
      <p className="mt-4 flex items-center gap-1.5 text-sm text-ok">
        <CircleCheck aria-hidden className="size-4" /> Saved.
      </p>
    );
  return (
    <form
      className="mt-4 space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        submit().catch(() => {});
      }}
    >
      <label className="block text-xs font-medium text-ink-2">
        {p.label}
        <Textarea
          rows={4}
          className="mt-1 font-sans text-sm"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={p.placeholder}
          autoFocus
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) e.currentTarget.form?.requestSubmit();
          }}
        />
      </label>
      {error ? <ErrorState message={problemMessage(error)} /> : null}
      <div className="flex justify-end gap-1.5">
        <Button size="sm" onClick={onCancel}>
          Cancel
        </Button>
        <Button size="sm" variant="primary" type="submit" disabled={busy || text.trim().length < p.min}>
          {p.submit}
        </Button>
      </div>
    </form>
  );
}
