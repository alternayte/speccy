import { createFileRoute } from "@tanstack/react-router";
import { BreakdownEntry } from "@/features/bundle/bundle-entry";

// The work item URL in a pushed issue knows only the spec doc. It lands here and moves to the
// breakdown screen in the doc's bundle.
export const Route = createFileRoute("/docs/$docId_/breakdown")({
  validateSearch: (s: Record<string, unknown>): { item?: string } =>
    typeof s.item === "string" && s.item ? { item: s.item } : {},
  component: function DocBreakdown() {
    const { docId } = Route.useParams();
    return <BreakdownEntry docId={docId} item={Route.useSearch().item} />;
  },
});
