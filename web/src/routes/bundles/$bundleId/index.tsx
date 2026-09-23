import { createFileRoute } from "@tanstack/react-router";
import { BundleEntry } from "@/features/bundle/bundle-entry";

export const Route = createFileRoute("/bundles/$bundleId/")({
  component: function Bundle() {
    const { bundleId } = Route.useParams();
    return <BundleEntry bundleId={bundleId} />;
  },
});
