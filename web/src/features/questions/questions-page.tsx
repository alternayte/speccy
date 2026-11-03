import { useBundleId } from "@/features/bundle/params";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import {
  ArrowLeft,
  Check,
  CircleCheck,
  ExternalLink,
  Loader2,
  Lock,
  MessageSquare,
  RotateCw,
  Sparkles,
} from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { TextDiff } from "@/features/bundle/text-diff";
import { useActiveRun } from "@/features/bundle/run-review";
import {
  type Ask,
  contradictionSlug,
  downstreamSlug,
  fixText,
  SuggestFix,
  upstreamSlug,
  useDecide,
  WaiverCard,
  WaiverDialog,
} from "@/features/bundle/findings-panel";
import { checkDocsURL } from "@/lib/docs";
import { useMe } from "@/features/account/me";
import { useReviewerMode } from "@/features/review/mode";
import { ThreadView } from "@/features/threads/threads-panel";
import { GapAnswer } from "@/features/trace/gap-answer";
import { MarkStandalone } from "@/features/trace/mark-standalone";
import {
  acceptFixes,
  type Finding,
  type FixSuggestion,
  type Material,
  type QuestionItem,
  requestWaiver,
  startRun,
  type StreamedQuestion,
  suggestAnswers,
  suggestFix,
} from "@/lib/api";
import {
  getQuestionsOptions,
  getQuestionsQueryKey,
  getSpecDocOptions,
  openBundleThreadMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// classLabel names what the two options of a material gap differ in.
const classLabel: Record<Material["class"], string> = {
  data_model: "Data model",
  interface: "Interface",
  security: "Security",
  user_visible: "What the user sees",
  none: "Advice",
};

// minReason is the shortest reason for "Not needed" that the server takes.
const minReason = 10;

// Choice is the author's answer to one question.
type Choice =
  { kind: "option"; option: 1 | 2 } | { kind: "other"; text: string } | { kind: "not_needed"; reason: string };

// Draft is where the patch of one answer stands. The patch starts when the author picks.
type Draft = { answer: string; state: "writing" | "ready" | "failed"; error?: string };

// Question is one material gap: a saved finding, or a question the running review streamed
// before it saved its finding.
type Question = {
  fork: string;
  question: string;
  material?: Material;
  section: string[];
  finding?: Finding;
};

function answerOf(q: Question, c?: Choice): string {
  if (!c) return "";
  if (c.kind === "option") return q.material?.options?.[c.option - 1] ?? "";
  if (c.kind === "other") return c.text.trim();
  return "";
}

// QuestionsPage is the questions screen: everything that blocks Build Ready and needs a person,
// in doc order, from the server's list. A material gap has two options and a suggested pick; the
// author picks one, writes another, or says it is not needed. Speccy starts writing an answer
// into the doc when the author picks it, so the diffs are ready at submit. Accept saves one
// version and starts a review, which reads only the changed sections. A blocking thread takes a
// reply, and a blocking finding takes the answer the rail gives it. A reviewer sees the same list
// with no answer controls and comments on each item; an approver also decides the waivers that
// wait for them here.
export function QuestionsPage({ docId }: { docId: string }) {
  const bundleId = useBundleId();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const mode = useReviewerMode();
  // A reviewer, or a guest, cannot write to the doc: they see the items and comment on them.
  const canAnswer = !mode.reviewer && !me.data?.guest;
  const bundle = useQuery(getSpecDocOptions({ path: { docId } }));
  const list = useQuery(getQuestionsOptions({ path: { docId } }));
  const runId = list.data?.run_id;
  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: getSpecDocOptions({ path: { docId } }).queryKey });
    qc.invalidateQueries({ queryKey: getQuestionsQueryKey({ path: { docId } }) });
  }, [qc, docId]);
  const run = useActiveRun(docId, refresh);
  const decide = useDecide(docId);

  const [choices, setChoices] = useState<Record<string, Choice>>({});
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [step, setStep] = useState<"answer" | "review">("answer");
  const [patches, setPatches] = useState<FixSuggestion[]>([]);
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<{ text: string; bad?: boolean }>();
  const [waiving, setWaiving] = useState<Ask>();
  // decided holds the waivers this person decided here, so the card stays and shows the result.
  const [decided, setDecided] = useState<string[]>([]);
  // asked holds the answer each finding's patch was asked for, so a new pick starts a new patch
  // and an old reply does not overwrite it.
  const asked = useRef<Record<string, string>>({});

  const items = list.data?.items ?? [];
  // The saved material gaps, then the ones the running review found that are not saved yet.
  const saved: Question[] = items
    .filter((it) => it.kind === "material_gap" && it.finding)
    .map((it) => ({
      fork: it.key,
      question: it.ask,
      material: it.finding!.material,
      section: it.finding!.anchor.heading_path,
      finding: it.finding,
    }));
  const known = new Set(items.map((it) => it.key));
  const streamed: Question[] = [];
  for (const e of run.active ? run.events : []) {
    const s: StreamedQuestion | undefined = e.type === "question" ? e.question : undefined;
    if (!s || known.has(s.fork)) continue;
    known.add(s.fork);
    streamed.push({ fork: s.fork, question: s.question, material: s.material, section: s.heading_path ?? [] });
  }
  const gapOf = new Map(saved.map((q) => [q.fork, q]));

  // write starts the patch of one answer at once, so the author does not wait at submit.
  const write = (q: Question, c: Choice) => {
    const f = q.finding;
    const answer = answerOf(q, c);
    const was = f ? drafts[f.id] : undefined;
    if (!f || !runId || !answer || (was?.answer === answer && was.state !== "failed")) return;
    asked.current[f.id] = answer;
    setDrafts((d) => ({ ...d, [f.id]: { answer, state: "writing" } }));
    suggestFix({
      path: { runId, findingId: f.id },
      body: { answer, option: c.kind === "option" ? c.option : 0 },
      throwOnError: true,
    }).then(
      () => {
        if (asked.current[f.id] !== answer) return;
        setDrafts((d) => ({ ...d, [f.id]: { answer, state: "ready" } }));
      },
      (err: unknown) => {
        if (asked.current[f.id] !== answer) return;
        setDrafts((d) => ({ ...d, [f.id]: { answer, state: "failed", error: problemMessage(err) } }));
      },
    );
  };
  const choose = (q: Question, c: Choice, start: boolean) => {
    setChoices((x) => ({ ...x, [q.fork]: c }));
    if (start) write(q, c);
  };

  // useSuggested picks the suggested option of every question that has no answer yet.
  const unpicked = saved.filter((q) => !choices[q.fork] && q.material?.pick);
  const useSuggested = () => {
    for (const q of unpicked) choose(q, { kind: "option", option: q.material!.pick as 1 | 2 }, true);
  };

  const answered = saved.filter((q) => {
    const c = choices[q.fork];
    if (!c) return false;
    if (c.kind === "not_needed") return c.reason.trim().length >= minReason;
    return answerOf(q, c) !== "";
  });

  const submit = async () => {
    if (!runId) return;
    setError(undefined);
    setBusy("Writing the answers into the doc");
    try {
      for (const q of answered) {
        const c = choices[q.fork];
        if (c?.kind !== "not_needed" || !q.finding) continue;
        await requestWaiver({
          path: { docId },
          body: { finding_id: q.finding.id, reason: c.reason.trim(), not_needed: true },
          throwOnError: true,
        });
      }
      const answers = answered
        .filter((q) => choices[q.fork]?.kind !== "not_needed" && q.finding)
        .map((q) => {
          const c = choices[q.fork]!;
          return { finding_id: q.finding!.id, answer: answerOf(q, c), option: c.kind === "option" ? c.option : 0 };
        });
      if (answers.length === 0) {
        await done(false);
        return;
      }
      const res = await suggestAnswers({ path: { runId }, body: { answers }, throwOnError: true });
      setPatches(res.data.patches);
      setStep("review");
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(undefined);
    }
  };

  const accept = async () => {
    if (!runId) return;
    setError(undefined);
    setBusy("Saving the answers");
    try {
      await acceptFixes({
        path: { runId },
        body: { finding_ids: patches.map((p) => p.finding_id) },
        throwOnError: true,
      });
      await done(true);
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(undefined);
    }
  };

  // done starts a review of the new version and goes back to the doc. The review reads only
  // the sections that changed, so its round has fewer questions.
  const done = async (changed: boolean) => {
    if (changed) await startRun({ path: { docId }, throwOnError: true });
    refresh();
    navigate({ to: "/bundles/$bundleId/docs/$docId", params: { bundleId, docId } });
  };

  const recheck = async () => {
    setError(undefined);
    try {
      await startRun({ path: { docId }, throwOnError: true });
      refresh();
    } catch (err) {
      setError(problemMessage(err));
    }
  };

  if (bundle.isPending || list.isPending || mode.pending) return <Loading label="Loading the questions" />;
  if (bundle.isError || list.isError)
    return (
      <div className="mx-auto max-w-[760px] p-6">
        <ErrorState message={problemMessage(bundle.error ?? list.error)} />
      </div>
    );

  const waivers = list.data.waivers;
  const count = items.length + streamed.length;
  const writing = saved.filter((q) => q.finding && drafts[q.finding.id]?.state === "writing").length;
  const answerable = canAnswer && saved.length > 0;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
        <Link
          to="/bundles/$bundleId/docs/$docId"
          params={{ bundleId, docId }}
          search={true}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> {bundle.data.title}
        </Link>
        <h1 className="text-lg font-semibold tracking-tight">Questions</h1>
        <span className="text-xs text-ink-2">
          {count === 0
            ? "Nothing blocks Build Ready"
            : `${count} question${count === 1 ? "" : "s"} block${count === 1 ? "s" : ""} Build Ready`}
        </span>
        {run.active ? (
          <span className="inline-flex items-center gap-1 text-xs text-ink-3">
            <Loader2 aria-hidden className="size-3.5 animate-spin text-accent" /> The review is still running
          </span>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[760px] space-y-4 px-4 py-5 sm:px-6">
          {notice ? (
            <p role="status" className={clsx("text-sm", notice.bad ? "text-bad" : "text-ok")}>
              {notice.text}
            </p>
          ) : null}
          {list.data.recheck && canAnswer && !run.active ? (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-warn/40 bg-warn-soft px-4 py-3 text-sm">
              <p className="min-w-0 flex-1 text-ink">
                The reviewer gave no answer for a MUST check. Another review answers it.
              </p>
              {canAnswer ? (
                <Button size="sm" icon={<RotateCw className="size-3.5" />} onClick={recheck}>
                  Check again
                </Button>
              ) : null}
            </div>
          ) : null}
          {waivers.length ? (
            <section aria-label="Waivers that wait for you" className="space-y-2">
              <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
                Waiting for your decision
              </h2>
              {waivers.map((w) => (
                <div key={w.id} className="rounded-lg border border-line bg-surface pt-3">
                  <p className="px-4 pb-2 font-mono text-2xs text-ink-3">{w.check_slug}</p>
                  <WaiverCard
                    waiver={w}
                    waivers={waivers}
                    viaPullRequest={bundle.data.source_kind === "github"}
                    decide={decide}
                    onDecided={() => {
                      setDecided((d) => [...d, w.id]);
                      refresh();
                    }}
                    next={[]}
                    onNext={() => {}}
                  />
                </div>
              ))}
            </section>
          ) : null}
          {step === "review" ? (
            <ReviewPatches patches={patches} />
          ) : count === 0 ? (
            <Empty title="No open questions">
              {runId
                ? "Nothing blocks Build Ready that needs a person: no material gap, no blocking thread, and no blocking finding."
                : "Run a review first. The questions screen lists what a builder would have to guess."}
            </Empty>
          ) : (
            <ol className="space-y-4">
              {items.map((it, i) => {
                const q = gapOf.get(it.key);
                if (q && canAnswer)
                  return (
                    <QuestionCard
                      key={it.key}
                      n={i + 1}
                      q={q}
                      choice={choices[q.fork]}
                      draft={q.finding ? drafts[q.finding.id] : undefined}
                      onChoose={(c, start) => choose(q, c, start)}
                      onWrite={() => {
                        const c = choices[q.fork];
                        if (c) write(q, c);
                      }}
                    />
                  );
                return (
                  <ItemCard
                    key={it.key}
                    n={i + 1}
                    item={it}
                    docId={docId}
                    canAnswer={canAnswer}
                    member={!me.data?.guest}
                    onAsk={setWaiving}
                    onDone={(text, bad) => {
                      setNotice({ text, bad });
                      refresh();
                    }}
                  />
                );
              })}
              {streamed.map((q, i) => (
                <ReadOnlyGap key={q.fork} n={items.length + i + 1} q={q} />
              ))}
            </ol>
          )}
          {decided.length ? <span className="sr-only">{decided.length} decided</span> : null}
        </div>
      </div>

      {answerable || step === "review" ? (
        <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-t border-line bg-surface px-4 py-3 sm:px-6">
          <span className="text-xs text-ink-2" role="status" aria-live="polite">
            {busy ??
              (step === "review"
                ? `${patches.length} change${patches.length === 1 ? "" : "s"} to the doc. Accept saves one version and checks the changed sections.`
                : `${answered.length} of ${saved.length} answered${writing ? ` · writing ${writing} into the doc` : ""}`)}
          </span>
          {error ? <span className="text-xs text-bad">{error}</span> : null}
          <div className="ml-auto flex gap-2">
            {step === "review" ? (
              <>
                <Button disabled={!!busy} onClick={() => setStep("answer")}>
                  Back
                </Button>
                <Button variant="primary" disabled={!!busy} onClick={accept}>
                  Accept and check again
                </Button>
              </>
            ) : (
              <>
                {unpicked.length ? (
                  <Button disabled={!!busy} onClick={useSuggested}>
                    Use the suggested answer{unpicked.length === 1 ? "" : "s"}
                  </Button>
                ) : null}
                <Button variant="primary" disabled={!!busy || answered.length === 0} onClick={submit}>
                  {busy ? <Loader2 aria-hidden className="size-3.5 animate-spin" /> : null}
                  Write {answered.length || ""} answer{answered.length === 1 ? "" : "s"}
                </Button>
              </>
            )}
          </div>
        </div>
      ) : error ? (
        <p className="shrink-0 border-t border-line bg-surface px-4 py-2 text-xs text-bad">{error}</p>
      ) : null}
      <WaiverDialog
        docId={docId}
        ask={waiving}
        onClose={() => {
          setWaiving(undefined);
          refresh();
        }}
      />
    </div>
  );
}

