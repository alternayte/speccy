import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext } from "@tanstack/react-router";
import { AppShell } from "@/components/app-shell";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: AppShell,
});
