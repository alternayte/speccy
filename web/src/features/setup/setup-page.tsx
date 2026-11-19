import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import { AuthCard } from "@/features/account/auth-card";
import { GitHubConnection } from "@/features/admin/github-section";
import { getSetupOptions, startSetupMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// SetupPage is step 1 of a new hosted Speccy (docs/specs/github-app.md): the setup token
// opens an invite for the first admin, who makes an account with email and password.
export function SetupPage() {
  const setup = useQuery(getSetupOptions());
  const [token, setToken] = useState("");
  const start = useMutation({
    ...startSetupMutation(),
    onSuccess: (res) => window.location.assign(res.invite_url),
  });
  if (setup.isPending) return <Loading label="Checking the setup" />;
  if (setup.isError) {
    return (
      <AuthCard title="Set up Speccy">
        <ErrorState message={problemMessage(setup.error)} />
      </AuthCard>
    );
  }
  if (!setup.data.open) {
    return (
      <AuthCard title="Setup is closed" lead="An admin exists, or SPECCY_SETUP_TOKEN is not set.">
        <Link to="/sign-in" className="text-sm underline">
          Sign in
        </Link>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Set up Speccy" lead="Step 1 of 2: make the first admin. Then connect GitHub, or skip that step.">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          start.mutate({ body: { token: token.trim() } });
        }}
      >
        <div>
          <Label htmlFor="setup-token">Setup token</Label>
          <Input
            id="setup-token"
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoFocus
          />
          <p className="mt-1 text-xs text-ink-3">The value of SPECCY_SETUP_TOKEN on the server.</p>
        </div>
        {start.isError ? <ErrorState message={problemMessage(start.error)} /> : null}
        <Button type="submit" variant="primary" className="w-full" disabled={!token.trim() || start.isPending}>
          {start.isPending ? "Checking" : "Next"}
        </Button>
      </form>
    </AuthCard>
  );
}

// SetupGitHubPage is step 2: connect GitHub with an App or a token, or skip it. Admin → GitHub
// shows the same controls later.
export function SetupGitHubPage() {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[960px] space-y-6 px-4 py-8 sm:px-6">
        <header>
          <h1 className="text-xl font-semibold tracking-tight">Connect GitHub</h1>
          <p className="mt-1 text-sm text-ink-2">
            Step 2 of 2. Speccy reads spec docs from GitHub repos and publishes edits as pull requests. You can skip
            this step and connect GitHub later in Admin.
          </p>
        </header>
        <GitHubConnection />
        <div className="flex justify-end">
          <Link to="/">
            <Button variant="primary">Done</Button>
          </Link>
        </div>
      </div>
    </div>
  );
}
