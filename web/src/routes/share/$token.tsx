import { createFileRoute } from "@tanstack/react-router";
import { SharePage } from "@/features/account/share-page";

export const Route = createFileRoute("/share/$token")({
  component: function Share() {
    return <SharePage token={Route.useParams().token} />;
  },
});
