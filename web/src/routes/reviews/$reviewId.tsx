import { createFileRoute } from "@tanstack/react-router";
import { ContentReviewPage } from "@/features/report/content-review-page";

export const Route = createFileRoute("/reviews/$reviewId")({
  component: function ContentReview() {
    return <ContentReviewPage reviewId={Route.useParams().reviewId} />;
  },
});
