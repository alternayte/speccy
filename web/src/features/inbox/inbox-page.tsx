import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import { AtSign, BadgeCheck, MessageSquare, PlayCircle, ShieldAlert, ShieldQuestion, ShieldX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { InboxItem } from "@/lib/api";
import { getInboxOptions, getInboxQueryKey, markInboxSeenMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "@/features/bundle/time";

const kindIcon = {
  review_request: BadgeCheck,
  message: MessageSquare,
  mention: AtSign,
  run: PlayCircle,
  waiver_request: ShieldQuestion,
  waiver_rejected: ShieldX,
  waiver_ended: ShieldAlert,
} as const;

const kindLabel = {
  review_request: "Review request",
  waiver_request: "Waiver request",
  waiver_rejected: "Waiver rejected",
  waiver_ended: "Waiver ended",
  message: "Message",
  mention: "Mention",
  run: "Review",
} as const;

// InboxPage lists what needs the caller: review requests, messages and finished reviews on
// the bundles they author, and mentions (REQ-091).
export function InboxPage() {
  const qc = useQueryClient();
  const inbox = useQuery({ ...getInboxOptions(), refetchInterval: 15_000 });
  const seen = useMutation({
    ...markInboxSeenMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: getInboxQueryKey() }),
  });
  const unread = inbox.data?.items.filter((i) => i.unread).length ?? 0;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[760px] px-4 py-8 sm:px-6">
        <header className="flex items-end justify-between gap-4">
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Inbox</h1>
            <p className="mt-1 text-sm text-ink-2">
              {unread ? `${unread} new item${unread === 1 ? "" : "s"}.` : "Nothing new."} The last 30 days.
            </p>
          </div>
          {unread ? (
            <Button size="sm" onClick={() => seen.mutate({})} disabled={seen.isPending}>
              Mark all read
            </Button>
          ) : null}
        </header>
        <div className="mt-6 overflow-hidden rounded-lg border border-line bg-surface">
          {inbox.isPending ? (
            <Loading label="Loading the inbox" />
          ) : inbox.isError ? (
            <div className="p-3">
              <ErrorState message={problemMessage(inbox.error)} />
            </div>
          ) : inbox.data.items.length === 0 ? (
            <Empty title="Your inbox is empty">
              Review requests, mentions, and news about the bundles you author appear here.
            </Empty>
          ) : (
            <ul className="divide-y divide-line">
              {inbox.data.items.map((i, n) => (
                <Row key={`${i.kind}-${i.at}-${n}`} item={i} />
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}

function Row({ item: i }: { item: InboxItem }) {
  const Icon = kindIcon[i.kind];
  return (
    <li>
      <Link
        to="/bundles/$bundleId"
        params={{ bundleId: i.bundle_id }}
        search={i.waiver_id ? { waiver: i.waiver_id } : {}}
        className={clsx("flex gap-3 px-4 py-3 transition-colors hover:bg-sunken", i.unread && "bg-accent-soft/40")}
      >
        <Icon aria-hidden className={clsx("mt-0.5 size-4 shrink-0", i.unread ? "text-accent" : "text-ink-3")} />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline gap-2 text-xs">
            <span className={clsx("font-semibold", i.unread ? "text-ink" : "text-ink-2")}>{kindLabel[i.kind]}</span>
            <span className="truncate text-ink-3">{i.bundle_title}</span>
            <span className="ml-auto shrink-0 text-ink-3">{relativeTime(i.at)}</span>
          </span>
          <span className="mt-0.5 block text-sm text-ink">{i.text}</span>
        </span>
        {i.unread ? <span className="sr-only">Unread</span> : null}
      </Link>
    </li>
  );
}
