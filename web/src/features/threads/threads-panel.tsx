import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import {
  ArrowLeft,
  Bot,
  CircleCheck,
  CircleDot,
  Gavel,
  Lock,
  MessageSquarePlus,
  PackageCheck,
  Send,
} from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Anchor, Thread, ThreadDetail } from "@/lib/api";
import {
  getBundleOptions,
  getThreadOptions,
  getThreadQueryKey,
  listBundleThreadsOptions,
  listBundleThreadsQueryKey,
  listProfileThreadsQueryKey,
  markDecisionMutation,
  openBundleThreadMutation,
  postMessageMutation,
  setThreadBlockingMutation,
  setThreadStatusMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime as timeAgo } from "@/features/bundle/time";

// A new thread's anchor (REQ-087): selected text, a section, or a finding.
export type NewAnchor =
  | { kind: "text"; anchor: Anchor; label: string }
  | { kind: "section"; heading_path: string[]; label: string }
  | { kind: "finding"; finding_id: string; check_slug: string; label: string };

// ThreadsPanel lists a bundle's threads, shows one, and opens new ones. A guest writes in
// threads for humans only; members also ask the AI, decide, mark blocking, and resolve.
export function ThreadsPanel({
  bundleId,
  member,
  reviewer = false,
  pending,
  onPendingDone,
  onOpenAnchor,
}: {
  bundleId: string;
  member: boolean;
  // reviewer changes the lead line: reviewer mode has no code view and no findings.
  reviewer?: boolean;
  pending?: NewAnchor;
  onPendingDone: () => void;
  onOpenAnchor: (a: Anchor) => void;
}) {
  const [open, setOpen] = useState<string>();
  const threads = useQuery(listBundleThreadsOptions({ path: { bundleId } }));

  if (pending) {
    return (
      <Composer
        bundleId={bundleId}
        member={member}
        anchor={pending}
        onDone={(id) => {
          onPendingDone();
          if (id) setOpen(id);
        }}
      />
    );
  }
  if (open)
    return (
      <ThreadView
        threadId={open}
        bundleId={bundleId}
        member={member}
        onBack={() => setOpen(undefined)}
        onOpenAnchor={onOpenAnchor}
      />
    );
  if (threads.isPending) return <Loading label="Loading threads" />;
  if (threads.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(threads.error)} />
      </div>
    );
  return (
    <div>
      <p className="px-3 pt-3 pb-1 text-xs text-ink-2">
        {reviewer
          ? "Select the words you want to talk about, then choose Comment. The author reads every comment."
          : "Select text in the code view and choose Comment, or discuss a finding. A blocking thread stops Build Ready."}
      </p>
      {threads.data.items.length === 0 ? (
        <Empty title="No threads yet" />
      ) : (
        <ul className="divide-y divide-line">
          {threads.data.items.map((t) => (
            <ThreadRow key={t.id} t={t} onOpen={() => setOpen(t.id)} />
          ))}
        </ul>
      )}
    </div>
  );
}

function ThreadRow({ t, onOpen }: { t: Thread; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="block w-full px-3 py-2.5 text-left transition-colors hover:bg-sunken"
      >
        <span className="flex items-center gap-1.5 text-2xs font-semibold tracking-wide">
          {t.status === "resolved" ? (
            <CircleCheck aria-hidden className="size-3.5 text-ok" />
          ) : (
            <CircleDot aria-hidden className={clsx("size-3.5", t.blocking ? "text-bad" : "text-accent")} />
          )}
          <span className={t.status === "resolved" ? "text-ok" : t.blocking ? "text-bad" : "text-ink-2"}>
            {t.status === "resolved" ? "Resolved" : t.blocking ? "Blocking" : "Open"}
          </span>
          {t.addressed_to === "ai" ? (
            <span className="inline-flex items-center gap-0.5 text-ink-3">
              <Bot aria-hidden className="size-3" /> AI
            </span>
          ) : null}
          {t.handoff_id ? (
            <span
              className="inline-flex items-center gap-0.5 font-normal text-ink-3"
              title={`A builder opened this from its handoff of version ${t.handoff_version}.`}
            >
              <PackageCheck aria-hidden className="size-3" /> v{t.handoff_version}
            </span>
          ) : null}
          {t.anchor_kind === "text" && t.anchor.detached ? (
            <span className="font-normal text-warn" title="The text changed. Speccy cannot find the quote.">
              Detached
            </span>
          ) : null}
          <span className="ml-auto font-normal text-ink-3">{timeAgo(t.last_message_at)}</span>
        </span>
        <span className="mt-1 block text-sm text-ink">{t.title}</span>
        <span className="mt-0.5 block text-xs text-ink-3">
          {t.message_count} message{t.message_count === 1 ? "" : "s"}
        </span>
      </button>
    </li>
  );
}

