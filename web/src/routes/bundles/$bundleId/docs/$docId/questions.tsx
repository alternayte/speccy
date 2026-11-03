import { createFileRoute } from "@tanstack/react-router";
import { QuestionsPage } from "@/features/questions/questions-page";

export const Route = createFileRoute("/bundles/$bundleId/docs/$docId/questions")({
  component: function Questions() {
    const { docId } = Route.useParams();
    return <QuestionsPage docId={docId} />;
  },
});
