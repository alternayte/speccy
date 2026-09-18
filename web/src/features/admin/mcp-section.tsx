import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { Plug, Plus, Search, Trash2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import type { McpConnection, McpConnectionInput } from "@/lib/api";
import {
  createMcpConnectionMutation,
  deleteMcpConnectionMutation,
  listMcpConnectionsOptions,
  listMcpConnectionsQueryKey,
  listMcpToolsOptions,
  updateMcpConnectionMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// MCPSection manages MCP connections (REQ-112). Only allowlisted read-only tools reach the AI;
// a connection marked search checks facts when the model has no web search (REQ-034).
export function MCPSection() {
  const conns = useQuery(listMcpConnectionsOptions());
  const [adding, setAdding] = useState(false);
  return (
    <section>
      <div className="mb-3 flex items-end justify-between gap-4">
        <div>
          <h2 className="text-md font-semibold">MCP connections</h2>
          <p className="mt-0.5 text-xs text-ink-2">
            Sources the review can read. Mark one as search to check facts when the model has no web search.
          </p>
        </div>
        <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setAdding(true)}>
          Add connection
        </Button>
      </div>
      <div className="overflow-hidden rounded-lg border border-line bg-surface">
        {conns.isPending ? (
          <Loading label="Loading connections" />
        ) : conns.isError ? (
          <div className="p-3">
            <ErrorState message={problemMessage(conns.error)} />
          </div>
        ) : conns.data.items.length === 0 ? (
          <Empty title="No MCP connection">Add one to give the review a search source or internal docs.</Empty>
        ) : (
          <ul className="divide-y divide-line">
            {conns.data.items.map((c) => (
              <ConnectionRow key={c.id} conn={c} />
            ))}
          </ul>
        )}
      </div>
      {adding ? <AddConnection onClose={() => setAdding(false)} /> : null}
    </section>
  );
}

function inputOf(c: McpConnection): McpConnectionInput {
  return {
    name: c.name,
    transport: c.transport as "stdio" | "http",
    ...(c.command ? { command: c.command } : {}),
    ...(c.url ? { url: c.url } : {}),
    ...(c.secret_env ? { secret_env: c.secret_env } : {}),
    tool_allowlist: c.tool_allowlist,
    is_search: c.is_search,
    ...(c.search_tool ? { search_tool: c.search_tool } : {}),
  };
}

function ConnectionRow({ conn: c }: { conn: McpConnection }) {
  const qc = useQueryClient();
  const [choosing, setChoosing] = useState(false);
  const tools = useQuery({ ...listMcpToolsOptions({ path: { connectionId: c.id } }), enabled: choosing, retry: false });
  const [allow, setAllow] = useState<string[]>(c.tool_allowlist);
  const [search, setSearch] = useState(c.search_tool);
  const refresh = () => qc.invalidateQueries({ queryKey: listMcpConnectionsQueryKey() });
  const save = useMutation({
    ...updateMcpConnectionMutation(),
    onSuccess: () => {
      setChoosing(false);
      refresh();
    },
  });
  const del = useMutation({ ...deleteMcpConnectionMutation(), onSuccess: refresh });
  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Plug aria-hidden className="size-4 text-ink-3" />
        <span className="font-medium">{c.name}</span>
        <span className="truncate text-xs text-ink-3">
          {c.transport} · {c.url ?? c.command?.join(" ")}
          {c.has_secret ? ` · secret …${c.secret_last4}` : ""}
        </span>
        {c.is_search ? (
          <span className="inline-flex items-center gap-1 text-xs text-accent">
            <Search aria-hidden className="size-3" /> search: {c.search_tool}
          </span>
        ) : null}
        <div className="ml-auto flex gap-1">
          <Button size="sm" onClick={() => setChoosing((v) => !v)}>
            {choosing ? "Close" : `Tools (${c.tool_allowlist.length} allowed)`}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Delete ${c.name}`}
            icon={<Trash2 className="size-3.5" />}
            onClick={() => window.confirm(`Delete ${c.name}?`) && del.mutate({ path: { connectionId: c.id } })}
          />
        </div>
      </div>
      {choosing ? (
        <div className="mt-3 rounded-md border border-line p-3">
          {tools.isPending ? (
            <Loading label={`Connecting to ${c.name}`} />
          ) : tools.isError ? (
            <ErrorState message={problemMessage(tools.error)} />
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const body = {
                  ...inputOf(c),
                  tool_allowlist: allow,
                  is_search: !!search,
                  ...(search ? { search_tool: search } : {}),
                };
                if (!search) delete body.search_tool;
                save.mutate({ path: { connectionId: c.id }, body });
              }}
              className="space-y-2"
            >
              <p className="text-xs text-ink-2">
                Allow read-only tools only. Speccy refuses a tool that the server marks as one that changes data.
              </p>
              <ul className="space-y-1">
                {tools.data.items.map((t) => (
                  <li key={t.name} className={clsx("flex items-start gap-3 text-sm", t.destructive && "opacity-50")}>
                    <label className="flex min-w-0 flex-1 items-start gap-2">
                      <input
                        type="checkbox"
                        disabled={t.destructive}
                        checked={allow.includes(t.name)}
                        onChange={(e) => {
                          setAllow((a) => (e.target.checked ? [...a, t.name] : a.filter((x) => x !== t.name)));
                          if (!e.target.checked && search === t.name) setSearch("");
                        }}
                        className="mt-1 accent-[var(--accent)]"
                      />
                      <span className="min-w-0">
                        <span className="font-mono text-xs">{t.name}</span>
                        <span className="ml-2 text-2xs text-ink-3">
                          {t.destructive ? "changes data" : t.read_only ? "read-only" : "not marked read-only"}
                        </span>
                        <span className="block truncate text-xs text-ink-2">{t.description}</span>
                      </span>
                    </label>
                    <label className="flex shrink-0 items-center gap-1 text-xs text-ink-2">
                      <input
                        type="radio"
                        name={`search-${c.id}`}
                        disabled={!allow.includes(t.name)}
                        checked={search === t.name}
                        onChange={() => setSearch(t.name)}
                        className="accent-[var(--accent)]"
                      />
                      search
                    </label>
                  </li>
                ))}
              </ul>
              {save.isError ? <ErrorState message={problemMessage(save.error)} /> : null}
              <div className="flex justify-end gap-2">
                {search ? (
                  <Button size="sm" variant="ghost" onClick={() => setSearch("")}>
                    Not a search source
                  </Button>
                ) : null}
                <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
                  Save tools
                </Button>
              </div>
            </form>
          )}
        </div>
      ) : null}
      {del.isError ? <ErrorState message={problemMessage(del.error)} /> : null}
    </li>
  );
}

function AddConnection({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [transport, setTransport] = useState<"http" | "stdio">("http");
  const [url, setUrl] = useState("");
  const [command, setCommand] = useState("");
  const [secret, setSecret] = useState("");
  const [secretEnv, setSecretEnv] = useState("");
  const create = useMutation({
    ...createMcpConnectionMutation(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: listMcpConnectionsQueryKey() });
      onClose();
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title="Add an MCP connection"
      description="Then choose which of its tools the review may use."
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate({
            body: {
              name: name.trim(),
              transport,
              ...(transport === "http"
                ? { url: url.trim() }
                : { command: command.split("\n").filter((l) => l.trim() !== "") }),
              ...(secret.trim() ? { secret: secret.trim() } : {}),
              ...(secretEnv.trim() ? { secret_env: secretEnv.trim() } : {}),
              tool_allowlist: [],
              is_search: false,
            },
          });
        }}
      >
        <div>
          <Label htmlFor="mcp-name">Name</Label>
          <Input id="mcp-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Web search" />
        </div>
        <div className="flex gap-4 text-sm">
          {(["http", "stdio"] as const).map((t) => (
            <label key={t} className="flex items-center gap-1.5">
              <input
                type="radio"
                name="transport"
                checked={transport === t}
                onChange={() => setTransport(t)}
                className="accent-[var(--accent)]"
              />
              {t === "http" ? "HTTP URL" : "Local command (stdio)"}
            </label>
          ))}
        </div>
        {transport === "http" ? (
          <div>
            <Label htmlFor="mcp-url">URL</Label>
            <Input
              id="mcp-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://mcp.example.com/mcp"
            />
          </div>
        ) : (
          <div>
            <Label htmlFor="mcp-cmd">Command, one argument per line</Label>
            <Textarea
              id="mcp-cmd"
              rows={3}
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder={"npx\n-y\nsome-mcp-server"}
            />
          </div>
        )}
        <div>
          <Label htmlFor="mcp-secret">Secret (optional)</Label>
          <Input
            id="mcp-secret"
            type="password"
            autoComplete="off"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
          />
          {transport === "stdio" ? (
            <div className="mt-2">
              <Label htmlFor="mcp-env">Pass the secret as the environment variable</Label>
              <Input
                id="mcp-env"
                value={secretEnv}
                onChange={(e) => setSecretEnv(e.target.value)}
                placeholder="API_KEY"
              />
            </div>
          ) : (
            <p className="mt-1 text-xs text-ink-3">Sent as a bearer token. Speccy encrypts it.</p>
          )}
        </div>
        {create.isError ? <ErrorState message={problemMessage(create.error)} /> : null}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={!name.trim() || create.isPending}>
            Add
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
