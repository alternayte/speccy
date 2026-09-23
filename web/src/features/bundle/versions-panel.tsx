import { useBundleId } from "./params";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { GitCompare } from "lucide-react";
import { useEffect } from "react";
import { ErrorState, Loading } from "@/components/ui/states";
import { listVersionsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "./time";

// VersionsPanel lists the versions of a bundle (REQ-005), newest first, each with a link to
// its diff against the current version (REQ-006).
export function VersionsPanel({ docId, current }: { docId: string; current: string }) {
  const bundleId = useBundleId();
  const versions = useQuery(listVersionsOptions({ path: { docId }, query: { limit: 50 } }));
  const { refetch } = versions;
  // A new current version means a new row.
  useEffect(() => {
    refetch();
  }, [current, refetch]);
  return (
    <section aria-label="Versions">
      <div>
        {versions.isPending ? (
          <Loading label="Loading versions" />
        ) : versions.isError ? (
          <div className="p-2">
            <ErrorState message={problemMessage(versions.error)} />
          </div>
        ) : (
          <ol>
            {versions.data.items.map((v) => (
              <li key={v.id} className="border-b border-line px-3 py-2">
                <div className="flex items-baseline gap-2">
                  <span className="font-mono text-xs font-medium text-ink">v{v.number}</span>
                  <span className="min-w-0 flex-1 truncate text-xs text-ink-2" title={v.message}>
                    {v.message}
                  </span>
                </div>
                <div className="mt-0.5 flex items-center gap-2 text-2xs text-ink-3">
                  <time dateTime={v.created_at} title={new Date(v.created_at).toLocaleString()}>
                    {relativeTime(v.created_at)}
                  </time>
                  {v.id === current ? (
                    <span className="text-accent">current</span>
                  ) : (
                    <Link
                      to="/bundles/$bundleId/docs/$docId/diff"
                      params={{ bundleId, docId }}
                      search={{ from: v.id, to: current }}
                      className="ml-auto inline-flex items-center gap-1 text-ink-2 hover:text-accent"
                    >
                      <GitCompare aria-hidden className="size-3" />
                      Compare with current
                    </Link>
                  )}
                </div>
              </li>
            ))}
          </ol>
        )}
      </div>
    </section>
  );
}
