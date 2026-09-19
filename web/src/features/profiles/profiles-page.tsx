import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { listProfilesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// ProfilesPage lists the doc types (REQ-010).
export function ProfilesPage() {
  const profiles = useQuery(listProfilesOptions());
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[760px] px-4 py-8 sm:px-6">
        <h1 className="text-xl font-semibold tracking-tight">Profiles</h1>
        <p className="mt-1 text-sm text-ink-2">
          A profile sets the template, the checks, and the limits of one doc type. Anyone can suggest a change.
        </p>
        <div className="mt-6 overflow-hidden rounded-lg border border-line bg-surface">
          {profiles.isPending ? (
            <Loading label="Loading profiles" />
          ) : profiles.isError ? (
            <div className="p-3">
              <ErrorState message={problemMessage(profiles.error)} />
            </div>
          ) : profiles.data.items.length === 0 ? (
            <Empty title="No profiles" />
          ) : (
            <ul className="divide-y divide-line">
              {profiles.data.items.map((p) => (
                <li key={p.key}>
                  <Link
                    to="/profiles/$key"
                    params={{ key: p.key }}
                    className="flex items-baseline gap-3 px-4 py-3 hover:bg-sunken"
                  >
                    <span className="font-medium">{p.name}</span>
                    <span className="font-mono text-xs text-ink-3">{p.key}</span>
                    <span className="ml-auto text-xs text-ink-3">
                      v{p.version} · {p.origin}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {profiles.data?.problems.length ? (
            <div className="border-t border-line p-3">
              {profiles.data.problems.map((pr) => (
                <ErrorState key={pr} message={pr} />
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
