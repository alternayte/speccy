import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import {
  coverTraceIdMutation,
  getSpecDocOptions,
  getTraceOptions,
  listBundlesOptions,
  requestWaiverMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

type Answer = "here" | "other" | "out";

// minReason is the waiver mechanism's shortest reason (REQ-072).
const minReason = 20;

const answers: { key: Answer; label: string }[] = [
  { key: "here", label: "This doc covers it" },
  { key: "other", label: "Another doc covers it" },
  { key: "out", label: "Out of scope" },
];

// GapAnswer answers a coverage gap in one of three ways. "This doc covers it" adds the line
// "Covers <ID>." at the end of a section, as a new version, so the ID sits where a builder
// reads it. "Another doc covers it" and "Out of scope" are Acknowledgements: they follow the
// profile's waiver policy, and the approval writes the sidecar's trace entry.
export function GapAnswer({
  docId,
  findingId,
  traceId,
  onDone,
  className,
  question = true,
}: {
  docId: string;
  findingId: string;
  traceId: string;
  onDone: (message: string) => void;
  className?: string;
  // question shows "Does this doc cover …?". The tour asks it in its heading already.
  question?: boolean;
}) {
  const qc = useQueryClient();
  const [answer, setAnswer] = useState<Answer>();
  const [section, setSection] = useState("");
  const [target, setTarget] = useState("");
  const [reason, setReason] = useState("");
  const doc = useQuery(getSpecDocOptions({ path: { docId } }));
  const trace = useQuery({ ...getTraceOptions({ path: { docId } }), enabled: answer === "here" });
  const bundles = useQuery({ ...listBundlesOptions({ query: { limit: 100 } }), enabled: answer === "other" });
  const finish = (message: string) => {
    qc.invalidateQueries();
    onDone(message);
  };
  const cover = useMutation({
    ...coverTraceIdMutation(),
    onSuccess: (r) => finish(`Added "Covers ${traceId}." as version ${r.version.number}. The gap is closed.`),
  });
  const ack = useMutation({
    ...requestWaiverMutation(),
    // A request never approves itself, so the gap stays open until someone approves it.
    onSuccess: () => finish(`Asked for approval of ${traceId}. The gap closes when it is approved.`),
  });
  const sections = trace.data?.sections ?? [];
  const others = (bundles.data?.items ?? []).flatMap((b) => b.docs).filter((d) => d.id !== docId);
  const error = cover.error ?? ack.error;
  const reasonOk = reason.trim().length >= minReason;
  const ready =
    answer === "here" ? section !== "" : answer === "other" ? target !== "" && reasonOk : answer === "out" && reasonOk;
  const submit = () => {
    if (answer === "here") {
      const path = sections[Number(section)];
      if (!path || !doc.data) return;
      cover.mutate({
        path: { docId },
        query: { base_version: doc.data.current_version.id },
        body: { trace_id: traceId, section: path },
      });
      return;
    }
    ack.mutate({
      path: { docId },
      body: {
        finding_id: findingId,
        reason: reason.trim(),
        trace: answer === "other" ? { status: "covered_by", target } : { status: "out_of_scope" },
      },
    });
  };
  return (
    <div className={clsx("rounded-md border border-line bg-sunken p-2 text-xs", className)}>
      <fieldset>
        <legend className={question ? "text-ink-2" : "sr-only"}>Does this doc cover {traceId}?</legend>
        <div className={clsx("flex flex-wrap gap-1", question && "mt-1.5")}>
          {answers.map((a) => (
            <label
              key={a.key}
              className={clsx(
                "cursor-pointer rounded-sm border px-2 py-1",
                answer === a.key
                  ? "border-accent bg-accent-soft text-ink"
                  : "border-line-strong text-ink-2 hover:text-ink",
              )}
            >
              <input
                type="radio"
                name={`gap-${findingId}`}
                className="sr-only"
                checked={answer === a.key}
                onChange={() => setAnswer(a.key)}
              />
              {a.label}
            </label>
          ))}
        </div>
      </fieldset>
      {answer === "here" ? (
        <label className="mt-2 block text-ink-2">
          The section that covers it
          <select
            value={section}
            onChange={(e) => setSection(e.target.value)}
            className="mt-1 block h-7 w-full rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
          >
            <option value="">Pick a section</option>
            {sections.map((p, i) => (
              <option key={p.join(" › ")} value={i}>
                {p.join(" › ")}
              </option>
            ))}
          </select>
          {section !== "" ? (
            <span className="mt-1 block text-ink-3">
              Speccy adds <code className="text-ink">Covers {traceId}.</code> at the end of this section.
            </span>
          ) : null}
        </label>
      ) : null}
      {answer === "other" ? (
        <label className="mt-2 block text-ink-2">
          The doc that covers it
          <select
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            className="mt-1 block h-7 w-full rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
          >
            <option value="">Pick a doc</option>
            {others.map((d) => (
              <option key={d.id} value={d.slug}>
                {d.title} ({d.slug})
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {answer === "other" || answer === "out" ? (
        <label className="mt-2 block text-ink-2">
          Reason
          <Textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={2}
            placeholder={
              answer === "out" ? "Why this doc does not cover it." : "What the other doc does for this requirement."
            }
            className="mt-1 text-xs"
          />
          <span className="mt-1 block text-ink-3">
            At least {minReason} characters. The profile's waiver policy decides who approves it.
          </span>
        </label>
      ) : null}
      {error ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(error)} />
        </div>
      ) : null}
      {answer ? (
        <div className="mt-2 flex justify-end">
          <Button size="sm" variant="primary" disabled={!ready || cover.isPending || ack.isPending} onClick={submit}>
            {answer === "here" ? "Add the line" : "Ask for approval"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
