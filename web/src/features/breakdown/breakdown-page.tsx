import { useBundleId } from "@/features/bundle/params";
import { relativeTime } from "@/features/bundle/time";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Bot,
  ExternalLink,
  GitMerge,
  Loader2,
  MessageSquare,
  RotateCw,
  Scissors,
  Send,
  Sparkles,
  Users,
} from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { Menu, MenuItem } from "@/components/ui/menu";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { useMe } from "@/features/account/me";
import { useReviewerMode } from "@/features/review/mode";
import {
  type Assignee,
  type Breakdown,
  type BreakdownFinding,
  type BreakdownOp,
  type BreakdownProposal,
  checkBreakdownReaders,
  editBreakdown,
  proposeBreakdown,
  pushBreakdown,
  type PushResult,
  type Tracker,
  updateBreakdown,
  type WorkItem,
} from "@/lib/api";
import {
  getBreakdownOptions,
  getBreakdownProposalOptions,
  getBreakdownProposalQueryKey,
  getBreakdownQueryKey,
  getSpecDocOptions,
  getSpecDocQueryKey,
  listBreakdownAssigneesOptions,
  listBreakdownTrackersOptions,
  listHandoffsQueryKey,
  openBundleThreadMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { checkDocsURL } from "@/lib/docs";
import { problemMessage } from "@/lib/problem";

const coverLabel: Record<string, string> = {
  out_of_scope: "Out of scope",
  covered_elsewhere: "Covered elsewhere",
  no_build: "No build work",
};

// The tracker and the place a push writes to, per doc, so the assignee lists and the push read
// the same target. Kept in this browser only.
function useTarget(docId: string) {
  const key = `speccy-breakdown-target:${docId}`;
  const read = (): { tracker: string; target: string } => {
    try {
      const v = JSON.parse(localStorage.getItem(key) ?? "{}");
      return {
        tracker: typeof v.tracker === "string" ? v.tracker : "",
        target: typeof v.target === "string" ? v.target : "",
      };
    } catch {
      return { tracker: "", target: "" };
    }
  };
  const [value, setValue] = useState(read);
  const set = (v: { tracker: string; target: string }) => {
    setValue(v);
    try {
      localStorage.setItem(key, JSON.stringify(v));
    } catch {
      // Storage is not available: the choice lasts for this page load.
    }
  };
  return [value, set] as const;
}

// BreakdownPage is the one screen of a breakdown (#156): the epics and their work items, the
// findings, the coverage of the doc's trace IDs, and the push to a tracker. A person who cannot
// edit the doc reads it and comments on an item.
export function BreakdownPage({ docId, focusItem }: { docId: string; focusItem?: string }) {
  const bundleId = useBundleId();
  const qc = useQueryClient();
  const me = useMe();
  const mode = useReviewerMode();
  const canEdit = !mode.reviewer && !me.data?.guest;
  const doc = useQuery(getSpecDocOptions({ path: { docId } }));
  const bd = useQuery({ ...getBreakdownOptions({ path: { docId } }), retry: false });
  const [error, setError] = useState<string>();
  const [anyway, setAnyway] = useState(false);
  const [target, setTarget] = useTarget(docId);

  const put = (b: Breakdown) => {
    qc.setQueryData(getBreakdownQueryKey({ path: { docId } }), b);
    setError(undefined);
  };
  const fail = (err: unknown) => setError(problemMessage(err));

  // A job makes the proposal, so no proxy cuts its model call. The page polls it until it ends
  // (docs/specs/breakdown-proposal-job.md).
  const proposal = useQuery({
    ...getBreakdownProposalOptions({ path: { docId } }),
    retry: false,
    refetchInterval: (q) => (proposing(q.state.data) ? 3000 : false),
  });
  const running = proposing(proposal.data);
  const ended = proposal.data?.status;
  useEffect(() => {
    if (ended === "complete") void qc.invalidateQueries({ queryKey: getBreakdownQueryKey({ path: { docId } }) });
  }, [ended, docId, qc]);
  const propose = useMutation({
    mutationFn: async () =>
      (await proposeBreakdown({ path: { docId }, body: { acknowledged: anyway || undefined }, throwOnError: true }))
        .data,
    onSuccess: (p) => {
      setError(undefined);
      qc.setQueryData(getBreakdownProposalQueryKey({ path: { docId } }), p);
    },
    onError: fail,
  });
  const edit = useMutation({
    mutationFn: async (ops: BreakdownOp[]) =>
      (await editBreakdown({ path: { docId }, body: { based_on: bd.data!.revision, ops }, throwOnError: true })).data,
    onSuccess: put,
    onError: (err) => {
      fail(err);
      qc.invalidateQueries({ queryKey: getBreakdownQueryKey({ path: { docId } }) });
    },
  });
  const update = useMutation({
    mutationFn: async () =>
      (
        await updateBreakdown({
          path: { docId },
          body: { based_on: bd.data!.revision, acknowledged: anyway || undefined },
          throwOnError: true,
        })
      ).data,
    onSuccess: put,
    onError: fail,
  });

  // A link to one item scrolls to it once.
  useEffect(() => {
    if (!focusItem || !bd.data) return;
    document.getElementById(`item-${focusItem}`)?.scrollIntoView({ block: "center" });
  }, [focusItem, bd.data]);

  if (doc.isPending || mode.pending || bd.isPending) return <Loading label="Loading the breakdown" />;
  if (doc.isError)
    return (
      <div className="mx-auto max-w-[860px] p-6">
        <ErrorState message={problemMessage(doc.error)} />
      </div>
    );
  const b = bd.data;
  const notReady = doc.data.verdict?.result !== "build_ready";
  const missing = bd.isError && (bd.error as { code?: string } | null)?.code === "no_breakdown";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
        <Link
          to="/bundles/$bundleId/docs/$docId"
          params={{ bundleId, docId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> {doc.data.title}
        </Link>
        <h1 className="text-lg font-semibold tracking-tight">Breakdown</h1>
        {b ? (
          <span className="text-xs text-ink-2">
            {b.items.length} work item{b.items.length === 1 ? "" : "s"} in {b.epics.length} epic
            {b.epics.length === 1 ? "" : "s"}, from version {b.version_number}
            {b.must > 0 ? ` · ${b.must} MUST finding${b.must === 1 ? " blocks" : "s block"} the push` : ""}
          </span>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[860px] space-y-5 px-4 py-5 sm:px-6">
          {error ? <ErrorState message={error} /> : null}
          {!b && proposal.data?.status === "failed" && !error ? (
            <ErrorState message={proposal.data.error ?? "The breakdown proposal failed. Propose again."} />
          ) : null}

          {!b ? (
            missing ? (
              <Empty title="No breakdown yet">
                <p>
                  Speccy proposes epics and work items from the doc. Each work item quotes its criteria from the doc,
                  and you edit them before you push them to a tracker.
                </p>
                {canEdit ? (
                  <div className="mt-4 flex flex-col items-center gap-2">
                    {notReady ? (
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          className="accent-[var(--color-accent)]"
                          checked={anyway}
                          onChange={(e) => setAnyway(e.target.checked)}
                        />
                        The doc is not Build Ready. Propose it anyway.
                      </label>
                    ) : null}
                    <Button
                      variant="primary"
                      icon={
                        propose.isPending || running ? (
                          <Loader2 className="size-4 animate-spin" />
                        ) : (
                          <Sparkles className="size-4" />
                        )
                      }
                      disabled={propose.isPending || running || (notReady && !anyway)}
                      onClick={() => propose.mutate()}
                    >
                      {propose.isPending || running ? "Proposing the breakdown" : "Propose a breakdown"}
                    </Button>
                    {running && proposal.data ? (
                      <p role="status" className="text-xs text-ink-3">
                        Started {relativeTime(proposal.data.created_at)}. A long doc takes a few minutes. You can leave
                        this page.
                      </p>
                    ) : null}
                  </div>
                ) : null}
              </Empty>
            ) : (
              <ErrorState message={problemMessage(bd.error)} />
            )
          ) : (
            <>
              {b.stale ? (
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-warn/40 bg-warn-soft px-4 py-3 text-sm">
                  <p className="min-w-0 flex-1 text-ink">
                    The doc is at version {b.current_version_number}. This breakdown quotes version {b.version_number}.{" "}
                    {changedCount(b)} item{changedCount(b) === 1 ? "" : "s"} changed
                    {b.new_trace_ids.length
                      ? `, and ${b.new_trace_ids.join(", ")} ${b.new_trace_ids.length === 1 ? "is" : "are"} new`
                      : ""}
                    .
                    {pushedChanged(b).length
                      ? ` Pushed issues whose trace IDs changed: ${pushedChanged(b).join(", ")}. Speccy does not edit them.`
                      : ""}
                  </p>
                  {canEdit ? (
                    <Button
                      size="sm"
                      icon={<RotateCw className="size-3.5" />}
                      disabled={update.isPending}
                      onClick={() => update.mutate()}
                    >
                      Update to version {b.current_version_number}
                    </Button>
                  ) : null}
                </div>
              ) : null}

              <Findings b={b} canEdit={canEdit} onEdit={(ops) => edit.mutate(ops)} />

              {b.epics.map((e) => (
                <section key={e.id} aria-label={e.title} className="space-y-2">
                  <EpicHeader
                    b={b}
                    epicId={e.id}
                    canEdit={canEdit}
                    target={target}
                    docId={docId}
                    onEdit={(ops) => edit.mutate(ops)}
                  />
                  <ol className="space-y-2">
                    {b.items
                      .filter((it) => it.epic === e.id)
                      .map((it) => (
                        <ItemCard
                          key={it.id}
                          b={b}
                          item={it}
                          docId={docId}
                          canEdit={canEdit}
                          member={!me.data?.guest}
                          focused={it.id === focusItem}
                          target={target}
                          busy={edit.isPending}
                          onEdit={(ops) => edit.mutate(ops)}
                          onChecked={put}
                          onError={fail}
                        />
                      ))}
                  </ol>
                </section>
              ))}

              <Coverage b={b} />

              {canEdit ? (
                <PushPanel
                  b={b}
                  docId={docId}
                  notReady={notReady}
                  target={target}
                  onTarget={setTarget}
                  onPushed={(r) => {
                    put(r.breakdown);
                    qc.invalidateQueries({ queryKey: listHandoffsQueryKey({ path: { docId } }) });
                    qc.invalidateQueries({ queryKey: getSpecDocQueryKey({ path: { docId } }) });
                  }}
                />
              ) : null}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function changedCount(b: Breakdown): number {
  return b.items.filter((it) => it.changed.length > 0).length;
}

function pushedChanged(b: Breakdown): string[] {
  return b.items.filter((it) => it.issue && it.changed.length > 0).map((it) => it.issue!.key);
}

// Findings lists the failed checks, MUST first. A coverage finding takes its answer here.
function Findings({ b, canEdit, onEdit }: { b: Breakdown; canEdit: boolean; onEdit: (ops: BreakdownOp[]) => void }) {
  if (b.findings.length === 0)
    return (
      <p className="text-sm text-ok">The breakdown passes every check. Every trace ID is in a work item or answered.</p>
    );
  const sorted = [...b.findings].sort((x, y) => (x.level === y.level ? 0 : x.level === "MUST" ? -1 : 1));
  return (
    <section aria-label="Findings" className="space-y-2">
      <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Findings</h2>
      <ul className="space-y-2">
        {sorted.map((f, i) => (
          <FindingRow
            key={`${f.slug}-${f.trace_id ?? f.items.join(",")}-${i}`}
            b={b}
            f={f}
            canEdit={canEdit}
            onEdit={onEdit}
          />
        ))}
      </ul>
    </section>
  );
}

function FindingRow({
  b,
  f,
  canEdit,
  onEdit,
}: {
  b: Breakdown;
  f: BreakdownFinding;
  canEdit: boolean;
  onEdit: (ops: BreakdownOp[]) => void;
}) {
  const [answer, setAnswer] = useState("");
  const [reason, setReason] = useState("");
  const [into, setInto] = useState("");
  const coverage = f.slug === "breakdown.coverage" && !!f.trace_id;
  return (
    <li
      className={clsx(
        "rounded-lg border px-4 py-3 text-sm",
        f.level === "MUST" ? "border-bad/30 bg-bad-soft" : "border-line bg-surface",
      )}
    >
      <div className="flex flex-wrap items-baseline gap-2">
        <span className={clsx("text-2xs font-semibold", f.level === "MUST" ? "text-bad" : "text-warn")}>{f.level}</span>
        <a
          href={checkDocsURL(f.slug)}
          target="_blank"
          rel="noreferrer"
          className="font-mono text-2xs text-ink-3 hover:text-ink"
        >
          {f.slug}
        </a>
      </div>
      <p className="mt-1 text-ink">{f.message}</p>
      {coverage && canEdit ? (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <select
            aria-label={`The item for ${f.trace_id}`}
            value={into}
            onChange={(e) => setInto(e.target.value)}
            className="h-7 rounded-md border border-line-strong bg-surface px-1.5 text-xs text-ink"
          >
            <option value="">Add to an item…</option>
            {b.items.map((it) => (
              <option key={it.id} value={it.id}>
                {it.title}
              </option>
            ))}
          </select>
          <Button
            size="sm"
            disabled={!into}
            onClick={() => onEdit([{ op: "move_trace", trace_id: f.trace_id, from: "", to: into }])}
          >
            Add
          </Button>
          <span className="text-xs text-ink-3">or</span>
          <select
            aria-label={`Why ${f.trace_id} needs no item`}
            value={answer}
            onChange={(e) => setAnswer(e.target.value)}
            className="h-7 rounded-md border border-line-strong bg-surface px-1.5 text-xs text-ink"
          >
            <option value="">It needs no item…</option>
            {b.cover_answers.map((a) => (
              <option key={a} value={a}>
                {coverLabel[a] ?? a}
              </option>
            ))}
          </select>
          {answer ? (
            <>
              <Input
                aria-label="Reason"
                className="h-7 max-w-64 text-xs"
                placeholder="Why"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
              <Button
                size="sm"
                disabled={!reason.trim()}
                onClick={() =>
                  onEdit([
                    {
                      op: "cover",
                      trace_id: f.trace_id,
                      answer: answer as BreakdownOp["answer"],
                      reason: reason.trim(),
                    },
                  ])
                }
              >
                Save
              </Button>
            </>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

function EpicHeader({
  b,
  epicId,
  canEdit,
  target,
  docId,
  onEdit,
}: {
  b: Breakdown;
  epicId: string;
  canEdit: boolean;
  target: { tracker: string; target: string };
  docId: string;
  onEdit: (ops: BreakdownOp[]) => void;
}) {
  const e = b.epics.find((x) => x.id === epicId)!;
  const [picking, setPicking] = useState(false);
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
      <h2 className="text-md font-semibold text-ink">{e.title}</h2>
      {e.issue ? <IssueLink issue={e.issue} /> : null}
      <span className="text-xs text-ink-3">
        {e.default_assignee ? `Default: ${e.default_assignee.name}` : "No default assignee"}
      </span>
      {canEdit ? (
        <Button size="sm" variant="ghost" icon={<Users className="size-3.5" />} onClick={() => setPicking(true)}>
          Default assignee
        </Button>
      ) : null}
      <AssigneeDialog
        open={picking}
        onOpenChange={setPicking}
        docId={docId}
        target={target}
        title={`Default assignee of ${e.title}`}
        description="Every item of this epic with no assignee of its own gets this person or agent."
        current={e.default_assignee}
        onPick={(a) => {
          onEdit([{ op: "set_epic_default", epic: e.id, assignee: a ?? undefined }]);
          setPicking(false);
        }}
      />
    </div>
  );
}

function IssueLink({ issue }: { issue: NonNullable<WorkItem["issue"]> }) {
  return (
    <a
      href={issue.url}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 rounded-full bg-sunken px-1.5 py-0.5 font-mono text-2xs text-ink-2 hover:text-ink"
    >
      {issue.key}
      <ExternalLink aria-hidden className="size-3" />
    </a>
  );
}

function ItemCard({
  b,
  item: it,
  docId,
  canEdit,
  member,
  focused,
  target,
  busy,
  onEdit,
  onChecked,
  onError,
}: {
  b: Breakdown;
  item: WorkItem;
  docId: string;
  canEdit: boolean;
  member: boolean;
  focused: boolean;
  target: { tracker: string; target: string };
  busy: boolean;
  onEdit: (ops: BreakdownOp[]) => void;
  onChecked: (b: Breakdown) => void;
  onError: (err: unknown) => void;
}) {
  const [dialog, setDialog] = useState<"split" | "merge" | "deps" | "note" | "assign" | "comment" | null>(null);
  const findings = b.findings.filter((f) => f.items.includes(it.id));
  const index = b.items.findIndex((x) => x.id === it.id);
  const title = (id: string) => b.items.find((x) => x.id === id)?.title ?? id;
  const readers = useMutation({
    mutationFn: async () =>
      (await checkBreakdownReaders({ path: { docId }, body: { item: it.id }, throwOnError: true })).data,
    onSuccess: onChecked,
    onError,
  });
  const pushed = !!it.issue;
  return (
    <li
      id={`item-${it.id}`}
      className={clsx(
        "rounded-lg border bg-surface px-4 py-3",
        focused ? "border-accent" : findings.some((f) => f.level === "MUST") ? "border-bad/40" : "border-line",
      )}
    >
      <div className="flex flex-wrap items-start gap-x-2 gap-y-1">
        <span className="pt-0.5 font-mono text-2xs text-ink-3">{it.id}</span>
        <h3 className="min-w-0 flex-1 text-sm font-semibold text-ink">{it.title}</h3>
        {it.issue ? <IssueLink issue={it.issue} /> : null}
        {canEdit ? (
          <Menu
            trigger={
              <Button size="sm" variant="ghost" aria-label={`Edit ${it.title}`} disabled={busy}>
                Edit
              </Button>
            }
          >
            {!pushed && it.trace_ids.length > 1 ? (
              <MenuItem icon={<Scissors className="size-3.5" />} onSelect={() => setDialog("split")}>
                Split
              </MenuItem>
            ) : null}
            {!pushed && b.items.length > 1 ? (
              <MenuItem icon={<GitMerge className="size-3.5" />} onSelect={() => setDialog("merge")}>
                Merge into…
              </MenuItem>
            ) : null}
            <MenuItem onSelect={() => setDialog("deps")}>Dependencies</MenuItem>
            <MenuItem icon={<Users className="size-3.5" />} onSelect={() => setDialog("assign")}>
              Assignee
            </MenuItem>
            <MenuItem onSelect={() => setDialog("note")}>Note</MenuItem>
            {index > 0 ? (
              <MenuItem
                icon={<ArrowUp className="size-3.5" />}
                onSelect={() => onEdit([{ op: "reorder", item: it.id, before: b.items[index - 1]?.id ?? "" }])}
              >
                Move up
              </MenuItem>
            ) : null}
            {index < b.items.length - 1 ? (
              <MenuItem
                icon={<ArrowDown className="size-3.5" />}
                onSelect={() => onEdit([{ op: "reorder", item: it.id, before: b.items[index + 2]?.id ?? "" }])}
              >
                Move down
              </MenuItem>
            ) : null}
            {!pushed && it.trace_ids.length === 0 ? (
              <MenuItem danger onSelect={() => onEdit([{ op: "remove_item", item: it.id }])}>
                Remove
              </MenuItem>
            ) : null}
          </Menu>
        ) : member ? (
          <Button
            size="sm"
            variant="ghost"
            icon={<MessageSquare className="size-3.5" />}
            onClick={() => setDialog("comment")}
          >
            Comment
          </Button>
        ) : null}
      </div>

      <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-xs">
        {it.trace_ids.map((t) =>
          canEdit && !pushed ? (
            <TraceChip key={t} b={b} item={it} traceId={t} onEdit={onEdit} />
          ) : (
            <span key={t} className="rounded-full bg-sunken px-1.5 py-0.5 font-mono text-2xs text-ink-2">
              {t}
            </span>
          ),
        )}
        {it.heading_path.length && !it.trace_ids.length ? (
          <span className="text-ink-3">{it.heading_path.join(" › ")}</span>
        ) : null}
        {it.repos.map((r) => (
          <span key={r} className="font-mono text-2xs text-ink-3">
            {r}
          </span>
        ))}
        {it.effective_assignee ? (
          <span className="inline-flex items-center gap-1 text-ink-2">
            {it.effective_assignee.kind === "agent" ? <Bot aria-hidden className="size-3.5" /> : null}
            {it.effective_assignee.name}
            {it.assignee ? "" : " (epic default)"}
          </span>
        ) : null}
        {it.depends_on.length ? <span className="text-ink-3">After {it.depends_on.map(title).join(", ")}</span> : null}
      </div>

      {it.changed.length ? (
        <ul className="mt-2 space-y-0.5 text-xs text-warn">
          {it.changed.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
      ) : null}

      <ul className="mt-2 space-y-1.5">
        {it.criteria.map((c) => (
          <li key={c.quote} className="border-l-2 border-line pl-2.5 text-sm text-ink">
            <q className="before:content-none after:content-none">{c.quote}</q>
            <span className="block text-2xs text-ink-3">
              {c.doc ? `${c.doc} › ` : ""}
              {c.heading_path.join(" › ")}
            </span>
          </li>
        ))}
      </ul>

      {it.answers.length ? (
        <details className="mt-2 text-xs">
          <summary className="cursor-pointer text-ink-2">
            {it.answers.length} build answer{it.answers.length === 1 ? "" : "s"}
          </summary>
          <ul className="mt-1 space-y-1">
            {it.answers.map((a) => (
              <li key={a.question}>
                <span className="font-medium text-ink">{a.question}</span>{" "}
                <span className="text-ink-2">{a.answer}</span>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {it.questions.length ? (
        <ul className="mt-2 list-disc space-y-0.5 pl-5 text-xs text-ink-2">
          {it.questions.map((q) => (
            <li key={q}>{q}</li>
          ))}
        </ul>
      ) : null}
      {it.note ? <p className="mt-2 text-xs whitespace-pre-wrap text-ink-2">Note: {it.note}</p> : null}

      {findings.length ? (
        <ul className="mt-2 space-y-0.5 text-xs">
          {findings.map((f, i) => (
            <li key={i} className={f.level === "MUST" ? "text-bad" : "text-warn"}>
              {f.message}
            </li>
          ))}
        </ul>
      ) : null}

      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
        {it.readers ? (
          <span className={clsx(it.readers.diverge ? "text-warn" : "text-ok", !it.readers.current && "text-ink-3")}>
            {!it.readers.current
              ? "The item changed after the readers read it."
              : it.readers.diverge
                ? "Two readers would build different things."
                : "Two readers would build the same thing."}
          </span>
        ) : null}
        {canEdit ? (
          <Button
            size="sm"
            variant="ghost"
            icon={readers.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Sparkles className="size-3.5" />}
            disabled={readers.isPending}
            onClick={() => readers.mutate()}
          >
            {readers.isPending ? "Readers are reading" : "Check with readers"}
          </Button>
        ) : null}
      </div>

      <SplitDialog
        open={dialog === "split"}
        item={it}
        onClose={() => setDialog(null)}
        onSplit={(ids, t) => onEdit([{ op: "split", item: it.id, trace_ids: ids, title: t || undefined }])}
      />
      <Dialog open={dialog === "merge"} onOpenChange={(o) => !o && setDialog(null)} title={`Merge ${it.title}`}>
        <ul className="space-y-1">
          {b.items
            .filter((x) => x.id !== it.id && !x.issue)
            .map((x) => (
              <li key={x.id}>
                <Button
                  variant="ghost"
                  className="w-full justify-start"
                  onClick={() => {
                    onEdit([{ op: "merge", item: it.id, into: x.id }]);
                    setDialog(null);
                  }}
                >
                  Into {x.title}
                </Button>
              </li>
            ))}
        </ul>
      </Dialog>
      <DepsDialog
        open={dialog === "deps"}
        b={b}
        item={it}
        onClose={() => setDialog(null)}
        onSave={(deps) => onEdit([{ op: "set_dependencies", item: it.id, depends_on: deps }])}
      />
      <NoteDialog
        open={dialog === "note"}
        item={it}
        onClose={() => setDialog(null)}
        onSave={(note) => onEdit([{ op: "set_note", item: it.id, note }])}
      />
      <AssigneeDialog
        open={dialog === "assign"}
        onOpenChange={(o) => !o && setDialog(null)}
        docId={docId}
        target={target}
        title={`Assignee of ${it.title}`}
        description="A person, or an agent that starts on the item when the push assigns it."
        current={it.assignee}
        onPick={(a) => {
          onEdit([{ op: "set_assignee", item: it.id, assignee: a ?? undefined }]);
          setDialog(null);
        }}
      />
      <CommentDialog open={dialog === "comment"} docId={docId} item={it} onClose={() => setDialog(null)} />
    </li>
  );
}

// TraceChip moves one trace ID to another item, or out of every item.
function TraceChip({
  b,
  item,
  traceId,
  onEdit,
}: {
  b: Breakdown;
  item: WorkItem;
  traceId: string;
  onEdit: (ops: BreakdownOp[]) => void;
}) {
  return (
    <Menu
      trigger={
        <button
          type="button"
          className="rounded-full bg-sunken px-1.5 py-0.5 font-mono text-2xs text-ink-2 hover:text-ink"
          aria-label={`Move ${traceId}`}
        >
          {traceId}
        </button>
      }
    >
      {b.items
        .filter((x) => x.id !== item.id && !x.issue)
        .map((x) => (
          <MenuItem
            key={x.id}
            onSelect={() => onEdit([{ op: "move_trace", trace_id: traceId, from: item.id, to: x.id }])}
          >
            Move to {x.title}
          </MenuItem>
        ))}
      <MenuItem onSelect={() => onEdit([{ op: "move_trace", trace_id: traceId, from: item.id, to: "" }])}>
        Take out of every item
      </MenuItem>
    </Menu>
  );
}

function SplitDialog({
  open,
  item,
  onClose,
  onSplit,
}: {
  open: boolean;
  item: WorkItem;
  onClose: () => void;
  onSplit: (ids: string[], title: string) => void;
}) {
  const [ids, setIds] = useState<string[]>([]);
  const [title, setTitle] = useState("");
  const titleId = useId();
  const valid = ids.length > 0 && ids.length < item.trace_ids.length;
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title={`Split ${item.title}`}
      description="The trace IDs you pick move to a new item, with their criteria."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={!valid}
            onClick={() => {
              onSplit(ids, title.trim());
              setIds([]);
              setTitle("");
              onClose();
            }}
          >
            Split
          </Button>
        </>
      }
    >
      <ul className="space-y-1">
        {item.trace_ids.map((t) => (
          <li key={t}>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="accent-[var(--color-accent)]"
                checked={ids.includes(t)}
                onChange={(e) => setIds(e.target.checked ? [...ids, t] : ids.filter((x) => x !== t))}
              />
              <span className="font-mono text-xs">{t}</span>
            </label>
          </li>
        ))}
      </ul>
      <div className="mt-3">
        <Label htmlFor={titleId}>Title of the new item (optional)</Label>
        <Input id={titleId} value={title} onChange={(e) => setTitle(e.target.value)} />
      </div>
    </Dialog>
  );
}

function DepsDialog({
  open,
  b,
  item,
  onClose,
  onSave,
}: {
  open: boolean;
  b: Breakdown;
  item: WorkItem;
  onClose: () => void;
  onSave: (deps: string[]) => void;
}) {
  const [deps, setDeps] = useState(item.depends_on);
  useEffect(() => setDeps(item.depends_on), [item.depends_on, open]);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title={`${item.title} depends on`}
      description="A builder takes this item after the items you tick. The push sets each as a blocker."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={() => {
              onSave(deps);
              onClose();
            }}
          >
            Save
          </Button>
        </>
      }
    >
      <ul className="max-h-[50vh] space-y-1 overflow-y-auto">
        {b.items
          .filter((x) => x.id !== item.id)
          .map((x) => (
            <li key={x.id}>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  className="accent-[var(--color-accent)]"
                  checked={deps.includes(x.id)}
                  onChange={(e) => setDeps(e.target.checked ? [...deps, x.id] : deps.filter((d) => d !== x.id))}
                />
                {x.title}
              </label>
            </li>
          ))}
      </ul>
    </Dialog>
  );
}

function NoteDialog({
  open,
  item,
  onClose,
  onSave,
}: {
  open: boolean;
  item: WorkItem;
  onClose: () => void;
  onSave: (note: string) => void;
}) {
  const [note, setNote] = useState(item.note ?? "");
  useEffect(() => setNote(item.note ?? ""), [item.note, open]);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title={`Note on ${item.title}`}
      description="The note goes into the issue under the criteria. It adds no criterion: to change one, change the doc."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={() => {
              onSave(note.trim());
              onClose();
            }}
          >
            Save
          </Button>
        </>
      }
    >
      <Textarea aria-label="Note" rows={5} value={note} onChange={(e) => setNote(e.target.value)} />
    </Dialog>
  );
}

// AssigneeDialog picks a person or an agent from the tracker the push writes to. Agents are
// listed apart from people.
function AssigneeDialog({
  open,
  onOpenChange,
  docId,
  target,
  title,
  description,
  current,
  onPick,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  docId: string;
  target: { tracker: string; target: string };
  title: string;
  description: string;
  current?: Assignee;
  onPick: (a: Assignee | null) => void;
}) {
  const [query, setQuery] = useState("");
  const ready = !!target.tracker && !!target.target;
  const list = useQuery({
    ...listBreakdownAssigneesOptions({
      path: { docId },
      query: {
        tracker: target.tracker as "linear" | "jira" | "github",
        target: target.target,
        query: query || undefined,
      },
    }),
    enabled: open && ready && (target.tracker !== "jira" || query.length > 1),
    retry: false,
  });
  const agents = list.data?.items.filter((a) => a.kind === "agent") ?? [];
  const people = list.data?.items.filter((a) => a.kind === "person") ?? [];
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={title} description={description}>
      {!ready ? (
        <p className="text-sm text-ink-2">Pick the tracker and where the issues go, under Push, first.</p>
      ) : (
        <div className="space-y-3">
          <Input
            aria-label="Find a person or an agent"
            placeholder="Find by name"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {list.isError ? <ErrorState message={problemMessage(list.error)} /> : null}
          {list.isFetching ? <Loading label="Reading the tracker" /> : null}
          {[
            { label: "Agents", items: agents },
            { label: "People", items: people },
          ].map((g) =>
            g.items.length ? (
              <div key={g.label}>
                <h3 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
                  {g.label}
                </h3>
                <ul className="mt-1 max-h-48 space-y-0.5 overflow-y-auto">
                  {g.items.map((a) => (
                    <li key={a.id}>
                      <Button
                        variant="ghost"
                        className={clsx("w-full justify-start", current?.id === a.id && "text-accent")}
                        icon={a.kind === "agent" ? <Bot className="size-3.5" /> : undefined}
                        onClick={() => onPick(a)}
                      >
                        {a.name}
                      </Button>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null,
          )}
          {current ? (
            <Button variant="ghost" onClick={() => onPick(null)}>
              Clear {current.name}
            </Button>
          ) : null}
        </div>
      )}
    </Dialog>
  );
}

// CommentDialog opens a thread on the item's first criterion: a reviewer comments, and the
// author answers in the doc's threads.
function CommentDialog({
  open,
  docId,
  item,
  onClose,
}: {
  open: boolean;
  docId: string;
  item: WorkItem;
  onClose: () => void;
}) {
  const [body, setBody] = useState("");
  const open_ = useMutation({ ...openBundleThreadMutation(), onSuccess: () => (setBody(""), onClose()) });
  const path = item.criteria.find((c) => !c.doc)?.heading_path ?? item.heading_path;
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title={`Comment on ${item.title}`}
      description="The comment opens a thread on the section the item quotes. The author sees it with the doc's threads."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            icon={<Send className="size-3.5" />}
            disabled={!body.trim() || open_.isPending}
            onClick={() =>
              open_.mutate({
                path: { docId },
                body: {
                  anchor_kind: "section",
                  anchor: { heading_path: path },
                  addressed_to: "humans",
                  title: `Work item ${item.id}: ${item.title}`,
                  body: body.trim(),
                },
              })
            }
          >
            Comment
          </Button>
        </>
      }
    >
      <Textarea aria-label="Comment" rows={4} value={body} onChange={(e) => setBody(e.target.value)} />
      {open_.error ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(open_.error)} />
        </div>
      ) : null}
    </Dialog>
  );
}

// Coverage shows each trace ID of the doc with the items that hold it.
function Coverage({ b }: { b: Breakdown }) {
  const titles = useMemo(() => new Map(b.items.map((it) => [it.id, it.title])), [b.items]);
  if (b.coverage.length === 0) return null;
  return (
    <section aria-label="Coverage" className="space-y-2">
      <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Coverage</h2>
      <div className="overflow-x-auto rounded-lg border border-line">
        <table className="w-full text-left text-xs">
          <thead className="bg-sunken text-ink-3">
            <tr>
              <th className="px-3 py-1.5 font-medium">Trace ID</th>
              <th className="px-3 py-1.5 font-medium">Work item</th>
            </tr>
          </thead>
          <tbody>
            {b.coverage.map((c) => (
              <tr key={c.trace_id} className="border-t border-line">
                <td className="px-3 py-1.5 align-top font-mono text-ink" title={c.text}>
                  {c.trace_id}
                </td>
                <td className="px-3 py-1.5 text-ink-2">
                  {c.items.length ? (
                    c.items.map((id) => titles.get(id) ?? id).join(", ")
                  ) : c.cover ? (
                    (coverLabel[c.cover] ?? c.cover)
                  ) : (
                    <span className="text-bad">In no item</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// PushPanel picks the tracker and pushes the items that have no issue yet. With no connection,
// it exports the breakdown.
function PushPanel({
  b,
  docId,
  notReady,
  target,
  onTarget,
  onPushed,
}: {
  b: Breakdown;
  docId: string;
  notReady: boolean;
  target: { tracker: string; target: string };
  onTarget: (t: { tracker: string; target: string }) => void;
  onPushed: (r: PushResult) => void;
}) {
  const trackers = useQuery(listBreakdownTrackersOptions({ path: { docId } }));
  const [anyway, setAnyway] = useState(false);
  const [result, setResult] = useState<PushResult>();
  const targetId = useId();
  const pushedTracker = b.items.find((it) => it.issue)?.issue?.tracker;
  const tracker = pushedTracker ?? target.tracker;
  const todo = b.items.filter((it) => !it.issue).length;
  const push = useMutation({
    mutationFn: async () =>
      (
        await pushBreakdown({
          path: { docId },
          body: {
            based_on: b.revision,
            tracker: tracker as "linear" | "jira" | "github",
            target: target.target,
            acknowledged: anyway || undefined,
          },
          throwOnError: true,
        })
      ).data,
    onSuccess: (r) => {
      setResult(r);
      onPushed(r);
    },
  });
  const options: Tracker[] = trackers.data?.items ?? [];
  const pick = options.find((o) => o.kind === tracker && (o.kind !== "jira" || target.target.includes(o.host ?? "")));
  const placeholder =
    tracker === "linear"
      ? "Team, such as ENG"
      : tracker === "jira"
        ? "https://acme.atlassian.net/browse/PAY"
        : "owner/name";
  const blocked = b.must > 0 || b.stale || (notReady && !anyway);
  return (
    <section aria-label="Push" className="space-y-3 rounded-lg border border-line bg-surface px-4 py-4">
      <h2 className="text-md font-semibold text-ink">Push to a tracker</h2>
      <p className="text-xs text-ink-2">
        The push creates an issue for each epic and each work item that has none. It never edits or closes an issue, and
        it records one handoff with the label breakdown.
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <div>
          <Label>Tracker</Label>
          <select
            aria-label="Tracker"
            value={tracker}
            disabled={!!pushedTracker}
            onChange={(e) => onTarget({ tracker: e.target.value, target: "" })}
            className="mt-1 h-8 rounded-md border border-line-strong bg-surface px-2 text-sm text-ink"
          >
            <option value="">Pick one…</option>
            {[...new Set(options.map((o) => o.kind))].map((k) => (
              <option key={k} value={k}>
                {k === "linear" ? "Linear" : k === "jira" ? "Jira" : "GitHub Issues"}
              </option>
            ))}
          </select>
        </div>
        <div className="min-w-56 flex-1">
          <Label htmlFor={targetId}>Where the issues go</Label>
          <Input
            id={targetId}
            className="mt-1"
            placeholder={placeholder}
            value={target.target}
            onChange={(e) => onTarget({ tracker, target: e.target.value })}
          />
        </div>
      </div>
      {tracker && pick && !pick.ready ? <p className="text-xs text-warn">{pick.reason}</p> : null}
      {notReady ? (
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            className="accent-[var(--color-accent)]"
            checked={anyway}
            onChange={(e) => setAnyway(e.target.checked)}
          />
          The doc is not Build Ready. Push anyway; the handoff records the verdict.
        </label>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="primary"
          icon={push.isPending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
          disabled={blocked || !tracker || !target.target.trim() || todo === 0 || push.isPending}
          onClick={() => push.mutate()}
        >
          {todo === 0 ? "Every item is pushed" : `Push ${todo} work item${todo === 1 ? "" : "s"}`}
        </Button>
        <a
          className="text-xs text-ink-2 hover:text-ink"
          href={`/api/v1/docs/${docId}/breakdown/export?format=markdown`}
        >
          Export as markdown
        </a>
        <a className="text-xs text-ink-2 hover:text-ink" href={`/api/v1/docs/${docId}/breakdown/export?format=csv`}>
          Export as CSV
        </a>
      </div>
      {b.must > 0 ? <p className="text-xs text-bad">Fix the MUST findings first.</p> : null}
      {b.stale ? <p className="text-xs text-warn">Update the breakdown to the doc's current version first.</p> : null}
      {push.error ? <ErrorState message={problemMessage(push.error)} /> : null}
      {result ? (
        <p role="status" className="text-sm text-ok">
          {result.created.length === 0
            ? "Every item was already an issue. The push created nothing."
            : `Created ${result.created.length} issue${result.created.length === 1 ? "" : "s"}: ${result.created.map((c) => c.key).join(", ")}.`}
        </p>
      ) : null}
    </section>
  );
}

// proposing says whether a proposal is still queued or running.
function proposing(p: BreakdownProposal | undefined): boolean {
  return p?.status === "queued" || p?.status === "running";
}
