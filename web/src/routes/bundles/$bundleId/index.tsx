import { createFileRoute } from "@tanstack/react-router";
import { BundlePage } from "@/features/bundle/bundle-page";
import { validateBundleSearch } from "@/features/bundle/search";

export const Route = createFileRoute("/bundles/$bundleId/")({
  validateSearch: validateBundleSearch,
  component: function Bundle() {
    const { bundleId } = Route.useParams();
    return <BundlePage bundleId={bundleId} search={Route.useSearch()} />;
  },
});
