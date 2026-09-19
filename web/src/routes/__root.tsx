import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext } from "@tanstack/react-router";
import { AppShell } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/states";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: AppShell,
  // A screen that fails to render shows what happened and a way on, not a blank page.
  errorComponent: ({ error }) => (
    <div className="mx-auto max-w-[720px] p-6">
      <ErrorState
        message={`Speccy could not show this page: ${error instanceof Error ? error.message : String(error)}. Reload the page. If it fails again, go back to the bundles.`}
        action={
          <Button size="sm" onClick={() => window.location.reload()}>
            Reload
          </Button>
        }
      />
    </div>
  ),
});