// kindLabel names an item that is not an answerable material gap.
const kindLabel: Record<QuestionItem["kind"], string> = {
  material_gap: "Build question",
  blocking_thread: "Blocking thread",
  decision: "Needs a decision",
  finding: "Blocking finding",
};

// ItemCard is one item that the author answers with the rail's action for it, or that a
// reviewer reads and comments on. A material gap shows here for a reviewer, with its options
// and no controls.
function ItemCard({
  n,
  item: it,
  docId,
  canAnswer,
  member,
  onAsk,
  onDone,
}: {
  n: number;
  item: QuestionItem;
  docId: string;
  canAnswer: boolean;
  member: boolean;
  onAsk: (a: Ask) => void;
  onDone: (text: string, bad?: boolean) => void;
}) {
  const f = it.finding;
  const [open, setOpen] = useState<"gap" | "standalone" | "comment">();
  const docs = f ? checkDocsURL(f.check_slug) : undefined;
  const section = it.anchor?.heading_path ?? [];
  const m = f?.material && f.material.class !== "none" ? f.material : undefined;
  return (
    <li className="rounded-lg border border-line bg-surface">
      <div className="p-4">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="font-mono text-xs text-ink-3">{n}</span>
          <p className="min-w-0 flex-1 text-sm font-medium text-ink">
            <InlineCode text={it.ask} />
          </p>
        </div>
        <p className="mt-1 flex flex-wrap items-center gap-x-1 text-xs text-ink-3">
          {it.kind === "blocking_thread" ? <Lock aria-hidden className="size-3" /> : null}
          {m ? classLabel[m.class] : kindLabel[it.kind]}
          {f ? <span className="font-mono">· {f.check_slug}</span> : null}
          {section.length ? <span>· {section.join(" › ")}</span> : null}
        </p>
        {it.context && !it.thread ? <p className="mt-2 text-sm text-ink-2">{it.context}</p> : null}
        {m?.options?.length ? (
          <ul className="mt-3 space-y-1.5">
            {m.options.map((text, i) => (
              <li key={i} className="flex items-start gap-2 rounded-md border border-line px-3 py-2 text-sm">
                <span className="font-mono text-xs text-ink-3">{i + 1}</span>
                <span className="min-w-0 flex-1 text-ink">
                  <InlineCode text={text} />
                </span>
                {m.pick === i + 1 ? (
                  <span className="inline-flex shrink-0 items-center gap-1 text-xs text-ink-2">
                    <Sparkles aria-hidden className="size-3" /> Suggested
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        ) : null}
        {/* A gap's anchor is the frontmatter, which says nothing about the gap. */}
        {f && f.anchor.quote.trim() && !f.trace_id && it.kind !== "material_gap" ? (
          <p className="mt-2 line-clamp-3 border-l-2 border-line-strong pl-2 font-mono text-xs text-ink-2">
            {f.anchor.quote}
          </p>
        ) : null}
      </div>

      {it.thread ? (
        <div className="border-t border-line">
          <ThreadView threadId={it.thread.id} docId={docId} member={member && canAnswer} />
        </div>
      ) : null}

      {f && canAnswer ? (
        <FindingActions finding={f} docId={docId} open={open} setOpen={setOpen} onAsk={onAsk} onDone={onDone} />
      ) : null}

      {f && !canAnswer ? (
        open === "comment" ? (
          <CommentForm item={it} docId={docId} onCancel={() => setOpen(undefined)} />
        ) : (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line px-4 py-2.5 text-xs">
            <button
              type="button"
              onClick={() => setOpen("comment")}
              className="inline-flex items-center gap-1 font-medium text-accent hover:underline"
            >
              <MessageSquare aria-hidden className="size-3.5" /> Comment
            </button>
            {docs ? <DocsLink href={docs} /> : null}
          </div>
        )
      ) : null}
    </li>
  );
}

// FindingActions are the answers the rail gives a blocking finding: a coverage gap takes one
// of its three answers, a missing upstream link takes a link or the standalone answer, a
// conflict takes "the other doc must change", and every other finding takes a fix or a waiver.
function FindingActions({
  finding: f,
  docId,
  open,
  setOpen,
  onAsk,
  onDone,
}: {
  finding: Finding;
  docId: string;
  open?: "gap" | "standalone" | "comment";
  setOpen: (o?: "gap" | "standalone" | "comment") => void;
  onAsk: (a: Ask) => void;
  onDone: (text: string, bad?: boolean) => void;
}) {
  const docs = checkDocsURL(f.check_slug);
  if (f.trace_id)
    return (
      <div className="border-t border-line px-4 py-3">
        <GapAnswer docId={docId} findingId={f.id} traceId={f.trace_id} question={false} onDone={(t) => onDone(t)} />
      </div>
    );
  const upstream = f.check_slug === upstreamSlug;
  return (
    <div className="border-t border-line pt-3">
      <SuggestFix
        runId={f.run_id}
        docId={docId}
        finding={f}
        onAccepted={(r) => {
          const t = fixText(r);
          onDone(t.text, t.bad);
        }}
        aside={
          <>
            {upstream ? (
              <button
                type="button"
                onClick={() => setOpen(open === "standalone" ? undefined : "standalone")}
                className="text-xs text-ink-2 hover:text-ink"
              >
                Mark it standalone
              </button>
            ) : (
              <button type="button" onClick={() => onAsk({ finding: f })} className="text-xs text-ink-2 hover:text-ink">
                Ask for a waiver
              </button>
            )}
            {f.check_slug === contradictionSlug && !f.upstream_change ? (
              <button
                type="button"
                onClick={() => onAsk({ finding: f, kind: "upstream" })}
                className="text-xs text-ink-2 hover:text-ink"
              >
                The linked doc must change
              </button>
            ) : null}
            {f.check_slug === downstreamSlug ? (
              <button
                type="button"
                onClick={() => onAsk({ finding: f, kind: "send_back" })}
                className="text-xs text-ink-2 hover:text-ink"
              >
                The downstream doc must change
              </button>
            ) : null}
            {docs ? <DocsLink href={docs} /> : null}
          </>
        }
      />
      {upstream && open === "standalone" ? (
        <MarkStandalone
          className="mx-4 mb-3"
          docId={docId}
          findingId={f.id}
          onCancel={() => setOpen(undefined)}
          onDone={(t) => {
            setOpen(undefined);
            onDone(t);
          }}
        />
      ) : null}
    </div>
  );
}

function DocsLink({ href }: { href: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
    >
      What this check means <ExternalLink aria-hidden className="size-3" />
    </a>
  );
}

// CommentForm opens a thread on an item's finding, so the author sees it on that finding.
function CommentForm({ item, docId, onCancel }: { item: QuestionItem; docId: string; onCancel: () => void }) {
  const qc = useQueryClient();
  const [body, setBody] = useState("");
  const post = useMutation({
    ...openBundleThreadMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: getQuestionsQueryKey({ path: { docId } }) }),
  });
  const f = item.finding!;
  if (post.isSuccess)
    return (
      <p className="flex items-center gap-1.5 border-t border-line px-4 py-2.5 text-xs text-ok">
        <CircleCheck aria-hidden className="size-3.5" /> Posted. The author sees your comment on this finding.
      </p>
    );
  return (
    <form
      className="space-y-2 border-t border-line px-4 py-3"
      onSubmit={(e) => {
        e.preventDefault();
        post.mutate({
          path: { docId },
          body: {
            anchor_kind: "finding",
            anchor: { finding_id: f.id, check_slug: f.check_slug },
            addressed_to: "humans",
            title: item.ask.slice(0, 200),
            body: body.trim(),
          },
        });
      }}
    >
      <Textarea
        aria-label="Comment"
        rows={3}
        autoFocus
        className="font-sans text-sm"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder="Tell the author which answer is right, or what is missing."
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) e.currentTarget.form?.requestSubmit();
        }}
      />
      {post.isError ? <ErrorState message={problemMessage(post.error)} /> : null}
      <div className="flex justify-end gap-1.5">
        <Button size="sm" type="button" onClick={onCancel}>
          Cancel
        </Button>
        <Button size="sm" variant="primary" type="submit" disabled={post.isPending || !body.trim()}>
          Post
        </Button>
      </div>
    </form>
  );
}

