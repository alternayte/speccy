import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft, CircleCheck, CircleMinus, CircleX, Hash, Share2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { BundleLink, TraceCell, TraceMatrix } from "@/lib/api";
import {
  addTraceIdsMutation,
  getBundleAccessOptions,
  getBundleOptions,
  getTraceOptions,
} from "@/lib/api/@tanstack/react-query.gen";
import { useMe } from "@/features/account/me";
import { problemMessage } from "@/lib/problem";

const cellStyle = {
  referenced: { icon: CircleCheck, tone: "text-ok", text: "Referenced" },
  covered_by: { icon: Share2, tone: "text-ink-2", text: "Covered by another bundle" },
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
export function TracePage({ bundleId }: { bundleId: string }) {
  const bundle = useQuery(getBundleOptions({ path: { bundleId } }));
  const trace = useQuery(getTraceOptions({ path: { bundleId } }));
  const hosted = useMe().data?.mode === "hosted";
  const access = useQuery({ ...getBundleAccessOptions({ path: { bundleId } }), enabled: hosted });
  const canEdit = !hosted || !!access.data?.can_edit;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[1200px] px-3 py-6 sm:px-6">
        <Link
          to="/bundles/$bundleId"
          params={{ bundleId }}
          className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        >
          <ArrowLeft aria-hidden className="size-3.5" /> Back to the bundle
        </Link>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">Traceability</h1>
        {bundle.data ? <p className="mt-0.5 text-sm text-ink-2">{bundle.data.title}</p> : null}

        {trace.isPending ? (
          <Loading label="Loading links and trace IDs" />
        ) : trace.isError ? (
          <div className="mt-4">
            <ErrorState message={problemMessage(trace.error)} />
          </div>
        ) : (
          <>
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
                      <LinkRow key={`out-${l.kind}-${l.target_ref}`} link={l} />
                    ))}
                  {trace.data.incoming.map((l) => (
                    <LinkRow key={`in-${l.kind}-${l.target_ref}`} link={l} incoming />
                  ))}
                </ul>
              )}
            </section>

            <ExternalLinks links={trace.data.links.filter((l) => l.target_kind === "external")} />

            <section className="mt-8">
              <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">Coverage</h2>
              {trace.data.matrices.length === 0 ? (
                <div className="mt-2">
                  <Empty title="No traceability matrix">
                    A matrix appears when one bundle implements another, and the upstream doc defines trace IDs.
                  </Empty>
                </div>
              ) : (
                trace.data.matrices.map((m) => <Matrix key={m.upstream.id} matrix={m} />)
              )}
            </section>

            {bundle.data && canEdit ? (
              <Suggestions
                key={trace.data.suggestions.map((s) => s.id).join()}
                bundleId={bundleId}
                baseVersion={bundle.data.current_version.id}
                items={trace.data.suggestions}
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
function ExternalLinks({ links }: { links: BundleLink[] }) {
  if (links.length === 0) return null;
  return (
    <section className="mt-8">
      <h2 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">External links</h2>
      <ul className="mt-2 divide-y divide-line rounded-md border border-line">
        {links.map((l) => {
          const state = stateStyle[l.state ?? "unchecked"];
          return (
            <li key={`${l.kind}-${l.target_ref}`} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2">
              {l.target_url ? (
                <a
                  href={l.target_url}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono text-xs text-ink hover:text-accent"
                >
                  {l.target_ref}
                </a>
              ) : (
                <span className="font-mono text-xs text-ink">{l.target_ref}</span>
              )}
              <span className={clsx("rounded-sm border px-1.5 py-px text-2xs font-medium", state.tone)}>
                {state.text}
              </span>
              <span className="text-xs text-ink-3">{kindText[l.kind]}</span>
              {l.state_reason ? <span className="min-w-0 flex-1 text-xs text-ink-2">{l.state_reason}</span> : null}
              {l.checked_at ? (
                <span className="ml-auto text-2xs text-ink-3">read {new Date(l.checked_at).toLocaleDateString()}</span>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function LinkRow({ link: l, incoming }: { link: BundleLink; incoming?: boolean }) {
  const label = incoming ? `${kindText[l.kind]} this bundle` : kindText[l.kind];
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
      {l.origin === "rule" ? <span className="text-xs text-ink-3">(link rule)</span> : null}
    </li>
  );
}

function BundleName({ id, title, slug }: { id: string; title: string; slug: string }) {
  return (
    <Link
      to="/bundles/$bundleId"
      params={{ bundleId: id }}
      className="font-medium text-ink underline underline-offset-2"
    >
      {title} <span className="font-mono text-xs font-normal text-ink-3">{slug}</span>
    </Link>
  );
}

function Matrix({ matrix: m }: { matrix: TraceMatrix }) {
  const gaps = m.cells.flat().filter((c) => c.state === "gap").length;
  return (
    <div className="mt-3">
      <p className="text-sm">
        IDs of{" "}
        <Link
          to="/bundles/$bundleId"
          params={{ bundleId: m.upstream.id }}
          className="font-medium underline underline-offset-2"
        >
          {m.upstream.title}
        </Link>
        <span className={clsx("ml-2 text-xs", gaps ? "text-bad" : "text-ink-3")}>
          {gaps ? `${gaps} gap${gaps === 1 ? "" : "s"}` : "No gaps"}
        </span>
      </p>
      {m.rows.length === 0 ? (
        <p className="mt-1 text-sm text-ink-3">The upstream doc defines no trace IDs that the downstream docs cover.</p>
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
                    <Link to="/bundles/$bundleId" params={{ bundleId: c.id }} className="hover:underline">
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
                  {(m.cells[i] ?? []).map((c, j) => (
                    <Cell key={m.columns[j]?.id ?? j} cell={c} />
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function Cell({ cell: c }: { cell: TraceCell }) {
  const { icon: Icon, tone, text } = cellStyle[c.state];
  const detail =
    c.state === "referenced"
      ? `${c.refs.length} reference${c.refs.length === 1 ? "" : "s"}`
      : c.state === "covered_by"
        ? `${c.target ?? ""}: ${c.reason ?? ""}`
        : (c.reason ?? "");
  return (
    <td className={clsx("px-4 py-3 align-top", c.state === "gap" && "bg-bad/10")}>
      <span className={clsx("inline-flex items-center gap-1 text-xs font-medium", tone)}>
        <Icon aria-hidden className="size-3.5" />
        {text}
      </span>
      {detail ? <span className="mt-0.5 block text-xs text-ink-3">{detail}</span> : null}
    </td>
  );
}

function Suggestions({
  bundleId,
  baseVersion,
  items,
}: {
  bundleId: string;
  baseVersion: string;
  items: { id: string; text: string }[];
}) {
  const qc = useQueryClient();
  const [picked, setPicked] = useState<Set<string>>(() => new Set(items.map((s) => s.id)));
  const add = useMutation({
    ...addTraceIdsMutation(),
    onSuccess: () => qc.invalidateQueries(),
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
              path: { bundleId },
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
