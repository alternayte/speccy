import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { CircleCheck, CircleX, KeyRound, Pencil, Plus, Terminal, Trash2, Zap } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { Backend, BackendTest, Role } from "@/lib/api";
import {
  assignRoleMutation,
  deleteBackendMutation,
  getBudgetOptions,
  getBudgetQueryKey,
  listBackendsOptions,
  listBackendsQueryKey,
  listRolesOptions,
  listRolesQueryKey,
  setBudgetMutation,
  testBackendMutation,
  unassignRoleMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { BackendDialog, kinds } from "./backend-dialog";
import { MCPSection } from "./mcp-section";

const roleHelp: Record<string, string> = {
  reviewer: "Checks the rubric and facts, and writes build questions.",
  reader_1: "Answers build questions from the doc only.",
  reader_2: "A second, independent reader. Use another model family for diversity.",
  reader_3: "A third reader.",
  judge: "Groups reader answers by meaning.",
  writer: "Suggests fixes, summarises diffs, and answers threads.",
};

// AdminPage configures models (REQ-100 to REQ-104). Only admins see it (DEC-013).
export function AdminPage() {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[960px] space-y-10 px-4 py-8 sm:px-6">
        <header>
          <h1 className="text-xl font-semibold tracking-tight">Models</h1>
          <p className="mt-1 text-sm text-ink-2">
            Choose which model does each review job, and how many tokens a month the reviews may use.
          </p>
        </header>
        <BackendsSection />
        <RolesSection />
        <MCPSection />
        <BudgetSection />
      </div>
    </div>
  );
}

function Section({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-3 flex items-end justify-between gap-4">
        <h2 className="text-md font-semibold">{title}</h2>
        {action}
      </div>
      <div className="overflow-hidden rounded-lg border border-line bg-surface">{children}</div>
    </section>
  );
}

function BackendsSection() {
  const backends = useQuery(listBackendsOptions());
  const [dialog, setDialog] = useState<{ open: boolean; editing?: Backend }>({ open: false });
  return (
    <Section
      title="Backends"
      action={
        <Button
          variant="primary"
          size="sm"
          icon={<Plus className="size-3.5" />}
          onClick={() => setDialog({ open: true })}
        >
          Add backend
        </Button>
      }
    >
      {backends.isPending ? (
        <Loading label="Loading backends" />
      ) : backends.isError ? (
        <div className="p-3">
          <ErrorState message={problemMessage(backends.error)} />
        </div>
      ) : backends.data.items.length === 0 ? (
        <Empty title="No backend yet">
          Add an API key, or a CLI you already use on this machine, such as Claude Code. Reviews need at least one.
        </Empty>
      ) : (
        <ul className="divide-y divide-line">
          {backends.data.items.map((b) => (
            <BackendRow key={b.id} backend={b} onEdit={() => setDialog({ open: true, editing: b })} />
          ))}
        </ul>
      )}
      {dialog.open ? (
        <BackendDialog
          key={dialog.editing?.id ?? "new"}
          open
          onOpenChange={(open) => setDialog({ open })}
          editing={dialog.editing}
        />
      ) : null}
    </Section>
  );
}

