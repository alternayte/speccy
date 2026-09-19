import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { ErrorState, Loading } from "@/components/ui/states";
import { getContentReviewReport } from "@/lib/api";
import { problemMessage } from "@/lib/problem";

// ContentReviewPage shows the report of a review of files not saved on the server: the link
// that connected mode posts on a pull request (SDD §12.4). The report is self-contained HTML,
// so it shows in a frame with no scripts.
export function ContentReviewPage({ reviewId }: { reviewId: string }) {
  const url = `/api/v1/reviews/${reviewId}/report`;
  const report = useQuery({
    queryKey: ["contentReviewReport", reviewId],
    queryFn: async () => {
      const { data } = await getContentReviewReport({
        path: { reviewId },
        parseAs: "text",
        throwOnError: true,
      });
      return data as string;
    },
  });
  if (report.isPending) return <Loading label="Loading the report" />;
  if (report.isError)
    return (
      <div className="mx-auto max-w-[720px] p-6">
        <ErrorState message={problemMessage(report.error)} />
      </div>
    );
  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line bg-surface px-4 py-2">
        <p className="text-sm text-ink-2">
          A review of files that are not saved on the server. The server keeps it for 90 days.
        </p>
        <a
          href={url}
          download
          className="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-sunken hover:text-ink"
        >
          <Download aria-hidden className="size-3.5" /> Download
        </a>
      </div>
      <iframe title="Review report" sandbox="" srcDoc={report.data} className="min-h-0 w-full flex-1 bg-white" />
    </div>
  );
}
