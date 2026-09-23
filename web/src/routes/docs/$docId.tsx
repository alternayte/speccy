import { createFileRoute } from "@tanstack/react-router";
import { DocEntry } from "@/features/bundle/bundle-entry";

// A link that knows only a spec doc lands here and moves to the doc's page in its bundle.
export const Route = createFileRoute("/docs/$docId")({
  component: function Doc() {
    const { docId } = Route.useParams();
    return <DocEntry docId={docId} />;
  },
});
