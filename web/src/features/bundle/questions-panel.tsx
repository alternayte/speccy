import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { ChevronRight, CircleCheck, CircleHelp, Split } from "lucide-react";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Anchor, BuildQuestion } from "@/lib/api";
import { listQuestionsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

const resultStyle = {
  agree: { icon: CircleCheck, tone: "text-ok", text: "Readers agree" },
  diverge: { icon: Split, tone: "text-bad", text: "Readers disagree" },
  gap: { icon: CircleHelp, tone: "text-warn", text: "Not in the doc" },
} as const;

// QuestionsPanel lists the build questions of the last full review, with each reader's
// answer and the result (REQ-040 to REQ-045). Readers are named by number (DEC-013).
export function QuestionsPanel({ runId, onOpen }: { runId?: string; onOpen: (a: Anchor) => void }) {
  const questions = useQuery({ ...listQuestionsOptions({ path: { runId: runId ?? "" } }), enabled: !!runId });
  if (!runId) {
    return (
      <p className="px-3 py-3 text-xs text-ink-3">
        Run a full review. Three readers then answer build questions from the doc alone.
      </p>
    );
  }
  if (questions.isPending) return <Loading label="Loading build questions" />;
  if (questions.isError) {
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(questions.error)} />
      </div>
    );
  }
  // Questions that need an answer in the doc come first, in their order.
  const rank = { diverge: 0, gap: 0, agree: 1 } as const;
  const items = [...questions.data.items].sort((a, b) => rank[a.result] - rank[b.result] || a.number - b.number);
  if (items.length === 0) {
    return <Empty title="No build questions">This review asked the readers no build questions.</Empty>;
  }
  const open = items.filter((q) => q.result !== "agree").length;
  return (
    <div>
      <p className="px-3 pt-3 pb-1 text-xs text-ink-2">
        {open === 0
          ? `The readers agree on all ${items.length} questions.`
          : `${open} of ${items.length} questions need an answer in the doc.`}
      </p>
      <ul className="divide-y divide-line">
        {items.map((q) => (
          <QuestionRow key={q.id} question={q} onOpen={onOpen} />
        ))}
      </ul>
    </div>
  );
}

function QuestionRow({ question: q, onOpen }: { question: BuildQuestion; onOpen: (a: Anchor) => void }) {
  const { icon: Icon, tone, text } = resultStyle[q.result];
  const groupOf = (reader: number) => q.groups.findIndex((g) => g.includes(reader));
  const cites = q.cites.map((c) => (c.kind === "trace" ? c.id : c.path?.join(" › "))).join(", ");
  return (
    <li className="px-3 py-2.5">
      <button type="button" onClick={() => onOpen(q.anchor)} className="block w-full text-left">
        <span className={clsx("inline-flex items-center gap-1 text-2xs font-semibold tracking-wide", tone)}>
          <Icon aria-hidden className="size-3.5" />
          {text}
          <span className="ml-1 font-mono text-ink-3">{q.level}</span>
        </span>
        <span className="mt-1 block text-sm text-ink">{q.text}</span>
        {cites ? <span className="mt-0.5 block text-xs text-ink-3">Cites {cites}</span> : null}
      </button>
      <details className="group mt-1.5">
        <summary className="inline-flex cursor-pointer list-none items-center gap-1 text-xs text-ink-2 hover:text-ink">
          <ChevronRight aria-hidden className="size-3 transition-transform group-open:rotate-90" />
          Reader answers
        </summary>
        <ol className="mt-1.5 space-y-2">
          {q.answers.map((a) => {
            const g = groupOf(a.reader);
            return (
              <li key={a.reader} className="rounded-md bg-sunken px-2.5 py-2 text-xs">
                <span className="font-semibold text-ink-2">
                  Reader {a.reader}
                  {q.groups.length > 1 && g >= 0 ? (
                    <span className="ml-1 font-normal text-ink-3">· meaning {String.fromCharCode(65 + g)}</span>
                  ) : null}
                </span>
                <p className={clsx("mt-0.5", a.answered ? "text-ink" : "text-ink-3")}>{a.answer}</p>
                {!a.answered && a.quotes.length > 0 ? (
                  <p className="mt-0.5 text-ink-3">No quote is in the doc, so this counts as not specified.</p>
                ) : null}
                {a.quotes.length ? (
                  <ul className="mt-1 space-y-0.5">
                    {a.quotes.map((quote, i) => (
                      <li key={i} className={clsx("border-l-2 pl-2", quote.found ? "border-line" : "border-bad")}>
                        <span className={quote.found ? "text-ink-2" : "text-ink-3 line-through"}>“{quote.text}”</span>
                        {quote.found ? null : <span className="sr-only"> (not found in the doc)</span>}
                      </li>
                    ))}
                  </ul>
                ) : null}
              </li>
            );
          })}
        </ol>
      </details>
    </li>
  );
}
