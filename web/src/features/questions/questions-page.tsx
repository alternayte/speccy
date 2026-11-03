import { useBundleId } from "@/features/bundle/params";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft, Check, Loader2, Sparkles } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { TextDiff } from "@/features/bundle/text-diff";
import { useActiveRun } from "@/features/bundle/run-review";
import {
  acceptFixes,
  type Finding,
  type FixSuggestion,
  type Material,
  requestWaiver,
  startRun,
  type StreamedQuestion,
  suggestAnswers,
  suggestFix,
} from "@/lib/api";
import { getSpecDocOptions, listFindingsOptions } from "@/lib/api/@tanstack/react-query.gen";
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

// Question is one blocking build question: a saved finding, or a question the running review
// streamed before it saved its finding.
type Question = {
  fork: string;
  question: string;
  material?: Material;
  section: string[];
  finding?: Finding;
};

// blocking says whether a finding is an open blocking build question: a material gap, or a
// MUST answer finding that the judge has not read yet.
function blocking(f: Finding): boolean {
  if (f.level !== "MUST" || f.waived || f.unanswered) return false;
  if (f.upstream_change && !f.upstream_change.blocks) return false;
  if (f.material) return f.material.class !== "none";
  return f.fix_kind === "answer";
}

function answerOf(q: Question, c?: Choice): string {
  if (!c) return "";
  if (c.kind === "option") return q.material?.options?.[c.option - 1] ?? "";
  if (c.kind === "other") return c.text.trim();
  return "";
}

// QuestionsPage is the questions screen (material gate): every open blocking build question of
// the doc on one screen. Each material gap has two options and a suggested pick; the author
// picks one, writes another, or says it is not needed. Speccy starts writing an answer into the
// doc when the author picks it, so the diffs are ready at submit. Accept saves one version and
// starts a review, which reads only the changed sections.
export function QuestionsPage({ docId }: { docId: string }) {
  const bundleId = useBundleId();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const bundle = useQuery(getSpecDocOptions({ path: { docId } }));
  const runId = bundle.data?.verdict?.run_id;
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: getSpecDocOptions({ path: { docId } }).queryKey });
  }, [qc, docId]);
  const run = useActiveRun(docId, refresh);

  const [choices, setChoices] = useState<Record<string, Choice>>({});
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [step, setStep] = useState<"answer" | "review">("answer");
  const [patches, setPatches] = useState<FixSuggestion[]>([]);
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string>();
  // asked holds the answer each finding's patch was asked for, so a new pick starts a new patch
  // and an old reply does not overwrite it.
  const asked = useRef<Record<string, string>>({});

  // The saved questions first, then the ones the running review found that are not saved yet.
  const saved: Question[] = (findings.data?.items ?? []).filter(blocking).map((f) => ({
    fork: f.fork ?? f.id,
    question: f.material?.question || f.question || f.message,
    material: f.material,
    section: f.anchor.heading_path,
    finding: f,
  }));
  const known = new Set(saved.map((q) => q.fork));
  const streamed: Question[] = [];
  for (const e of run.active ? run.events : []) {
    const s: StreamedQuestion | undefined = e.type === "question" ? e.question : undefined;
    if (!s || known.has(s.fork)) continue;
    known.add(s.fork);
    streamed.push({ fork: s.fork, question: s.question, material: s.material, section: s.heading_path ?? [] });
  }
  const questions = [...saved, ...streamed];

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

  if (bundle.isPending || (runId && findings.isPending)) return <Loading label="Loading the questions" />;
  if (bundle.isError || findings.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(bundle.error ?? findings.error)} />
      </div>
    );

  const writing = saved.filter((q) => q.finding && drafts[q.finding.id]?.state === "writing").length;
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
        <Link
          to="/bundles/$bundleId/docs/$docId"
          params={{ bundleId, docId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> {bundle.data.title}
        </Link>
        <h1 className="text-lg font-semibold tracking-tight">Questions</h1>
        <span className="text-xs text-ink-2">
          {questions.length === 0
            ? "Nothing blocks Build Ready"
            : `${questions.length} question${questions.length === 1 ? "" : "s"} block${questions.length === 1 ? "s" : ""} Build Ready`}
        </span>
        {run.active ? (
          <span className="inline-flex items-center gap-1 text-xs text-ink-3">
            <Loader2 aria-hidden className="size-3.5 animate-spin text-accent" /> The review is still running
          </span>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[760px] px-4 py-5 sm:px-6">
          {step === "review" ? (
            <ReviewPatches patches={patches} />
          ) : questions.length === 0 ? (
            <Empty title="No open questions">
              {runId
                ? "A builder would not have to guess anything that changes the data model, an interface, security or what the user sees."
                : "Run a review first. The questions screen lists what a builder would have to guess."}
            </Empty>
          ) : (
            <ol className="space-y-4">
              {questions.map((q, i) => (
                <QuestionCard
                  key={q.fork}
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
              ))}
            </ol>
          )}
        </div>
      </div>

      {questions.length > 0 || step === "review" ? (
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
      ) : null}
    </div>
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
