import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookText, Trash2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { UrlConnector, UrlConnectorInput } from "@/lib/api";
import {
  createUrlConnectorMutation,
  deleteUrlConnectorMutation,
  listProfilesOptions,
  listUrlConnectorsQueryKey,
  updateUrlConnectorMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// URLConnectorRow is one URL connector in the Connectors list: its address, its base URL, its
// header and the profiles it serves (docs/specs/url-connectors.md).
export function URLConnectorRow({ conn: c }: { conn: UrlConnector }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const del = useMutation({
    ...deleteUrlConnectorMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: listUrlConnectorsQueryKey() }),
  });
  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <BookText aria-hidden className="size-4 text-ink-3" />
        <span className="font-medium">{c.name}</span>
        <span className="text-xs text-ink-2">{c.kind === "github" ? "URL · GitHub repo" : "URL"}</span>
        <span className="min-w-0 truncate font-mono text-xs text-ink-3" title={c.address}>
          {c.address}
        </span>
        <span className="text-xs text-ink-2">
          {c.profiles.length ? `for ${c.profiles.join(", ")}` : "for every doc"}
          {c.has_header ? ` · ${c.header_name} …${c.header_last4}` : ""}
        </span>
        <div className="ml-auto flex gap-1">
          <Button size="sm" onClick={() => setEditing(true)}>
            Edit
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
      {del.isError ? <ErrorState message={problemMessage(del.error)} /> : null}
      {editing ? <URLConnectorDialog conn={c} onClose={() => setEditing(false)} /> : null}
    </li>
  );
}

// URLConnectorDialog adds a URL connector, or changes one. Speccy reads the index before it
// saves, and says how many links it can follow.
export function URLConnectorDialog({ conn, onClose }: { conn?: UrlConnector; onClose: () => void }) {
  const qc = useQueryClient();
  const profiles = useQuery(listProfilesOptions());
  const [name, setName] = useState(conn?.name ?? "");
  const [address, setAddress] = useState(conn?.address ?? "");
  const [base, setBase] = useState(conn?.base_url ?? "");
  const [header, setHeader] = useState(conn?.header_name ?? "");
  const [value, setValue] = useState("");
  const [only, setOnly] = useState<string[]>(conn?.profiles ?? []);
  const github = /^https?:\/\/[^/]*github[^/]*\/[^/]+\/[^/]+\/blob\//.test(address.trim());
  const done = () => qc.invalidateQueries({ queryKey: listUrlConnectorsQueryKey() });
  const create = useMutation({ ...createUrlConnectorMutation(), onSuccess: done });
  const update = useMutation({ ...updateUrlConnectorMutation(), onSuccess: done });
  const saved = create.data ?? update.data;
  const error = create.error ?? update.error;
  const pending = create.isPending || update.isPending;
  const body = (): UrlConnectorInput => ({
    name,
    address,
    ...(base.trim() ? { base_url: base.trim() } : {}),
    ...(header.trim() && !github ? { header_name: header.trim() } : {}),
    ...(value.trim() && !github ? { header_value: value.trim() } : {}),
    profiles: only,
  });
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={conn ? `Edit ${conn.name}` : "Add a URL connector"}
      description="A doc index, such as an llms.txt. A review reads the index, picks the pages that fit the claims, and checks the claims against them. Speccy reads it with no MCP server."
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (conn) update.mutate({ path: { connectionId: conn.id }, body: body() });
          else create.mutate({ body: body() });
        }}
      >
        <div>
          <Label htmlFor="uc-name">Name</Label>
          <Input id="uc-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Platform docs" />
        </div>
        <div>
          <Label htmlFor="uc-address">Index address</Label>
          <Input
            id="uc-address"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
            placeholder="https://docs.acme.dev/llms.txt"
          />
          <p className="mt-1 text-xs text-ink-3">
            A web address, or a GitHub URL of a file at a branch, such as{" "}
            <code>https://github.com/acme/platform/blob/gh-pages/llms.txt</code>. Speccy reads a GitHub address with its
            GitHub credential, so a private GitHub Pages site works through its repo.
          </p>
        </div>
        <div>
          <Label htmlFor="uc-base">Base URL (optional)</Label>
          <Input
            id="uc-base"
            value={base}
            onChange={(e) => setBase(e.target.value)}
            placeholder={github ? "https://acme.github.io/platform/" : "The folder of the index"}
          />
          <p className="mt-1 text-xs text-ink-3">
            {github
              ? "The address of the site the branch builds. A link under it maps to the file at the same path in the branch."
              : "Speccy fetches only links under it, and reads external links under it."}
          </p>
        </div>
        {!github ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <Label htmlFor="uc-header">Header (optional)</Label>
              <Input
                id="uc-header"
                value={header}
                onChange={(e) => setHeader(e.target.value)}
                placeholder="Authorization"
              />
            </div>
            <div>
              <Label htmlFor="uc-value">Header value</Label>
              <Input
                id="uc-value"
                type="password"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={conn?.has_header ? `Stored, ends in ${conn.header_last4}` : "Bearer …"}
              />
            </div>
            <p className="text-xs text-ink-3 sm:col-span-2">
              Speccy seals the value and sends it only to the base URL's host.
            </p>
          </div>
        ) : null}
        <fieldset>
          <legend className="text-sm font-medium text-ink">Doc types</legend>
          <p className="text-xs text-ink-3">With none ticked, the connector serves every doc.</p>
          <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1">
            {(profiles.data?.items ?? []).map((p) => (
              <label key={p.key} className="flex items-center gap-1.5 text-sm">
                <input
                  type="checkbox"
                  checked={only.includes(p.key)}
                  onChange={(e) => setOnly(e.target.checked ? [...only, p.key] : only.filter((k) => k !== p.key))}
                />
                {p.name}
              </label>
            ))}
          </div>
        </fieldset>
        {error ? <ErrorState message={problemMessage(error)} /> : null}
        {saved ? (
          <p role="status" className="rounded-md border border-line bg-sunken px-3 py-2 text-sm text-ink">
            Saved. Speccy read the index and can follow {saved.links ?? 0} link{saved.links === 1 ? "" : "s"} under the
            base URL.
          </p>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>{saved ? "Close" : "Cancel"}</Button>
          {!saved ? (
            <Button type="submit" variant="primary" disabled={pending || !name.trim() || !address.trim()}>
              {pending ? "Reading the index" : conn ? "Save" : "Add"}
            </Button>
          ) : null}
        </div>
      </form>
    </Dialog>
  );
}
