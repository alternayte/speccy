import { useBundleId } from "@/features/bundle/params";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft, CircleCheck, CircleMinus, CircleX, Hash, Share2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { BundleLink, BundleRef, TraceCell, TraceMatrix, TraceView } from "@/lib/api";
import {
  addTraceIdsMutation,
  getBundleAccessOptions,
  getSpecDocOptions,
  getTraceOptions,
  listVerificationsOptions,
  removeLinkMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { useMe } from "@/features/account/me";
import { problemMessage } from "@/lib/problem";
import { GapAnswer } from "@/features/trace/gap-answer";
import { Withdraw } from "@/features/trace/withdraw";

const cellStyle = {
  referenced: { icon: CircleCheck, tone: "text-ok", text: "Referenced" },
  covered_by: { icon: Share2, tone: "text-ink-2", text: "Covered by another doc" },
  out_of_scope: { icon: CircleMinus, tone: "text-ink-2", text: "Out of scope" },
  gap: { icon: CircleX, tone: "text-bad", text: "Not covered" },
} as const;

const kindText: Record<BundleLink["kind"], string> = {
  implements: "Implements",
  refines: "Refines",
  references: "References",
  supersedes: "Supersedes",
  "implemented-by": "Implemented by",
};

// stateStyle is the four states of an external link (DEC-021).
const stateStyle = {
  aligned: { tone: "text-ok border-ok/40", text: "Aligned" },
  drifted: { tone: "text-warn border-warn/40", text: "Drifted" },
  conflicting: { tone: "text-bad border-bad/40", text: "Conflicting" },
  unchecked: { tone: "text-ink-3 border-line", text: "Unchecked" },
} as const;

// TracePage shows a bundle's links, the traceability matrices it takes part in (REQ-058), and
// suggested trace IDs for unnumbered items (REQ-052).
export function TracePage({ docId }: { docId: string }) {
  const bundleId = useBundleId();
  const bundle = useQuery(getSpecDocOptions({ path: { docId } }));
  const trace = useQuery(getTraceOptions({ path: { docId } }));
  const hosted = useMe().data?.mode === "hosted";
  const access = useQuery({ ...getBundleAccessOptions({ path: { bundleId } }), enabled: hosted });
  const canEdit = !hosted || !!access.data?.can_edit;
  // notice says what the last Add IDs or Remove did. The suggestions and the link leave the
  // page with the new version, so the page, not the list, keeps the message.
  const [notice, setNotice] = useState<string>();
  // unlink is how a row removes its link. It is absent when this person cannot.
  const unlink =
    bundle.data && canEdit ? { docId, baseVersion: bundle.data.current_version.id, onDone: setNotice } : undefined;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[1200px] px-3 py-6 sm:px-6">
        <Link
          to="/bundles/$bundleId/docs/$docId"
          params={{ bundleId, docId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> Back to the bundle
        </Link>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">Traceability</h1>
        {bundle.data ? <p className="mt-0.5 text-sm text-ink-2">{bundle.data.title}</p> : null}
        {notice ? (
          <p role="status" className="mt-3 text-sm text-ok">
            {notice}
          </p>
        ) : null}

        {trace.isPending ? (
          <Loading label="Loading links and trace IDs" />
        ) : trace.isError ? (
          <div className="mt-4">
            <ErrorState message={problemMessage(trace.error)} />
          </div>
        ) : (
          <>
            <WhyEmpty trace={trace.data} docId={docId} />
            <section className="mt-6">
              <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Links</h2>
              {trace.data.standalone ? (
                <p className="mt-2 text-sm">
                  <span className="rounded-sm border border-line-strong px-1.5 py-px text-xs font-medium">
                    Standalone (acknowledged)
                  </span>{" "}
                  <span className="text-ink-2">
                    {trace.data.standalone.reason} — {trace.data.standalone.acknowledged_by}
                  </span>
                  {canEdit ? <Withdraw docId={docId} onDone={setNotice} className="ml-2" /> : null}
                </p>
              ) : null}
              {trace.data.links.filter((l) => l.target_kind !== "external").length === 0 &&
              trace.data.incoming.length === 0 &&
              !trace.data.standalone ? (
                <p className="mt-2 text-sm text-ink-3">
                  This bundle has no links. Add one under links: in the frontmatter, or a link rule in .speccy.yaml.
                </p>
              ) : (
                <ul className="mt-2 space-y-1 text-sm">
                  {trace.data.links
                    .filter((l) => l.target_kind !== "external")
                    .map((l) => (
                      <LinkRow key={`out-${l.kind}-${l.target_ref}`} link={l} unlink={unlink} />
                    ))}
                  {trace.data.incoming.map((l) => (
                    <LinkRow key={`in-${l.kind}-${l.target_ref}`} link={l} incoming />
                  ))}
                </ul>
              )}
            </section>

            <ExternalLinks links={trace.data.links.filter((l) => l.target_kind === "external")} unlink={unlink} />

            <CodeAndTests docId={docId} />

            <section className="mt-8">
              <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Coverage</h2>
              {trace.data.matrices.length === 0 ? (
                <div className="mt-2">
                  <Empty title="No traceability matrix">
                    A matrix appears when one bundle implements another, and the upstream doc defines trace IDs.
                  </Empty>
                </div>
              ) : (
                trace.data.matrices.map((m) => <Matrix key={m.upstream.id} matrix={m} onNotice={setNotice} />)
              )}
            </section>

            {bundle.data && canEdit ? (
              <Suggestions
                key={trace.data.suggestions.map((s) => s.id).join()}
                docId={docId}
                baseVersion={bundle.data.current_version.id}
                items={trace.data.suggestions}
                onAdded={setNotice}
              />
            ) : null}
          </>
        )}
      </div>
    </div>
  );
}

// ExternalLinks is the one table of the issues, pages, and code this doc links to, with the
// state the last review run read (DEC-021).
function ExternalLinks({ links, unlink }: { links: BundleLink[]; unlink?: UnlinkProps }) {
  if (links.length === 0) return null;
  return (
    <section className="mt-8">
      <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">External links</h2>
      <ul className="mt-2 divide-y divide-line rounded-md border border-line">
        {links.map((l) => {
          const state = stateStyle[l.state ?? "unchecked"];
          return (
            <li
              key={`${l.kind}-${l.target_ref}`}
              className="grid grid-cols-[minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 px-3 py-2 sm:grid-cols-[minmax(0,22rem)_5.5rem_7rem_minmax(0,1fr)_auto_auto]"
            >
              {l.target_url ? (
                <a
                  href={l.target_url}
                  target="_blank"
                  rel="noreferrer"
                  className="truncate font-mono text-xs text-ink hover:text-accent"
                >
                  {l.target_ref}
                </a>
              ) : (
                <span className="truncate font-mono text-xs text-ink">{l.target_ref}</span>
              )}
              <span
                className={clsx("justify-self-start rounded-sm border px-1.5 py-px text-2xs font-medium", state.tone)}
              >
                {state.text}
              </span>
              <span className="truncate text-xs text-ink-3">{kindText[l.kind]}</span>
              <span className="min-w-0 text-xs text-ink-2">{l.state_reason ?? ""}</span>
              <span className="text-2xs text-ink-3">
                {l.checked_at ? `read ${new Date(l.checked_at).toLocaleDateString()}` : ""}
              </span>
              <span className="justify-self-start sm:justify-self-end">
                <LinkSource link={l} unlink={unlink} />
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function LinkRow({ link: l, incoming, unlink }: { link: BundleLink; incoming?: boolean; unlink?: UnlinkProps }) {
  const label = incoming ? `${kindText[l.kind]} this doc` : kindText[l.kind];
  return (
    <li className="flex flex-wrap items-baseline gap-x-2">
      {incoming && l.bundle ? <BundleName id={l.bundle.id} title={l.bundle.title} slug={l.bundle.slug} /> : null}
      <span className="text-ink-2">{label}</span>
      {!incoming ? (
        l.bundle ? (
          <BundleName id={l.bundle.id} title={l.bundle.title} slug={l.bundle.slug} />
        ) : l.target_kind === "external" ? (
          <span className="font-mono text-xs text-ink-2">{l.target_ref}</span>
        ) : (
          <span className="text-bad">
            <span className="font-mono text-xs">{l.target_ref}</span> — no bundle has this name
          </span>
        )
      ) : null}
      {incoming ? (
        l.origin === "rule" ? (
          <span className="text-xs text-ink-3">(link rule)</span>
        ) : null
      ) : (
        <LinkSource link={l} unlink={unlink} />
      )}
    </li>
  );
}

type UnlinkProps = { docId: string; baseVersion: string; onDone: (message: string) => void };

// LinkSource says where an outgoing link lives, and removes it where Speccy holds it: an adopted
// link, or the frontmatter of a doc Speccy writes. A link rule and a repo doc's frontmatter stay
// with .speccy.yaml and the repo. Remove asks once, because an implements link may be what
// passes links.has-upstream and what draws the coverage matrix.
function LinkSource({ link: l, unlink }: { link: BundleLink; unlink?: UnlinkProps }) {
  const qc = useQueryClient();
  const [asking, setAsking] = useState(false);
  const name = l.bundle?.title ?? l.target_ref;
  const remove = useMutation({
    ...removeLinkMutation(),
    onSuccess: (r) => {
      unlink?.onDone(`Removed the link to ${name}${r.version ? ` as version ${r.version.number}` : ""}.`);
      qc.invalidateQueries();
    },
  });
  if (l.origin === "rule") return <span className="text-xs text-ink-3">(link rule)</span>;
  if (!l.removable)
    return l.origin === "frontmatter" ? <span className="text-xs text-ink-3">(in the repo)</span> : null;
  if (!unlink) return null;
  if (!asking)
    return (
      <button type="button" onClick={() => setAsking(true)} className="text-xs text-ink-3 hover:text-bad">
        Remove
      </button>
    );
  const where =
    l.origin === "adopted"
      ? "Speccy forgets the link. The repo takes no commit."
      : "Speccy takes the link out of the frontmatter as a new version.";
  return (
    <span className="flex basis-full flex-wrap items-center gap-x-2 gap-y-1 text-xs">
      <span className="text-ink-2">
        {where}
        {l.kind === "implements" ? " Without it, the doc can fail links.has-upstream." : ""}
      </span>
      <Button
        size="sm"
        variant="danger"
        disabled={remove.isPending}
        onClick={() =>
          remove.mutate({
            path: { docId: unlink.docId },
            query: { base_version: unlink.baseVersion, kind: l.kind, target: l.target_ref },
          })
        }
      >
        {remove.isPending ? "Removing" : "Remove the link"}
      </Button>
      <Button size="sm" variant="ghost" onClick={() => setAsking(false)}>
        Cancel
      </Button>
      {remove.isError ? <span className="basis-full text-bad">{problemMessage(remove.error)}</span> : null}
    </span>
  );
}

function BundleName({ id, title, slug }: { id: string; title: string; slug: string }) {
  return (
    <Link to="/docs/$docId" params={{ docId: id }} className="font-medium text-ink underline underline-offset-2">
      {title} <span className="font-mono text-xs font-normal text-ink-3">{slug}</span>
    </Link>
  );
}

// CodeAndTests is the code column and the test column of the newest verification run, so one
// view answers where a requirement is and what tests it. A cited test is a citation, not a
// pass: Speccy reads the code and runs nothing.
function CodeAndTests({ docId }: { docId: string }) {
  const runs = useQuery(listVerificationsOptions({ path: { docId } }));
  const run = runs.data?.items?.[0];
  if (!run || run.outcomes.length === 0) return null;
  const rows = [...run.outcomes].sort((a, b) => a.trace_id.localeCompare(b.trace_id));
  const where = (o: (typeof rows)[number], kind: "code" | "test") => {
    const t = o.targets.find((t) => t.kind === kind && t.holds);
    return t ? `${t.path}:${t.line ?? 0}` : "";
  };
  return (
    <section className="mt-8">
      <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Code and tests</h2>
      <p className="mt-1 text-sm text-ink-2">
        {run.repo} at {run.sha ? run.sha.slice(0, 7) : "a folder"}
        {run.stale ? " · the bundle changed after this run" : ""}
      </p>
      <div className="mt-2 overflow-x-auto rounded-md border border-line">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-line bg-sunken text-left text-xs text-ink-2">
              <th scope="col" className="px-4 py-2.5 font-medium">
                Trace ID
              </th>
              <th scope="col" className="px-4 py-2.5 font-medium">
                Outcome
              </th>
              <th scope="col" className="px-4 py-2.5 font-medium">
                Code
              </th>
              <th scope="col" className="px-4 py-2.5 font-medium">
                Test cited
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((o) => (
              <tr key={o.trace_id} className="border-b border-line last:border-b-0">
                <th scope="row" className="px-4 py-3 text-left font-mono text-xs font-semibold">
                  {o.trace_id}
                </th>
                <td
                  className={clsx(
                    "px-4 py-3 text-xs",
                    (o.outcome === "missing" || o.outcome === "breached") && "bg-bad/10 text-bad",
                  )}
                >
                  {o.outcome}
                  {o.waived ? " (waived)" : ""}
                </td>
                <td className="px-4 py-3 font-mono text-xs text-ink-2">{where(o, "code")}</td>
                <td className="px-4 py-3 font-mono text-xs text-ink-2">{where(o, "test")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// Answering is the gap cell whose three answers are open: the column's spec doc, the row's ID,
// and the coverage finding the answer closes.
type Answering = { doc: BundleRef; traceId: string; findingId: string };

function Matrix({ matrix: m, onNotice }: { matrix: TraceMatrix; onNotice: (message: string) => void }) {
  const [answering, setAnswering] = useState<Answering>();
  const gaps = m.cells.flat().filter((c) => c.state === "gap").length;
  const none = m.rows.length === 0;
  return (
    <div className="mt-3">
      <p className="text-sm">
        IDs of{" "}
        <Link to="/docs/$docId" params={{ docId: m.upstream.id }} className="font-medium underline underline-offset-2">
          {m.upstream.title}
        </Link>
        <span className={clsx("ml-2 text-xs", gaps ? "text-bad" : "text-ink-3")}>
          {none ? "No trace IDs" : gaps ? `${gaps} gap${gaps === 1 ? "" : "s"}` : "No gaps"}
        </span>
      </p>
      {none ? (
        <p className="mt-1 text-sm text-ink-3">
          {m.upstream.title} defines no trace IDs, so coverage does not apply. Open it, and add IDs from its
          Traceability page.
        </p>
      ) : (
        <div className="mt-2 overflow-x-auto rounded-md border border-line">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-line bg-sunken text-left text-xs text-ink-2">
                <th scope="col" className="sticky left-0 z-10 bg-sunken px-4 py-2.5 font-medium">
                  Upstream ID
                </th>
                {m.columns.map((c) => (
                  <th key={c.id} scope="col" className="px-4 py-2.5 font-medium">
                    <Link to="/docs/$docId" params={{ docId: c.id }} className="hover:underline">
                      {c.title}
                    </Link>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {m.rows.map((r, i) => (
                <tr key={r.id} className="border-b border-line last:border-b-0">
                  <th
                    scope="row"
                    className="sticky left-0 z-10 w-[45vw] max-w-[360px] min-w-[150px] bg-surface px-4 py-3 text-left font-normal"
                  >
                    <span className="font-mono text-xs font-semibold">{r.id}</span>
                    <span className="mt-0.5 line-clamp-2 block text-xs text-ink-2">
                      {r.text.replace(/^\S+:\s*/, "")}
                    </span>
                  </th>
                  {(m.cells[i] ?? []).map((c, j) => {
                    const doc = m.columns[j];
                    if (!doc) return null;
                    return (
                      <Cell
                        key={doc.id}
                        cell={c}
                        doc={doc}
                        traceId={r.id}
                        editable={!!m.editable[j]}
                        onNotice={onNotice}
                        onAnswer={() => c.finding_id && setAnswering({ doc, traceId: r.id, findingId: c.finding_id })}
                      />
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Dialog
        open={!!answering}
        onOpenChange={(o) => {
          if (!o) setAnswering(undefined);
        }}
        title={answering ? `Answer ${answering.traceId} for ${answering.doc.title}` : "Answer the gap"}
        description={answering ? `Does ${answering.doc.title} cover ${answering.traceId}?` : undefined}
      >
        {answering ? (
          <GapAnswer
            docId={answering.doc.id}
            findingId={answering.findingId}
            traceId={answering.traceId}
            question={false}
            onDone={(message) => {
              setAnswering(undefined);
              onNotice(`${answering.doc.title}: ${message}`);
            }}
          />
        ) : null}
      </Dialog>
    </div>
  );
}

// Cell is one column's coverage of one upstream ID. A person who can edit the column's spec
// doc answers a gap there, and withdraws an acknowledgement. A reference is prose the author
// wrote, so it links to its place in the editor and has no Withdraw.
function Cell({
  cell: c,
  doc,
  traceId,
  editable,
  onNotice,
  onAnswer,
}: {
  cell: TraceCell;
  doc: BundleRef;
  traceId: string;
  editable: boolean;
  onNotice: (message: string) => void;
  onAnswer: () => void;
}) {
  const { icon: Icon, tone, text } = cellStyle[c.state];
  const detail = c.state === "covered_by" ? `${c.target ?? ""}: ${c.reason ?? ""}` : (c.reason ?? "");
  return (
    <td className={clsx("px-4 py-3 align-top", c.state === "gap" && "bg-bad/10")}>
      <span className={clsx("inline-flex items-center gap-1 text-xs font-medium", tone)}>
        <Icon aria-hidden className="size-3.5" />
        {text}
      </span>
      {c.state === "referenced" ? (
        <span className="mt-0.5 flex flex-col items-start gap-0.5">
          {c.refs.map((ref) => (
            <Link
              key={`${ref.start}-${ref.end}`}
              to="/bundles/$bundleId/docs/$docId"
              params={{ bundleId: doc.bundle_id, docId: doc.id }}
              search={{ file: ref.file, at: `${ref.start}-${ref.end}` }}
              title={`Open this reference in ${doc.title}`}
              className="max-w-[16rem] truncate text-xs text-ink-2 underline decoration-line-strong underline-offset-2 hover:text-accent hover:decoration-accent"
            >
              {/* The column names the doc, so the section says where in it. */}
              {ref.heading_path.at(-1) ?? ref.file}
            </Link>
          ))}
        </span>
      ) : detail ? (
        <span className="mt-0.5 block text-xs text-ink-3">{detail}</span>
      ) : null}
      {editable && (c.state === "covered_by" || c.state === "out_of_scope") ? (
        <Withdraw
          docId={doc.id}
          docTitle={doc.title}
          traceId={traceId}
          onDone={(m) => onNotice(`${doc.title}: ${m}`)}
          className="mt-1"
        />
      ) : null}
      {editable && c.state === "gap" && c.finding_id ? (
        <button type="button" onClick={onAnswer} className="mt-1 block text-xs font-medium text-accent hover:underline">
          Answer
        </button>
      ) : null}
    </td>
  );
}

function Suggestions({
  docId,
  baseVersion,
  items,
  onAdded,
}: {
  docId: string;
  baseVersion: string;
  items: { id: string; text: string }[];
  onAdded: (message: string) => void;
}) {
  const qc = useQueryClient();
  const [picked, setPicked] = useState<Set<string>>(() => new Set(items.map((s) => s.id)));
  const add = useMutation({
    ...addTraceIdsMutation(),
    onSuccess: (r) => {
      onAdded(`Added ${picked.size} ID${picked.size === 1 ? "" : "s"} as version ${r.version.number}.`);
      qc.invalidateQueries();
    },
  });
  if (items.length === 0) return null;
  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  return (
    <section className="mt-8">
      <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
        Items without an ID
      </h2>
      <p className="mt-1 text-sm text-ink-2">
        Other docs can reference an item only by its ID. Speccy adds the IDs you pick to the start of each item.
      </p>
      <ul className="mt-2 divide-y divide-line rounded-md border border-line">
        {items.map((s) => (
          <li key={s.id}>
            <label className="flex cursor-pointer items-start gap-3 px-3 py-2 text-sm hover:bg-sunken">
              <input
                type="checkbox"
                className="mt-1 accent-[var(--color-accent)]"
                checked={picked.has(s.id)}
                onChange={() => toggle(s.id)}
              />
              <span className="inline-flex shrink-0 items-center gap-1 font-mono text-xs font-semibold">
                <Hash aria-hidden className="size-3" />
                {s.id}
              </span>
              <span className="text-ink-2">{s.text}</span>
            </label>
          </li>
        ))}
      </ul>
      {add.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(add.error)} />
        </div>
      ) : null}
      <div className="mt-3">
        <Button
          variant="primary"
          size="sm"
          disabled={picked.size === 0 || add.isPending}
          onClick={() =>
            add.mutate({
              path: { docId },
              query: { base_version: baseVersion },
              body: { ids: [...picked] },
            })
          }
        >
          {add.isPending ? "Adding IDs" : `Add ${picked.size} ID${picked.size === 1 ? "" : "s"}`}
        </Button>
      </div>
    </section>
  );
}

// WhyEmpty explains a traceability page with nothing to trace: what the page shows, which of
// the three causes left it empty, and the one thing to do next (#55).
function WhyEmpty({ trace, docId }: { trace: TraceView; docId: string }) {
  const bundleId = useBundleId();
  const bundleLinks = trace.links.filter((l) => l.target_kind === "bundle");
  if (trace.matrices.length > 0 || trace.standalone) return null;
  const broken = bundleLinks.filter((l) => !l.bundle);
  let why: string;
  let next: React.ReactNode;
  if (broken.length > 0) {
    why = `The link to "${broken[0]!.target_ref}" does not resolve: no bundle has that slug or that path.`;
    next = (
      <>
        Change the target under <code>links:</code> to the other bundle&apos;s slug, or a path relative to this doc. The{" "}
        <Link to="/bundles/$bundleId/docs/$docId" params={{ bundleId, docId }} className="text-accent hover:underline">
          findings
        </Link>{" "}
        name the bundles that can match.
      </>
    );
  } else if (bundleLinks.length === 0 && trace.incoming.length === 0) {
    why = "This doc links to no other doc, and no doc links to it.";
    next = (
      <>
        On an SDD, Suggest fix on <code>links.has-upstream</code> lists the PRDs and writes the link. Or add it under{" "}
        <code>links:</code> in the frontmatter: <code>kind: implements</code> and the path of the PRD. A doc with no
        upstream doc takes Mark it standalone on the same finding, with a reason.
      </>
    );
  } else {
    why = "The linked docs define no trace IDs, such as REQ-001, so there is nothing to cover.";
    next = (
      <>
        Give the upstream doc&apos;s requirements trace IDs. Speccy suggests them below when it finds unnumbered items.
      </>
    );
  }
  return (
    <div className="mt-6 rounded-lg border border-line bg-surface px-4 py-3 text-sm">
      <p className="text-ink">
        This page shows how this doc covers the requirements of the docs it links to, and which code builds it.
      </p>
      <p className="mt-2 text-ink-2">{why}</p>
      <p className="mt-1 text-ink-2">{next}</p>
    </div>
  );
}
