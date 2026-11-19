import { Link2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/states";
import { authMessage, linkGitHub } from "@/lib/auth";
import { problemCode, problemMessage } from "@/lib/problem";

// needsGitHubLink says whether a write failed because the person has not connected GitHub:
// with a GitHub App, a doc change goes to GitHub as its author, never as the bot.
export function needsGitHubLink(err: unknown): boolean {
  return problemCode(err) === "github_link_required";
}

// WriteError shows the error of a GitHub write. When the person must connect GitHub first, it
// offers the connect button, which comes back to `back` (a path on this site).
export function WriteError({ error, back }: { error: unknown; back: string }) {
  const [busy, setBusy] = useState(false);
  const [linkError, setLinkError] = useState<string>();
  if (!needsGitHubLink(error)) return <ErrorState message={problemMessage(error)} />;
  return (
    <div className="space-y-2 rounded-md border border-line bg-sunken p-3 text-sm">
      <p>Connect your GitHub account first. The pull request goes to GitHub with you as its author.</p>
      <Button
        size="sm"
        variant="primary"
        icon={<Link2 className="size-3.5" />}
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await linkGitHub(back);
          } catch (err) {
            setLinkError(authMessage(err));
            setBusy(false);
          }
        }}
      >
        {busy ? "Opening GitHub" : "Connect GitHub"}
      </Button>
      {linkError ? <ErrorState message={linkError} /> : null}
    </div>
  );
}
