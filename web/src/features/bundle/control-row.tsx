import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import {
  ChevronDown,
  CircleAlert,
  Compass,
  Download,
  FileText,
  FolderTree,
  MessageSquarePlus,
  Network,
  PackageCheck,
  Play,
  Printer,
  Trash2,
  Wrench,
  Tags,
} from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Menu, MenuItem } from "@/components/ui/menu";
import type { BundleVerdict, SpecDoc, NextAction } from "@/lib/api";
import { verdictLabel } from "./verdict";

const kindIcon: Record<NextAction["kind"], React.ReactNode> = {
  waiver: <MessageSquarePlus className="size-3.5" />,
  decide: <Compass className="size-3.5" />,
  fix: <Wrench className="size-3.5" />,
  review: <Play className="size-3.5" />,
  adopt: <FileText className="size-3.5" />,
  request_review: <MessageSquarePlus className="size-3.5" />,
  handoff: <PackageCheck className="size-3.5" />,
};

// ControlRow is the one row above the panes (SDD §13.4): the title, the verdict in words, and
// the next action as the only primary button. Everything else is in the More menu.
export function ControlRow({
  bundle,
  next,
  onNext,
  busy,
  onFiles,
  onDelete,
  onProfile,
  onRunReview,
  onRequestReview,
  onExport,
  onHandoff,
  onPrint,
  extra,
}: {
  bundle: SpecDoc;
  next?: NextAction;
  onNext: () => void;
  // busy is true while a review runs: the next action waits for it.
  busy: boolean;
  onFiles: () => void;
  // onDelete opens the delete dialog. It is absent for a person who may not delete.
  onDelete?: () => void;
  // onProfile opens the doc type dialog. It is absent for a person who may not edit.
  onProfile?: () => void;
  onRunReview: () => void;
  // onRequestReview asks for the reviews the profile needs. Hosted mode only.
  onRequestReview?: () => void;
  onExport: (format: "html" | "zip") => void;
  // onHandoff opens the Hand it to a builder dialog. It is absent for a guest.
  onHandoff?: () => void;
  onPrint: () => void;
  // extra holds the controls that only some bundles have, such as GitHub and Share.
  extra?: React.ReactNode;
}) {
  const navigate = useNavigate();
  // showError opens the full cause of a failed review, which is often long.
  const [showError, setShowError] = useState(false);
  const go = (to: string, params: Record<string, string>) => navigate({ to, params });
  const v = bundle.verdict;
  const tone =
    !v || v.result === "stale"
      ? "text-ink-2"
      : v.result === "build_ready"
        ? "text-ok"
        : v.result === "not_build_ready"
          ? "text-bad"
          : "text-ink-2";
  return (
    <div className="no-print flex min-h-12 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-2 sm:px-4">
      <div className="min-w-0 flex-1">
        <h1 className="truncate text-lg leading-tight font-semibold tracking-tight">{bundle.title}</h1>
        <p className="truncate text-xs">
          <span className={clsx("font-medium", tone)}>
            {bundle.run_error && !v ? "Speccy cannot review this doc" : v ? verdictLabel(v) : "Not checked yet"}
          </span>
          {v?.result === "stale" && v.stale_reason === "upstream_changed" ? (
            <span className="text-ink-2">
              {" · "}
              <UpstreamLinks verdict={v} /> changed after this review
            </span>
          ) : null}
          <span className="text-ink-3">
            {" · "}
            {bundle.slug} · v{bundle.current_version.number}
          </span>
          {v?.ai_version_number ? (
            <span className="text-ink-3">
              {" · "}AI review from v{v.ai_version_number}
              {v.sections_changed
                ? ` · ${v.sections_changed} section${v.sections_changed === 1 ? "" : "s"} changed`
                : ""}
            </span>
          ) : null}
        </p>
      </div>
      {bundle.run_error ? (
        <button
          type="button"
          aria-expanded={showError}
          onClick={() => setShowError((v) => !v)}
          className="inline-flex items-center gap-1 text-xs text-bad hover:underline"
        >
          <CircleAlert aria-hidden className="size-3.5" />
          The last review failed
        </button>
      ) : null}
      {extra}
      {next ? (
        // On a phone the row wraps, and a long next action stays inside it: the sentence
        // truncates, and its title holds the whole of it.
        <Button
          variant="primary"
          size="sm"
          icon={kindIcon[next.kind]}
          disabled={busy}
          onClick={onNext}
          title={busy ? undefined : next.sentence}
          className="max-w-full"
        >
          <span className="min-w-0 truncate">{busy ? "Reviewing" : next.sentence}</span>
        </Button>
      ) : null}
      <Menu
        trigger={
          <Button variant="ghost" size="sm" aria-label="More">
            More
            <ChevronDown aria-hidden className="size-3.5" />
          </Button>
        }
      >
        <MenuItem icon={<Play className="size-3.5" />} onSelect={onRunReview}>
          Run a review
        </MenuItem>
        {onRequestReview ? (
          <MenuItem icon={<MessageSquarePlus className="size-3.5" />} onSelect={onRequestReview}>
            Request a review
          </MenuItem>
        ) : null}
        {onHandoff ? (
          <MenuItem icon={<PackageCheck className="size-3.5" />} onSelect={onHandoff}>
            Hand it to a builder
          </MenuItem>
        ) : null}
        <MenuItem icon={<FolderTree className="size-3.5" />} onSelect={onFiles}>
          Files
        </MenuItem>
        <MenuItem
          icon={<Compass className="size-3.5" />}
          onSelect={() => go("/bundles/$bundleId/docs/$docId/tour", { docId: bundle.id })}
        >
          Tour
        </MenuItem>
        <MenuItem
          icon={<Network className="size-3.5" />}
          onSelect={() => go("/bundles/$bundleId/docs/$docId/trace", { docId: bundle.id })}
        >
          Traceability
        </MenuItem>
        {v ? (
          <MenuItem
            icon={<FileText className="size-3.5" />}
            onSelect={() => go("/bundles/$bundleId/docs/$docId/runs/$runId", { docId: bundle.id, runId: v.run_id })}
          >
            Run report
          </MenuItem>
        ) : null}
        <MenuItem icon={<FileText className="size-3.5" />} onSelect={() => onExport("html")}>
          HTML report
        </MenuItem>
        <MenuItem icon={<Printer className="size-3.5" />} onSelect={onPrint}>
          Print, or save as PDF
        </MenuItem>
        <MenuItem icon={<Download className="size-3.5" />} onSelect={() => onExport("zip")}>
          Bundle as .zip
        </MenuItem>
        {onProfile ? (
          <MenuItem icon={<Tags className="size-3.5" />} onSelect={onProfile}>
            Change doc type ({bundle.profile_key.toUpperCase()})
          </MenuItem>
        ) : null}
        {onDelete ? (
          <MenuItem icon={<Trash2 className="size-3.5" />} onSelect={onDelete}>
            Delete
          </MenuItem>
        ) : null}
      </Menu>
      {bundle.run_error && showError ? (
        <p role="alert" className="w-full rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-xs text-bad">
          {bundle.run_error}
        </p>
      ) : null}
    </div>
  );
}

// UpstreamLinks names the linked spec docs that changed after the review read them, each a
// link to that spec doc, so the author knows what to read before the next review.
function UpstreamLinks({ verdict }: { verdict: BundleVerdict }) {
  const ups = verdict.stale_upstream ?? [];
  if (ups.length === 0) return <>A linked spec doc</>;
  return (
    <>
      {ups.map((u, i) => (
        <span key={u.id}>
          {i === 0 ? "" : i === ups.length - 1 ? " and " : ", "}
          <Link
            to="/docs/$docId"
            params={{ docId: u.id }}
            className="font-medium text-ink underline underline-offset-2"
            title={u.slug}
          >
            {u.title || u.slug}
          </Link>
        </span>
      ))}
    </>
  );
}
