import { createFileRoute } from "@tanstack/react-router";
import { DiffPage, validateDiffSearch } from "@/features/diff/diff-page";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/diff")({
  validateSearch: validateDiffSearch,
  component: function Diff() {
    const { docId } = Route.useParams();
    return <DiffPage docId={docId} search={Route.useSearch()} />;
  },
});
