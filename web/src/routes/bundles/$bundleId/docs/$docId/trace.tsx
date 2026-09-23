import { createFileRoute } from "@tanstack/react-router";
import { TracePage } from "@/features/trace/trace-page";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/trace")({
  component: function Trace() {
    const { docId } = Route.useParams();
    return <TracePage docId={docId} />;
  },
});
