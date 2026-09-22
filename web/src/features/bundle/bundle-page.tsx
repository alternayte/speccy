import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ListChecks } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Divider, useDivider } from "@/components/ui/divider";
import { ErrorState, Loading } from "@/components/ui/states";
import { EditorPane, type View } from "@/features/editor/editor-pane";
import {
  getBundleAccessOptions,
  getBundleOptions,
  listFilesOptions,
  listFindingsOptions,
  listHandoffsOptions,
  listWaiversOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { useMe } from "@/features/account/me";
import { GitHubControl } from "./github-control";
import { ShareDialog } from "./share-dialog";
import { ReviewStatus } from "./review-status";
import { type NewAnchor, ThreadsPanel } from "@/features/threads/threads-panel";
import type { Anchor, Finding, NextAction, Waiver } from "@/lib/api";
import { problemMessage } from "@/lib/problem";
import { EvidencePanel } from "./evidence-panel";
import { QuestionsPanel } from "./questions-panel";
import { Explorer } from "./explorer";
import { FindingsPanel, waiverCovers } from "./findings-panel";
import type { BundleSearch } from "./search";
import { RunProgress, RunReviewButton, useActiveRun } from "./run-review";
import { VersionsPanel } from "./versions-panel";
import { HandoffsPanel } from "./handoffs-panel";
import { VerificationsPanel } from "./verifications-panel";
import { ReviewerPage } from "@/features/review/reviewer-page";
import { ControlRow } from "./control-row";
import { adoptFrontmatterMutation } from "@/lib/api/@tanstack/react-query.gen";
import { useReviewerMode } from "@/features/review/mode";

type Panel = "files" | "rail" | null;
type RailTab = "findings" | "threads" | "evidence" | "history";

const railLabels: Record<RailTab, string> = {
  findings: "Findings",
  threads: "Threads",
  evidence: "Evidence",
  history: "History",
};

export function BundlePage({ bundleId, search }: { bundleId: string; search: BundleSearch }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  // Local mode: files on disk change without Speccy. Poll for new versions (REQ-005).
  const bundle = useQuery({ ...getBundleOptions({ path: { bundleId } }), refetchInterval: 2000 });
  const version = bundle.data?.current_version;
  const files = useQuery({
    ...listFilesOptions({ path: { bundleId }, query: { version: version?.id } }),
    enabled: !!version,
    // Keep the old list while a new version loads, so the editor stays mounted.
    placeholderData: keepPreviousData,
  });
  const [dirty, setDirty] = useState(false);
  const [panel, setPanel] = useState<Panel>(null);
  const [tab, setTab] = useState<RailTab>("findings");
  const [focus, setFocus] = useState<{ start: number; end: number; seq: number }>();
  // selectedFinding is the finding a click in the overlay picked; the rail scrolls to it.
  const [selectedFinding, setSelectedFinding] = useState<string>();
  // newThread is the anchor of a thread the user is starting (REQ-087).
  const [newThread, setNewThread] = useState<NewAnchor>();
  const startThread = (a: NewAnchor) => {
    setNewThread(a);
    setTab("threads");
    setPanel("rail");
  };

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const selected = search.file ?? bundle.data?.main_doc ?? "";
  const view: View = search.view ?? lastView();

  useEffect(() => {
    if (search.view) saveView(search.view);
  }, [search.view]);

  const setSearch = useCallback(
    (next: BundleSearch) => navigate({ to: "/bundles/$bundleId", params: { bundleId }, search: next, replace: true }),
    [navigate, bundleId],
  );

  const select = (path: string) => {
    if (path === selected) return true;
    if (dirty && !window.confirm("This file has unsaved edits. Leave it and discard them?")) return false;
    setDirty(false);
    setPanel(null);
    setSearch({ ...search, file: path });
    return true;
  };

  const openAnchor = (a: Anchor) => {
    if (!select(a.file)) return;
    setPanel(null);
    setFocus((prev) => ({ start: a.start, end: a.end, seq: (prev?.seq ?? 0) + 1 }));
  };
  const openFinding = (f: Finding) => openAnchor(f.anchor);

  const refresh = useCallback(() => {
    qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey });
  }, [qc, bundleId]);
  const run = useActiveRun(bundleId, refresh);
  const findingsRun = bundle.data?.verdict?.run_id;
  const findings = useQuery({ ...listFindingsOptions({ path: { runId: findingsRun ?? "" } }), enabled: !!findingsRun });
  const me = useMe();
  const hosted = me.data?.mode === "hosted";
  const guest = !!me.data?.guest;
  const access = useQuery({ ...getBundleAccessOptions({ path: { bundleId } }), enabled: hosted });
  // History holds the versions and the handoffs. A v1 doc with a handoff has history too.
  const handoffs = useQuery(listHandoffsOptions({ path: { bundleId } }));
  // Waivers that wait for this person: the next action opens the first one.
  const waivers = useQuery({ ...listWaiversOptions({ path: { bundleId } }), refetchInterval: 5000 });
  // openWaiver shows a waiver where it can be judged: the rail selects the finding it excuses,
  // and the preview focuses the whole section the waiver covers (SDD §9.1).
  const mainDoc = bundle.data?.main_doc;
  const openWaiver = useCallback(
    (w: Waiver) => {
      setTab("findings");
      setPanel("rail");
      const f = (findings.data?.items ?? []).find((f) => waiverCovers(w, f));
      if (f) setSelectedFinding(f.id);
      const range = w.section_range;
      if (!range || !mainDoc) return;
      setSearch({ ...search, file: mainDoc, waiver: undefined });
      setFocus((prev) => ({ start: range.start, end: range.end, seq: (prev?.seq ?? 0) + 1 }));
    },
    [findings.data, mainDoc, search, setSearch],
  );
  // A waiver link from the inbox names the waiver in the URL. Open it once, then drop the param.
  const opened = useRef<string>(undefined);
  useEffect(() => {
    const id = search.waiver;
    if (!id || opened.current === id) return;
    const w = waivers.data?.items.find((x) => x.id === id);
    if (!w || !findings.data) return;
    opened.current = id;
    openWaiver(w);
  }, [search.waiver, waivers.data, findings.data, openWaiver]);

  // The two dividers of the bundle screen. Below lg and xl the panes are overlays, so the widths
  // apply only where the panes sit side by side.
  const explorer = useDivider({ key: "speccy-explorer-width", from: "left", min: 180, max: 480, initial: 248 });
  const rail = useDivider({ key: "speccy-rail-width", from: "right", min: 260, max: 560, initial: 320 });
  const mode = useReviewerMode(bundleId);
  // The control row opens the same dialogs as the buttons it holds.
  const runReview = useRef<() => void>(undefined);
  const askReview = useRef<() => void>(undefined);
  const adopt = useMutation({ ...adoptFrontmatterMutation(), onSuccess: () => refresh() });

  // doNext does the one thing the server named (SDD §13.4).
  const doNext = (next?: NextAction) => {
    if (!next) return;
    switch (next.kind) {
      case "waiver": {
        const w = waivers.data?.items.find((x) => x.id === next.waiver_id);
        if (w) openWaiver(w);
        return;
      }
      case "decide":
        navigate({ to: "/bundles/$bundleId/tour", params: { bundleId } });
        return;
      case "fix": {
        setTab("findings");
        setPanel("rail");
        const f = (findings.data?.items ?? []).find((x) => x.id === next.finding_id);
        if (f) {
          setSelectedFinding(f.id);
          openFinding(f);
        }
        return;
      }
      case "review":
        runReview.current?.();
        return;
      case "adopt":
        adopt.mutate({ path: { bundleId } });
        return;
      case "request_review":
        askReview.current?.();
        return;
      case "handoff":
        setTab("history");
        setPanel("rail");
        return;
    }
  };
  // Local mode: the one user edits everything. Hosted: authors and admins (SDD §3).
  const canEdit = !hosted || !!access.data?.can_edit;

  if (mode.pending) return <Loading label="Loading the bundle" />;
  if (mode.reviewer) return <ReviewerPage bundleId={bundleId} />;

  if (bundle.isPending) return <Loading label="Loading the bundle" />;
  if (bundle.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(bundle.error)} />
      </div>
    );
  const b = bundle.data;
  const file = files.data?.items.find((f) => f.path === selected);
  // Each panel earns its place from the doc's state: an empty tab teaches nothing (SDD §13.4).
  const reviewed = !!b.verdict;
  const assets = (files.data?.items.length ?? 1) > 1;
  const tabs: RailTab[] = [
    ...(reviewed ? (["findings"] as const) : []),
    "threads",
    ...(b.verdict?.kind === "full" ? (["evidence"] as const) : []),
    ...(b.current_version.number > 1 || (handoffs.data?.items.length ?? 0) > 0 ? (["history"] as const) : []),
  ];
  const shownTab = tabs.includes(tab) ? tab : "threads";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ControlRow
        bundle={b}
        next={b.next_action}
        busy={!!run.active}
        onNext={() => doNext(b.next_action)}
        onFiles={() => setPanel(panel === "files" ? null : "files")}
        onRunReview={() => runReview.current?.()}
        onRequestReview={hosted ? () => askReview.current?.() : undefined}
        onExport={(format) =>
          window.location.assign(`/api/v1/bundles/${bundleId}/export${format === "html" ? "?format=html" : ""}`)
        }
        onPrint={() => {
          if (view === "code") setSearch({ ...search, view: "preview" });
          setTimeout(() => window.print(), 300);
        }}
        extra={
          <>
            <GitHubControl bundle={b} canEdit={canEdit} />
            <ReviewStatus bundleId={bundleId} signedIn register={(f) => (askReview.current = f)} />
            {hosted ? <ShareDialog bundleId={bundleId} /> : null}
            <RunReviewButton
              bundleId={bundleId}
              active={!!run.active}
              onStarted={() => run.refetch()}
              register={(f) => (runReview.current = f)}
              button={false}
            />
            <Button
              variant="ghost"
              size="sm"
              className="xl:hidden"
              aria-label="Findings and history"
              icon={<ListChecks className="size-4" />}
              onClick={() => setPanel(panel === "rail" ? null : "rail")}
            />
          </>
        }
      />

      <div className="no-print">{run.active ? <RunProgress events={run.events} /> : null}</div>

      <div className="relative flex min-h-0 flex-1">
        <aside
          style={{ width: explorer.width }}
          className={clsx(
            "no-print shrink-0 border-r border-line bg-surface",
            panel === "files"
              ? "absolute inset-y-0 left-0 z-20 shadow-pop lg:static lg:shadow-none"
              : assets
                ? "hidden lg:block"
                : "hidden",
          )}
        >
          {files.isError ? (
            <div className="p-2">
              <ErrorState message={problemMessage(files.error)} />
            </div>
          ) : files.data ? (
            <Explorer
              bundleId={bundleId}
              baseVersion={b.current_version.id}
              files={files.data.items}
              selected={selected}
              onSelect={select}
              onChanged={(path) => {
                refresh();
                if (path) setSearch({ ...search, file: path });
              }}
              readOnly={!canEdit}
            />
          ) : (
            <Loading label="Loading files" />
          )}
        </aside>

        <Divider
          label="Width of the file explorer"
          className={assets ? "hidden lg:block" : "hidden"}
          {...explorer.props}
        />

        <main className="min-w-0 flex-1">
          {files.data && !file ? (
            <div className="p-6">
              <ErrorState
                message={`Version ${b.current_version.number} of the bundle has no file ${selected}.`}
                action={
                  <Button size="sm" onClick={() => setSearch({ view: search.view })}>
                    Open the main doc
                  </Button>
                }
              />
            </div>
          ) : file ? (
            <EditorPane
              key={selected}
              bundleId={bundleId}
              path={selected}
              version={{ id: files.data!.version.id, number: files.data!.version.number }}
              sha={file.sha256}
              profileKey={b.profile_key}
              view={view}
              onViewChange={(v) => setSearch({ ...search, view: v })}
              onOpenPath={select}
              onSaved={refresh}
              onDirtyChange={setDirty}
              focus={focus}
              readOnly={!canEdit}
              findings={findings.data?.items}
              onOpenFinding={(f) => {
                setTab("findings");
                setPanel("rail");
                setSelectedFinding(f.id);
              }}
              onComment={(sel) =>
                startThread({
                  kind: "text",
                  anchor: {
                    file: sel.file,
                    start: sel.start,
                    end: sel.end,
                    quote: sel.quote,
                    prefix: "",
                    suffix: "",
                    heading_path: [],
                  },
                  label: sel.quote.length > 120 ? `${sel.quote.slice(0, 119)}…` : sel.quote,
                })
              }
            />
          ) : (
            <Loading />
          )}
        </main>

        <Divider label="Width of the review rail" className="hidden xl:block" {...rail.props} />

        <aside
          style={{ width: rail.width }}
          className={clsx(
            "no-print shrink-0 border-l border-line bg-surface",
            panel === "rail"
              ? "absolute inset-y-0 right-0 z-20 shadow-pop max-xl:!w-[min(100%,360px)] xl:static xl:shadow-none"
              : "hidden xl:block",
          )}
        >
          <div className="flex h-full min-h-0 flex-col">
            <div
              role="tablist"
              aria-label="Review"
              className="flex h-10 shrink-0 items-end gap-2 border-b border-line px-3 whitespace-nowrap"
            >
              {tabs.map((t) => (
                <button
                  key={t}
                  role="tab"
                  type="button"
                  aria-selected={shownTab === t}
                  onClick={() => setTab(t)}
                  className={clsx(
                    "-mb-px border-b-2 pb-2 text-2xs font-semibold tracking-[var(--tracking-caps)] uppercase transition-colors",
                    shownTab === t ? "border-accent text-ink" : "border-transparent text-ink-3 hover:text-ink-2",
                  )}
                >
                  {railLabels[t]}
                  {t === "findings" && b.verdict ? (
                    <span className="ml-1 font-mono text-ink-3">
                      {b.verdict.must + b.verdict.should + b.verdict.info}
                    </span>
                  ) : null}
                  {t === "threads" && b.verdict?.blocking_threads ? (
                    <span className="ml-1 font-mono text-bad">{b.verdict.blocking_threads}</span>
                  ) : null}
                </button>
              ))}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              {shownTab === "findings" ? (
                <FindingsPanel
                  runId={b.verdict?.run_id}
                  selected={selectedFinding}
                  canEdit={canEdit && !guest}
                  bundleId={bundleId}
                  member={!guest}
                  onOpen={openFinding}
                  onOpenWaiver={openWaiver}
                  onDiscuss={(f) =>
                    startThread({
                      kind: "finding",
                      finding_id: f.id,
                      check_slug: f.check_slug,
                      label: `${f.check_slug}: ${f.message}`,
                    })
                  }
                />
              ) : shownTab === "threads" ? (
                <ThreadsPanel
                  bundleId={bundleId}
                  member={!guest}
                  pending={newThread}
                  onPendingDone={() => setNewThread(undefined)}
                  onOpenAnchor={openAnchor}
                />
              ) : shownTab === "evidence" ? (
                <>
                  <EvidencePanel
                    bundleId={bundleId}
                    runId={b.verdict?.kind === "full" ? b.verdict.run_id : undefined}
                    version={b.current_version.id}
                    onOpen={openAnchor}
                  />
                  <h3 className="mt-2 border-t border-line px-3 pt-3 text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
                    Build questions
                  </h3>
                  <QuestionsPanel
                    runId={b.verdict?.kind === "full" ? b.verdict.run_id : undefined}
                    onOpen={openAnchor}
                  />
                </>
              ) : (
                <>
                  <VersionsPanel bundleId={bundleId} current={b.current_version.id} />
                  <HandoffsPanel bundleId={bundleId} current={b.current_version.number} />
                  <VerificationsPanel bundleId={bundleId} />
                </>
              )}
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

// The view a person picked last, in this browser. A first visit gets the preview: it is the
// only view a non-technical author needs (SDD §13.4).
function lastView(): View {
  try {
    const v = localStorage.getItem("speccy-view");
    if (v === "code" || v === "split" || v === "preview") return v;
  } catch {
    // Storage is not available: the preview it is.
  }
  return "preview";
}

function saveView(v: View) {
  try {
    localStorage.setItem("speccy-view", v);
  } catch {
    // Storage is not available: the choice lasts for this page load.
  }
}
