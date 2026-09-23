import { createFileRoute } from "@tanstack/react-router";
import { TourPage } from "@/features/tour/tour-page";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/tour")({
  component: function Tour() {
    const { docId } = Route.useParams();
    return <TourPage docId={docId} />;
  },
});
