import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import { auth, authMessage, providerLabel } from "@/lib/auth";
import { AuthCard } from "./auth-card";
import { useMeta } from "./me";

// SignInPage signs a person in with email and password, or a linked provider (REQ-080).
export function SignInPage({ redirect }: { redirect?: string }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const meta = useMeta();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [mfaToken, setMfaToken] = useState<string>();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const target = redirect?.startsWith("/") && !redirect.startsWith("//") ? redirect : "/";

  const done = async () => {
    await qc.invalidateQueries();
    navigate({ to: target });
  };
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      if (mfaToken) {
        await auth.totp.verify({ code, mfaToken });
        await done();
        return;
      }
      const res = await auth.signIn.email({ email, password });
      if (res.mfaRequired && res.mfaToken) {
        setMfaToken(res.mfaToken);
        return;
      }
      await done();
    } catch (err) {
      setError(authMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const providers = meta.data?.sign_in_providers ?? [];
  return (
    <AuthCard title="Sign in to Speccy" lead="Accounts come from an invite link. Ask an admin for one.">
      <form onSubmit={submit} className="space-y-4">
        {mfaToken ? (
          <div>
            <Label htmlFor="code">Code from your authenticator app</Label>
            <Input
              id="code"
              inputMode="numeric"
              autoComplete="one-time-code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoFocus
            />
          </div>
        ) : (
          <>
            <div>
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                autoComplete="username"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                autoFocus
              />
            </div>
            <div>
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
          </>
        )}
        {error ? <ErrorState message={error} /> : null}
        <Button type="submit" variant="primary" className="w-full justify-center" disabled={busy}>
          {busy ? "Signing in" : "Sign in"}
        </Button>
      </form>
      {providers.length > 0 && !mfaToken ? (
        <div className="mt-6 space-y-2 border-t border-line pt-6">
          {providers.map((p) => (
            <a
              key={p}
              href={auth.oauth.authorize(p, { redirect_to: window.location.origin + target })}
              className="flex h-8 w-full items-center justify-center rounded-md border border-line-strong bg-surface text-sm font-medium text-ink hover:bg-sunken"
            >
              Sign in with {providerLabel[p] ?? p}
            </a>
          ))}
          <p className="text-xs text-ink-3">A provider signs you in after you link it to your account under Account.</p>
        </div>
      ) : null}
    </AuthCard>
  );
}