function Composer({
  bundleId,
  member,
  anchor,
  onDone,
}: {
  bundleId: string;
  member: boolean;
  anchor: NewAnchor;
  onDone: (id?: string) => void;
}) {
  const qc = useQueryClient();
  const [body, setBody] = useState("");
  const [ai, setAi] = useState(false);
  const [blocking, setBlocking] = useState(false);
  const create = useMutation({
    ...openBundleThreadMutation(),
    onSuccess: (t) => {
      qc.invalidateQueries({ queryKey: listBundleThreadsQueryKey({ path: { bundleId } }) });
      qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
      onDone(t.id);
    },
  });
  const anchorBody =
    anchor.kind === "text"
      ? { ...anchor.anchor }
      : anchor.kind === "section"
        ? { heading_path: anchor.heading_path }
        : { finding_id: anchor.finding_id, check_slug: anchor.check_slug };
  return (
    <form
      className="space-y-3 p-3"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate({
          path: { bundleId },
          body: {
            anchor_kind: anchor.kind,
            anchor: anchorBody,
            addressed_to: ai ? "ai" : "humans",
            body,
            blocking: blocking && !ai,
          },
        });
      }}
    >
      <button
        type="button"
        onClick={() => onDone()}
        className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
      >
        <ArrowLeft aria-hidden className="size-3.5" /> Threads
      </button>
      <p className="border-l-2 border-accent pl-2 text-xs text-ink-2">{anchor.label}</p>
      <Textarea
        aria-label="Message"
        rows={5}
        className="font-sans text-sm"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder={
          ai ? "Ask the AI about this part of the doc." : "Write to the team. Mention someone with @ and their email."
        }
        autoFocus
        required
      />
      {member ? (
        <div className="space-y-1.5 text-sm">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={ai}
              onChange={(e) => setAi(e.target.checked)}
              className="accent-[var(--color-accent)]"
            />
            Ask the AI
          </label>
          {ai ? (
            <p className="text-xs text-ink-3">
              The AI reads the part of the doc this thread is on, the whole doc, its assets, and its linked docs.
            </p>
          ) : null}
          {!ai ? (
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={blocking}
                onChange={(e) => setBlocking(e.target.checked)}
                className="accent-[var(--color-accent)]"
              />
              Blocking: the doc is not Build Ready until this is resolved
            </label>
          ) : null}
        </div>
      ) : null}
      {create.isError ? <ErrorState message={problemMessage(create.error)} /> : null}
      <Button
        type="submit"
        variant="primary"
        size="sm"
        icon={<MessageSquarePlus className="size-3.5" />}
        disabled={create.isPending || !body.trim()}
      >
        Start the thread
      </Button>
    </form>
  );
}