function BackendRow({ backend: b, onEdit }: { backend: Backend; onEdit: () => void }) {
  const qc = useQueryClient();
  const [model, setModel] = useState("");
  const [result, setResult] = useState<BackendTest>();
  const test = useMutation({ ...testBackendMutation(), onSuccess: setResult });
  const del = useMutation({
    ...deleteBackendMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: listBackendsQueryKey() }),
  });
  const label = kinds.find((k) => k.kind === b.kind)?.label ?? b.kind;
  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {b.kind === "agent_cli" ? (
          <Terminal aria-hidden className="size-4 text-ink-3" />
        ) : (
          <KeyRound aria-hidden className="size-4 text-ink-3" />
        )}
        <span className="font-medium">{b.name}</span>
        <span className="text-xs text-ink-3">
          {label}
          {b.preset ? ` · ${b.preset}` : ""}
          {b.has_secret ? ` · key …${b.secret_last4}` : ""}
        </span>
        {b.roles.length ? <span className="text-xs text-ink-2">Used by {b.roles.join(", ")}</span> : null}
        <div className="ml-auto flex gap-1">
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Edit ${b.name}`}
            icon={<Pencil className="size-3.5" />}
            onClick={onEdit}
          />
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Delete ${b.name}`}
            icon={<Trash2 className="size-3.5" />}
            onClick={() => window.confirm(`Delete ${b.name}?`) && del.mutate({ path: { backendId: b.id } })}
          />
        </div>
      </div>
      <form
        className="mt-2 flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setResult(undefined);
          test.mutate({ path: { backendId: b.id }, body: { model } });
        }}
      >
        <Input
          aria-label={`Model to test on ${b.name}`}
          className="max-w-64"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder="Model to test"
        />
        <Button type="submit" size="sm" icon={<Zap className="size-3.5" />} disabled={!model.trim() || test.isPending}>
          {test.isPending ? "Testing" : "Test"}
        </Button>
        {result ? (
          <span
            role="status"
            className={clsx("inline-flex items-center gap-1.5 text-xs", result.ok ? "text-ok" : "text-bad")}
          >
            {result.ok ? (
              <CircleCheck aria-hidden className="size-3.5" />
            ) : (
              <CircleX aria-hidden className="size-3.5" />
            )}
            {result.ok
              ? `Works: ${(result.duration_ms / 1000).toFixed(1)} s, ${(result.tokens_in ?? 0) + (result.tokens_out ?? 0)} tokens${result.estimated ? " (estimated)" : ""}`
              : result.error}
          </span>
        ) : null}
      </form>
      {del.isError ? <ErrorState message={problemMessage(del.error)} /> : null}
    </li>
  );
}

function RolesSection() {
  const roles = useQuery(listRolesOptions());
  const backends = useQuery(listBackendsOptions());
  return (
    <Section title="Roles">
      {roles.isPending || backends.isPending ? (
        <Loading label="Loading roles" />
      ) : roles.isError ? (
        <div className="p-3">
          <ErrorState message={problemMessage(roles.error)} />
        </div>
      ) : (
        <ul className="divide-y divide-line">
          {roles.data.items.map((r) => (
            <RoleRow key={r.role} role={r} backends={backends.data?.items ?? []} />
          ))}
        </ul>
      )}
    </Section>
  );
}