// ReadOnlyGap is a material gap the running review streamed before it saved its finding.
function ReadOnlyGap({ n, q }: { n: number; q: Question }) {
  return (
    <li className="rounded-lg border border-dashed border-line bg-surface p-4">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span className="font-mono text-xs text-ink-3">{n}</span>
        <p className="min-w-0 flex-1 text-sm font-medium text-ink">
          <InlineCode text={q.question} />
        </p>
      </div>
      <p className="mt-1 text-xs text-ink-3">
        {q.material && q.material.class !== "none" ? classLabel[q.material.class] : "Needs a fact"}
        {q.section.length ? ` · ${q.section.join(" › ")}` : ""} · the review is still saving this question
      </p>
    </li>
  );
}

function QuestionCard({
  n,
  q,
  choice,
  draft,
  onChoose,
  onWrite,
}: {
  n: number;
  q: Question;
  choice?: Choice;
  draft?: Draft;
  onChoose: (c: Choice, start: boolean) => void;
  onWrite: () => void;
}) {
  const name = `q-${q.fork}`;
  const options = q.material?.options ?? [];
  const pick = q.material?.pick;
  const pending = !q.finding;
  return (
    <li className="rounded-lg border border-line bg-surface">
      <fieldset className="p-4">
        <legend className="sr-only">Question {n}</legend>
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="font-mono text-xs text-ink-3">{n}</span>
          <p className="min-w-0 flex-1 text-sm font-medium text-ink">{q.question}</p>
        </div>
        <p className="mt-1 text-xs text-ink-3">
          {q.material && q.material.class !== "none" ? classLabel[q.material.class] : "Needs a fact"}
          {q.section.length ? ` · ${q.section.join(" › ")}` : ""}
          {pending ? " · the review is still saving this question" : ""}
        </p>
        <div className="mt-3 space-y-1.5">
          {options.map((text, i) => {
            const option = (i + 1) as 1 | 2;
            const on = choice?.kind === "option" && choice.option === option;
            return (
              <label
                key={option}
                className={clsx(
                  "flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm",
                  on ? "border-accent bg-accent-soft" : "border-line hover:border-line-strong",
                )}
              >
                <input
                  type="radio"
                  name={name}
                  className="mt-1"
                  checked={on}
                  onChange={() => onChoose({ kind: "option", option }, true)}
                />
                <span className="min-w-0 flex-1 text-ink">
                  <InlineCode text={text} />
                </span>
                {pick === option ? (
                  <span className="inline-flex shrink-0 items-center gap-1 text-xs text-ink-2">
                    <Sparkles aria-hidden className="size-3" /> Suggested
                  </span>
                ) : null}
              </label>
            );
          })}
          <label
            className={clsx(
              "flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm",
              choice?.kind === "other" ? "border-accent bg-accent-soft" : "border-line hover:border-line-strong",
            )}
          >
            <input
              type="radio"
              name={name}
              className="mt-1"
              checked={choice?.kind === "other"}
              onChange={() => onChoose({ kind: "other", text: choice?.kind === "other" ? choice.text : "" }, false)}
            />
            <span className="min-w-0 flex-1">
              <span className="text-ink">{options.length ? "Other" : "Your answer"}</span>
              {choice?.kind === "other" ? (
                <Textarea
                  value={choice.text}
                  rows={2}
                  autoFocus
                  placeholder="The fact the doc needs, in your own words."
                  className="mt-1.5 font-sans text-sm"
                  onChange={(e) => onChoose({ kind: "other", text: e.target.value }, false)}
                  onBlur={onWrite}
                />
              ) : null}
            </span>
          </label>
          <label
            className={clsx(
              "flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm",
              choice?.kind === "not_needed" ? "border-accent bg-accent-soft" : "border-line hover:border-line-strong",
            )}
          >
            <input
              type="radio"
              name={name}
              className="mt-1"
              checked={choice?.kind === "not_needed"}
              onChange={() =>
                onChoose({ kind: "not_needed", reason: choice?.kind === "not_needed" ? choice.reason : "" }, false)
              }
            />
            <span className="min-w-0 flex-1">
              <span className="text-ink">Not needed</span>
              {choice?.kind === "not_needed" ? (
                <Input
                  value={choice.reason}
                  autoFocus
                  placeholder="Why a builder can pick either answer"
                  className="mt-1.5"
                  onChange={(e) => onChoose({ kind: "not_needed", reason: e.target.value }, false)}
                />
              ) : (
                <span className="block text-xs text-ink-3">Say why. It needs no approval.</span>
              )}
            </span>
          </label>
        </div>
        {draft ? <DraftState draft={draft} /> : null}
      </fieldset>
    </li>
  );
}

