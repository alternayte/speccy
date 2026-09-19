import { createFileRoute } from "@tanstack/react-router";
import { TourPage } from "@/features/tour/tour-page";

export const Route = createFileRoute("/bundles/$bundleId/tour")({
  component: function Tour() {
    const { bundleId } = Route.useParams();
    return <TourPage bundleId={bundleId} />;
  },
});
