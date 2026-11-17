import { createFileRoute } from "@tanstack/react-router";
import { BreakdownPage } from "@/features/breakdown/breakdown-page";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/breakdown")({
  validateSearch: (s: Record<string, unknown>): { item?: string } =>
    typeof s.item === "string" && s.item ? { item: s.item } : {},
  component: function Breakdown() {
    const { docId } = Route.useParams();
    return <BreakdownPage docId={docId} focusItem={Route.useSearch().item} />;
  },
});
