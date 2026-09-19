import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import { authMessage, speccyAuth, tokenFromHash } from "@/lib/auth";
import { AuthCard } from "./auth-card";

// ResetPage sets a new password from an admin's reset link (REQ-082).
export function ResetPage() {
  const [token] = useState(tokenFromHash);
  const check = useQuery({
    queryKey: ["reset", token],
    queryFn: () => speccyAuth<{ email: string }>("reset/check", { token }),
    retry: false,
  });
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await speccyAuth("reset", { token, password });
      history.replaceState(null, "", window.location.pathname);
      setDone(true);
    } catch (err) {
      setError(authMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (done) {
    return (
      <AuthCard title="Password changed" lead="Speccy signed you out everywhere. Sign in with the new password.">
        <Link to="/sign-in" className="text-sm font-medium text-ink underline underline-offset-2">
          Sign in
        </Link>
      </AuthCard>
    );
  }
  if (check.isPending) return <Loading label="Checking the link" />;
  if (check.isError) {
    return (
      <AuthCard title="This link does not work">
        <ErrorState message={authMessage(check.error)} />
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Set a new password" lead={`For ${check.data.email}.`}>
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="password">New password</Label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            minLength={12}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoFocus
          />
          <p className="mt-1 text-xs text-ink-3">At least 12 characters.</p>
        </div>
        {error ? <ErrorState message={error} /> : null}
        <Button type="submit" variant="primary" className="w-full justify-center" disabled={busy}>
          {busy ? "Saving" : "Set the password"}
        </Button>
      </form>
    </AuthCard>
  );
}
