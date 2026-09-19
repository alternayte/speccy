import { createFileRoute } from "@tanstack/react-router";
import { TracePage } from "@/features/trace/trace-page";

export const Route = createFileRoute("/bundles/$bundleId/trace")({
  component: function Trace() {
    const { bundleId } = Route.useParams();
    return <TracePage bundleId={bundleId} />;
  },
});
