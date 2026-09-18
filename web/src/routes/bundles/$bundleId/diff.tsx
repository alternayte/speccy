import { createFileRoute } from "@tanstack/react-router";
import { DiffPage, validateDiffSearch } from "@/features/diff/diff-page";

export const Route = createFileRoute("/bundles/$bundleId/diff")({
  validateSearch: validateDiffSearch,
  component: function Diff() {
    const { bundleId } = Route.useParams();
    return <DiffPage bundleId={bundleId} search={Route.useSearch()} />;
  },
});
