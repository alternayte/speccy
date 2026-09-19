import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, Link2, Trash2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { auth, authMessage, providerLabel } from "@/lib/auth";
import { useMe, useMeta } from "./me";

// AccountPage: the password, personal API tokens (REQ-110), and linked sign-in providers
// (REQ-080) of the signed-in user.
export function AccountPage() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[720px] space-y-10 px-4 py-8 sm:px-6">
        <header>
          <h1 className="text-xl font-semibold tracking-tight">Account</h1>
          <p className="mt-1 text-sm text-ink-2">
            {me.data?.email} · {me.data?.role === "admin" ? "Admin" : "Member"}
          </p>
        </header>
        <PasswordSection />
        <TokensSection />
        <ProvidersSection />
      </div>
    </div>
  );
}

function PasswordSection() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const change = useMutation({
    mutationFn: () => auth.password.change({ currentPassword: current, newPassword: next, revokeOtherSessions: true }),
    onSuccess: () => {
      setCurrent("");
      setNext("");
    },
  });
  return (
    <section>
      <h2 className="text-md font-semibold">Password</h2>
      <form
        className="mt-3 grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          change.mutate();
        }}
      >
        <div>
          <Label htmlFor="current">Current password</Label>
          <Input
            id="current"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </div>
        <div>
          <Label htmlFor="next">New password</Label>
          <Input
            id="next"
            type="password"
            autoComplete="new-password"
            minLength={12}
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
          />
        </div>
        <Button type="submit" disabled={change.isPending}>
          Change
        </Button>
      </form>
      {change.isError ? (
        <div className="mt-2">
          <ErrorState message={authMessage(change.error)} />
        </div>
      ) : change.isSuccess ? (
        <p className="mt-2 text-sm text-ok">Password changed. Your other sessions are signed out.</p>
      ) : null}
    </section>
  );
}

function TokensSection() {
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["api-keys"], queryFn: () => auth.apiKeys.listKeys() });
  const [name, setName] = useState("");
  const [plain, setPlain] = useState<string>();
  const create = useMutation({
    mutationFn: () => auth.apiKeys.createKey({ name: name.trim() }),
    onSuccess: (res) => {
      setPlain(res.plaintext);
      setName("");
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => auth.apiKeys.revokeKey(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["api-keys"] }),
  });
  const live = keys.data?.keys.filter((k) => !k.revokedAt) ?? [];
  return (
    <section>
      <h2 className="text-md font-semibold">API tokens</h2>
      <p className="mt-1 text-sm text-ink-2">
        The CLI, CI, and MCP clients use a token as <span className="font-mono text-xs">Authorization: Bearer</span>. A
        token has your role.
      </p>
      <form
        className="mt-3 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <Input
          aria-label="Token name"
          placeholder="Name, such as CI"
          maxLength={100}
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
          className="max-w-[280px]"
        />
        <Button type="submit" icon={<KeyRound className="size-3.5" />} disabled={create.isPending || !name.trim()}>
          Make a token
        </Button>
      </form>
      {create.isError ? (
        <div className="mt-2">
          <ErrorState message={authMessage(create.error)} />
        </div>
      ) : null}
      {plain ? <OneTimeValue label="Copy the token now. Speccy shows it one time." value={plain} /> : null}
      <div className="mt-3">
        {keys.isPending ? (
          <Loading label="Loading tokens" />
        ) : keys.isError ? (
          <ErrorState message={authMessage(keys.error)} />
        ) : live.length === 0 ? (
          <Empty title="No API tokens" />
        ) : (
          <ul className="divide-y divide-line rounded-md border border-line">
            {live.map((k) => (
              <li key={k.id} className="flex items-center gap-3 px-3 py-2 text-sm">
                <span className="font-medium">{k.name}</span>
                <span className="font-mono text-xs text-ink-3">{k.start}…</span>
                <span className="text-xs text-ink-3">
                  {k.lastUsedAt ? `used ${new Date(k.lastUsedAt).toLocaleDateString()}` : "never used"}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="ml-auto"
                  aria-label={`Revoke ${k.name}`}
                  icon={<Trash2 className="size-3.5" />}
                  onClick={() => revoke.mutate(k.id)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function ProvidersSection() {
  const meta = useMeta();
  const configured = meta.data?.sign_in_providers ?? [];
  const linked = useQuery({
    queryKey: ["providers"],
    queryFn: () => auth.account.providers(),
    enabled: configured.length > 0,
  });
  const link = useMutation({
    mutationFn: (p: string) => auth.account.link(p),
    onSuccess: (res) => window.location.assign(res.url),
  });
  if (configured.length === 0) return null;
  const has = new Set((linked.data?.providers ?? []).map((p) => p.provider));
  return (
    <section>
      <h2 className="text-md font-semibold">Sign-in providers</h2>
      <p className="mt-1 text-sm text-ink-2">Link a provider to sign in with it next time.</p>
      <ul className="mt-3 space-y-2">
        {configured.map((p) => (
          <li key={p} className="flex items-center gap-3 text-sm">
            <span className="w-32">{providerLabel[p] ?? p}</span>
            {has.has(p) ? (
              <span className="text-ok">Linked</span>
            ) : (
              <Button
                size="sm"
                icon={<Link2 className="size-3.5" />}
                onClick={() => link.mutate(p)}
                disabled={link.isPending}
              >
                Link
              </Button>
            )}
          </li>
        ))}
      </ul>
      {link.isError ? (
        <div className="mt-2">
          <ErrorState message={authMessage(link.error)} />
        </div>
      ) : null}
    </section>
  );
}

// OneTimeValue shows a secret or a link that the server returns one time, with a copy button.
export function OneTimeValue({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-3 rounded-md border border-accent/40 bg-accent-soft p-3">
      <p className="text-xs text-ink-2">{label}</p>
      <div className="mt-1.5 flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate font-mono text-xs">{value}</code>
        <Button
          size="sm"
          icon={<Copy className="size-3.5" />}
          onClick={async () => {
            await navigator.clipboard?.writeText(value);
            setCopied(true);
          }}
        >
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
    </div>
  );
}
