import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import { authMessage, speccyAuth, tokenFromHash } from "@/lib/auth";
import { AuthCard } from "./auth-card";

// InvitePage makes an account from an invite link (REQ-081).
export function InvitePage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [token] = useState(tokenFromHash);
  const check = useQuery({
    queryKey: ["invite", token],
    queryFn: () => speccyAuth<{ role: string; expiresAt: string }>("invites/check", { token }),
    retry: false,
  });
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await speccyAuth("invites/accept", { token, name, email, password });
      history.replaceState(null, "", window.location.pathname); // drop the token from the address
      await qc.invalidateQueries();
      navigate({ to: "/" });
    } catch (err) {
      setError(authMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (check.isPending) return <Loading label="Checking the invite" />;
  if (check.isError) {
    return (
      <AuthCard title="This invite does not work">
        <ErrorState message={authMessage(check.error)} />
      </AuthCard>
    );
  }
  return (
    <AuthCard
      title="Join Speccy"
      lead={`You are invited as ${check.data.role === "admin" ? "an admin" : "a member"}. Make your account.`}
    >
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="name">Name</Label>
          <Input id="name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </div>
        <div>
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
          <p className="mt-1 text-xs text-ink-3">Speccy sends no mail. The email is your sign-in name.</p>
        </div>
        <div>
          <Label htmlFor="password">Password</Label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            minLength={12}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <p className="mt-1 text-xs text-ink-3">At least 12 characters.</p>
        </div>
        {error ? <ErrorState message={error} /> : null}
        <Button type="submit" variant="primary" className="w-full justify-center" disabled={busy}>
          {busy ? "Making your account" : "Make my account"}
        </Button>
      </form>
    </AuthCard>
  );
}
