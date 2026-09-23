import { createFileRoute } from "@tanstack/react-router";
import { ReportPage } from "@/features/report/report-page";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/runs/$runId")({
  component: function Report() {
    const { docId, runId } = Route.useParams();
    return <ReportPage docId={docId} runId={runId} />;
  },
});
