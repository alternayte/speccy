import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { GitPullRequest } from "lucide-react";
import type { GithubSource, PullInfo } from "@/lib/api";
import { listGithubSourcesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { ResultPill } from "./verdict";

// pullStateText is the state of a pull request in words.
export const pullStateText: Record<PullInfo["state"], string> = { open: "Open", merged: "Merged", closed: "Closed" };

// PullSources lists the pull requests Speccy follows. Each one names its spec docs with their
// doc type and verdict, so the PRD and the SDD of one pull request stay apart
// (docs/specs/pr-source-and-github-review.md).
export function PullSources() {
  const sources = useQuery(listGithubSourcesOptions());
  const pulls = (sources.data?.items ?? []).filter((s) => s.pull);
  if (pulls.length === 0) return null;
  return (
    <section className="mt-6" aria-labelledby="pull-sources">
      <h2 id="pull-sources" className="text-sm font-semibold text-ink">
        Pull requests
      </h2>
      <ul className="mt-2 space-y-2">
        {pulls.map((s) => (
          <PullSourceRow key={s.id} source={s} />
        ))}
      </ul>
    </section>
  );
}

function PullSourceRow({ source }: { source: GithubSource }) {
  const pr = source.pull!;
  return (
    <li className="overflow-hidden rounded-lg border border-line bg-surface">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-2">
        <GitPullRequest aria-hidden className="size-4 shrink-0 text-ink-3" />
        <a
          href={pr.url}
          target="_blank"
          rel="noreferrer"
          className="min-w-0 flex-1 truncate text-sm font-medium hover:underline"
        >
          PR #{pr.number} {pr.title}
        </a>
        <span className="text-xs text-ink-2">
          {source.repo} · by <span className="font-mono">{pr.author}</span>
        </span>
        <span className="text-xs text-ink-3">{pullStateText[pr.state]}</span>
      </div>
      {source.error ? <p className="px-3 py-2 text-xs text-bad">{source.error}</p> : null}
      <ul className="divide-y divide-line">
        {(source.docs ?? []).map((d) => (
          <li key={d.doc_id}>
            <Link
              to="/bundles/$bundleId/docs/$docId"
              params={{ bundleId: d.bundle_id, docId: d.doc_id }}
              className="grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-0.5 px-3 py-2 hover:bg-sunken sm:grid-cols-[1fr_6rem_9rem]"
            >
              <span title={d.path} className="min-w-0 truncate font-mono text-xs text-ink">
                {d.path}
              </span>
              <span className="hidden text-xs text-ink-2 sm:block">{d.profile}</span>
              <ResultPill result={d.verdict} />
            </Link>
          </li>
        ))}
      </ul>
    </li>
  );
}