function RoleRow({ role: r, backends }: { role: Role; backends: Backend[] }) {
  const qc = useQueryClient();
  const [backend, setBackend] = useState(r.backend_id ?? "");
  const [model, setModel] = useState(r.model ?? "");
  const [priceIn, setPriceIn] = useState(r.price_in_per_mtok ? String(r.price_in_per_mtok) : "");
  const [priceOut, setPriceOut] = useState(r.price_out_per_mtok ? String(r.price_out_per_mtok) : "");
  const refresh = () => {
    qc.invalidateQueries({ queryKey: listRolesQueryKey() });
    qc.invalidateQueries({ queryKey: listBackendsQueryKey() });
  };
  const assign = useMutation({ ...assignRoleMutation(), onSuccess: refresh });
  const clear = useMutation({ ...unassignRoleMutation(), onSuccess: refresh });
  const dirty =
    backend !== (r.backend_id ?? "") ||
    model !== (r.model ?? "") ||
    priceIn !== (r.price_in_per_mtok ? String(r.price_in_per_mtok) : "") ||
    priceOut !== (r.price_out_per_mtok ? String(r.price_out_per_mtok) : "");
  return (
    <li className="grid grid-cols-1 gap-2 px-4 py-3 md:grid-cols-[10rem_1fr] md:items-center">
      <div>
        <p className="font-mono text-sm font-medium">{r.role}</p>
        <p className="text-xs text-ink-3">{roleHelp[r.role]}</p>
      </div>
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          assign.mutate({
            path: { role: r.role },
            body: {
              backend_id: backend,
              model,
              ...(priceIn ? { price_in_per_mtok: Number(priceIn) } : {}),
              ...(priceOut ? { price_out_per_mtok: Number(priceOut) } : {}),
            },
          });
        }}
      >
        <select
          aria-label={`Backend for ${r.role}`}
          value={backend}
          onChange={(e) => setBackend(e.target.value)}
          className="h-8 rounded-md border border-line-strong bg-surface px-2 text-sm text-ink"
        >
          <option value="">Not assigned</option>
          {backends.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </select>
        <Input
          aria-label={`Model for ${r.role}`}
          className="w-52"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder="Model"
        />
        <Input
          aria-label={`Input price per million tokens for ${r.role}`}
          className="w-24"
          inputMode="decimal"
          value={priceIn}
          onChange={(e) => setPriceIn(e.target.value)}
          placeholder="$ in / M"
          title="Price per million input tokens, for cost estimates"
        />
        <Input
          aria-label={`Output price per million tokens for ${r.role}`}
          className="w-24"
          inputMode="decimal"
          value={priceOut}
          onChange={(e) => setPriceOut(e.target.value)}
          placeholder="$ out / M"
          title="Price per million output tokens, for cost estimates"
        />
        <Button
          type="submit"
          size="sm"
          variant={dirty ? "primary" : "secondary"}
          disabled={!dirty || !backend || !model.trim()}
        >
          Save
        </Button>
        {r.backend_id ? (
          <Button size="sm" variant="ghost" onClick={() => clear.mutate({ path: { role: r.role } })}>
            Clear
          </Button>
        ) : null}
      </form>
      {assign.isError || clear.isError ? (
        <div className="md:col-start-2">
          <ErrorState message={problemMessage(assign.error ?? clear.error)} />
        </div>
      ) : null}
    </li>
  );
}

function BudgetSection() {
  const qc = useQueryClient();
  const budget = useQuery(getBudgetOptions());
  const [limit, setLimit] = useState<string | null>(null);
  const save = useMutation({
    ...setBudgetMutation(),
    onSuccess: () => {
      setLimit(null);
      qc.invalidateQueries({ queryKey: getBudgetQueryKey() });
    },
  });
  if (budget.isPending) return <Loading label="Loading the budget" />;
  if (budget.isError) return <ErrorState message={problemMessage(budget.error)} />;
  const b = budget.data;
  const shown = limit ?? (b.token_limit != null ? String(b.token_limit) : "");
  const pct = b.token_limit ? Math.min(100, Math.round((100 * b.tokens_used) / b.token_limit)) : 0;
  return (
    <Section title="Monthly token budget">
      <div className="space-y-3 px-4 py-4">
        <p className="text-sm text-ink-2">
          {b.token_limit != null
            ? `${b.tokens_used.toLocaleString()} of ${b.token_limit.toLocaleString()} tokens used in ${b.month}. When the budget is spent, AI stages stop; lint still runs.`
            : `${b.tokens_used.toLocaleString()} tokens used in ${b.month}. There is no limit.`}
        </p>
        {b.token_limit != null ? (
          <div
            className="h-1.5 overflow-hidden rounded-full bg-sunken"
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            <div
              className={clsx(
                "h-full rounded-full transition-[width] duration-500",
                pct >= 90 ? "bg-bad" : "bg-accent",
              )}
              style={{ width: `${pct}%` }}
            />
          </div>
        ) : null}
        <form
          className="flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate({ body: shown.trim() ? { token_limit: Number(shown) } : {} });
          }}
        >
          <Input
            aria-label="Monthly token limit"
            className="w-48"
            inputMode="numeric"
            value={shown}
            onChange={(e) => setLimit(e.target.value.replace(/[^0-9]/g, ""))}
            placeholder="No limit"
          />
          <Button type="submit" size="sm" disabled={limit === null || save.isPending}>
            Save
          </Button>
        </form>
        {save.isError ? <ErrorState message={problemMessage(save.error)} /> : null}
      </div>
    </Section>
  );
}
