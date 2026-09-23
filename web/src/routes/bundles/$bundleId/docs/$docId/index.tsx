import { createFileRoute } from "@tanstack/react-router";
import { BundlePage } from "@/features/bundle/bundle-page";
import { validateBundleSearch } from "@/features/bundle/search";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/")({
  validateSearch: validateBundleSearch,
  component: function Bundle() {
    const { docId } = Route.useParams();
    return <BundlePage docId={docId} search={Route.useSearch()} />;
  },
});
