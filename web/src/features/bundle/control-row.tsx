import { useNavigate } from "@tanstack/react-router";
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
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Menu, MenuItem } from "@/components/ui/menu";
import type { Bundle, NextAction } from "@/lib/api";
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
  onRunReview,
  onRequestReview,
  onExport,
  onPrint,
  extra,
}: {
  bundle: Bundle;
  next?: NextAction;
  onNext: () => void;
  // busy is true while a review runs: the next action waits for it.
  busy: boolean;
  onFiles: () => void;
  // onDelete opens the delete dialog. It is absent for a person who may not delete.
  onDelete?: () => void;
  onRunReview: () => void;
  // onRequestReview asks for the reviews the profile needs. Hosted mode only.
  onRequestReview?: () => void;
  onExport: (format: "html" | "zip") => void;
  onPrint: () => void;
  // extra holds the controls that only some bundles have, such as GitHub and Share.
  extra?: React.ReactNode;
}) {
  const navigate = useNavigate();
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
          <span className="text-ink-3">
            {" · "}
            {bundle.slug} · v{bundle.current_version.number}
          </span>
        </p>
      </div>
      {bundle.run_error ? (
        <span className="inline-flex items-center gap-1 text-xs text-bad" title={bundle.run_error}>
          <CircleAlert aria-hidden className="size-3.5" />
          The last review failed
        </span>
      ) : null}
      {extra}
      {next ? (
        <Button variant="primary" size="sm" icon={kindIcon[next.kind]} disabled={busy} onClick={onNext}>
          {busy ? "Reviewing" : next.sentence}
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
        <MenuItem icon={<FolderTree className="size-3.5" />} onSelect={onFiles}>
          Files
        </MenuItem>
        <MenuItem
          icon={<Compass className="size-3.5" />}
          onSelect={() => go("/bundles/$bundleId/tour", { bundleId: bundle.id })}
        >
          Tour
        </MenuItem>
        <MenuItem
          icon={<Network className="size-3.5" />}
          onSelect={() => go("/bundles/$bundleId/trace", { bundleId: bundle.id })}
        >
          Traceability
        </MenuItem>
        {v ? (
          <MenuItem
            icon={<FileText className="size-3.5" />}
            onSelect={() => go("/bundles/$bundleId/runs/$runId", { bundleId: bundle.id, runId: v.run_id })}
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
        {onDelete ? (
          <MenuItem icon={<Trash2 className="size-3.5" />} onSelect={onDelete}>
            Delete
          </MenuItem>
        ) : null}
      </Menu>
    </div>
  );
}