// InlineCode shows the `code` spans of an option as code, as the doc would show them.
function InlineCode({ text }: { text: string }) {
  return (
    <>
      {text.split(/(`[^`]+`)/).map((part, i) =>
        part.length > 2 && part.startsWith("`") && part.endsWith("`") ? (
          <code key={i} className="rounded bg-sunken px-1 font-mono text-xs">
            {part.slice(1, -1)}
          </code>
        ) : (
          part
        ),
      )}
    </>
  );
}

function DraftState({ draft }: { draft: Draft }) {
  if (draft.state === "failed") return <p className="mt-2 text-xs text-bad">{draft.error}</p>;
  return (
    <p className="mt-2 inline-flex items-center gap-1 text-xs text-ink-3">
      {draft.state === "writing" ? (
        <>
          <Loader2 aria-hidden className="size-3 animate-spin text-accent" /> Writing the answer into the doc
        </>
      ) : (
        <>
          <Check aria-hidden className="size-3 text-ok" /> Ready to review
        </>
      )}
    </p>
  );
}

function ReviewPatches({ patches }: { patches: FixSuggestion[] }) {
  return (
    <ol className="space-y-4">
      {patches.map((p) => (
        <li key={p.finding_id} className="rounded-lg border border-line bg-surface p-4">
          <p className="text-sm text-ink">{p.explanation}</p>
          <p className="mt-1 font-mono text-2xs text-ink-3">
            {p.file} ·{" "}
            {p.old
              ? p.line === p.end_line
                ? `line ${p.line}`
                : `lines ${p.line} to ${p.end_line}`
              : `new text at line ${p.line}`}
            {p.answers && p.answers.length > 1 ? ` · ${p.answers.length} answers` : ""}
          </p>
          <TextDiff before={p.old} after={p.new} className="mt-2 max-h-80" />
        </li>
      ))}
    </ol>
  );
}