// ThreadView shows one thread. bundleId is absent for a suggestion thread on a profile.
export function ThreadView({
  threadId,
  bundleId,
  member,
  onBack,
  onOpenAnchor,
}: {
  threadId: string;
  bundleId?: string;
  member: boolean;
  onBack: () => void;
  onOpenAnchor?: (a: Anchor) => void;
}) {
  const qc = useQueryClient();
  const thread = useQuery({
    ...getThreadOptions({ path: { threadId } }),
    // While the AI writes, read the thread again until the answer is in.
    refetchInterval: (q) => (q.state.data?.answering ? 1500 : false),
  });
  const [body, setBody] = useState("");
  const update = (t: ThreadDetail) => {
    qc.setQueryData(getThreadQueryKey({ path: { threadId } }), t);
    if (bundleId) {
      qc.invalidateQueries({ queryKey: listBundleThreadsQueryKey({ path: { bundleId } }) });
      qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
    } else if (t.profile_key) {
      qc.invalidateQueries({ queryKey: listProfileThreadsQueryKey({ path: { key: t.profile_key } }) });
    }
  };
  const post = useMutation({
    ...postMessageMutation(),
    onSuccess: (t) => {
      setBody("");
      update(t);
    },
  });
  const decide = useMutation({ ...markDecisionMutation(), onSuccess: update });
  const block = useMutation({ ...setThreadBlockingMutation(), onSuccess: update });
  const status = useMutation({ ...setThreadStatusMutation(), onSuccess: update });
  const error = post.error ?? decide.error ?? block.error ?? status.error;

  if (thread.isPending) return <Loading label="Loading the thread" />;
  if (thread.isError)
    return (
      <div className="p-2">
        <ErrorState message={problemMessage(thread.error)} />
      </div>
    );
  const t = thread.data;
  const textAnchor = t.anchor_kind === "text" ? (t.anchor as unknown as Anchor) : undefined;
  const open = t.status === "open";
  return (
    <div className="flex min-h-full flex-col">
      <div className="space-y-2 border-b border-line p-3">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> Threads
        </button>
        <p className="text-sm font-medium">{t.title}</p>
        {textAnchor?.detached ? (
          <div className="border-l-2 border-warn pl-2 text-xs">
            <p className="font-medium text-warn">Detached: the text changed, and Speccy cannot find this quote.</p>
            <p className="mt-0.5 font-mono text-ink-2">{textAnchor.quote}</p>
          </div>
        ) : textAnchor && onOpenAnchor ? (
          <button
            type="button"
            onClick={() => onOpenAnchor(textAnchor)}
            className="block border-l-2 border-line-strong pl-2 text-left font-mono text-xs text-ink-2 hover:border-accent"
          >
            {textAnchor.quote}
          </button>
        ) : null}
        {member ? (
          <div className="flex flex-wrap gap-1.5">
            {t.addressed_to === "humans" ? (
              <Button
                size="sm"
                variant={t.blocking ? "primary" : "secondary"}
                icon={<Lock className="size-3.5" />}
                onClick={() => block.mutate({ path: { threadId }, body: { blocking: !t.blocking } })}
              >
                {t.blocking ? "Blocking" : "Mark blocking"}
              </Button>
            ) : null}
            <Button size="sm" onClick={() => status.mutate({ path: { threadId }, body: { open: !open } })}>
              {open ? "Resolve" : "Reopen"}
            </Button>
          </div>
        ) : null}
      </div>
      <ol className="flex-1 space-y-3 p-3">
        {t.messages.map((m) => (
          <li
            key={m.id}
            className={clsx(
              "rounded-md px-2.5 py-2 text-sm",
              m.decision ? "border border-accent/50 bg-accent-soft" : m.author_kind === "ai" ? "bg-sunken" : "",
            )}
          >
            <p className="flex items-center gap-1.5 text-xs">
              {m.author_kind === "ai" ? <Bot aria-hidden className="size-3.5 text-ink-3" /> : null}
              <span className="font-medium text-ink">{m.author_name}</span>
              {m.author_kind === "guest" ? <span className="text-ink-3">guest</span> : null}
              <span className="text-ink-3">{timeAgo(m.created_at)}</span>
              {m.decision ? (
                <span className="ml-auto inline-flex items-center gap-1 font-semibold text-accent">
                  <Gavel aria-hidden className="size-3" />
                  {m.decision === "reversal" ? "Decision (reversed an earlier one)" : "Decision"}
                </span>
              ) : member && open && m.author_kind !== "ai" ? (
                <button
                  type="button"
                  onClick={() => decide.mutate({ path: { threadId }, body: { message_id: m.id } })}
                  className="ml-auto text-ink-3 hover:text-ink"
                >
                  Mark as decision
                </button>
              ) : null}
            </p>
            <p className="mt-1 whitespace-pre-wrap text-ink">{m.body}</p>
            {m.sources.length ? (
              <ul className="mt-1.5 space-y-0.5">
                {m.sources.map((s) => (
                  <li key={s} className="truncate text-xs">
                    {/^https?:\/\//.test(s) ? (
                      <a href={s} target="_blank" rel="noreferrer noopener" className="text-accent underline">
                        {s}
                      </a>
                    ) : (
                      <span className="text-ink-2">{s}</span>
                    )}
                  </li>
                ))}
              </ul>
            ) : m.author_kind === "ai" ? (
              <p className="mt-1 text-xs text-ink-3">No source. Treat any outside fact here as unverified.</p>
            ) : null}
          </li>
        ))}
        {t.answering ? <li className="text-xs text-ink-3">The AI is writing an answer.</li> : null}
      </ol>
      {error ? (
        <div className="px-3">
          <ErrorState message={problemMessage(error)} />
        </div>
      ) : null}
      {open ? (
        <form
          className="sticky bottom-0 flex gap-2 border-t border-line bg-surface p-3"
          onSubmit={(e) => {
            e.preventDefault();
            post.mutate({ path: { threadId }, body: { body } });
          }}
        >
          <Textarea
            aria-label="Reply"
            rows={2}
            className="font-sans text-sm"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder={t.addressed_to === "ai" ? "Ask a follow-up question." : "Reply"}
          />
          <Button
            type="submit"
            size="sm"
            aria-label="Send"
            icon={<Send className="size-3.5" />}
            disabled={post.isPending || !body.trim()}
          />
        </form>
      ) : null}
    </div>
  );
}
