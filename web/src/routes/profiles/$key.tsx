import { createFileRoute } from "@tanstack/react-router";
import { ProfilePage } from "@/features/profiles/profile-page";

export const Route = createFileRoute("/profiles/$key")({
  component: function Profile() {
    return <ProfilePage profileKey={Route.useParams().key} />;
  },
});
