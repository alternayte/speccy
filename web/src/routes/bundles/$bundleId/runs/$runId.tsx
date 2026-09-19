import { createFileRoute } from "@tanstack/react-router";
import { ReportPage } from "@/features/report/report-page";

export const Route = createFileRoute("/bundles/$bundleId/runs/$runId")({
  component: function Report() {
    const { bundleId, runId } = Route.useParams();
    return <ReportPage bundleId={bundleId} runId={runId} />;
  },
});
