import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { suggestLinks, type ConfirmedLink } from "@/lib/api";

// Scope says which bundles count as docs beside the one being adopted: the bundles on disk, or
// the bundles of one GitHub source.
type Scope = { local: true } | { source_id: string };

// useLinkOffers asks which links Speccy offers for the docs about to be adopted, each with the
// profile picked for it, and holds which links the person keeps. The docs of the list count
// as targets too, so an SDD is offered its PRD before either is accepted. Speccy offers a link
// only when exactly one doc in the same folder has the upstream type, and never makes it
// without the person.
export function useLinkOffers(docs: { path: string; profile: string }[], scope: Scope) {
  const picked = docs.filter((d) => d.profile);
  const offers = useQuery({
    queryKey: ["suggestLinks", picked, scope],
    enabled: picked.length > 0,
    queryFn: async () => {
      const res = await suggestLinks({ body: { docs: picked, ...scope }, throwOnError: true });
      return res.data.items;
    },
  });
  const [dropped, setDropped] = useState<Record<string, boolean>>({});
  const offerFor = (path: string) => offers.data?.find((o) => o.from === path);
  const link = (path: string): ConfirmedLink | undefined => {
    const o = offerFor(path);
    return o && !dropped[path] ? { kind: o.kind, target: o.to } : undefined;
  };
  const view = (path: string) => {
    const o = offerFor(path);
    if (!o) return null;
    return (
      <label className="flex w-full items-center gap-2 text-2xs text-ink-2">
        <input
          type="checkbox"
          checked={!dropped[path]}
          onChange={(e) => setDropped({ ...dropped, [path]: !e.target.checked })}
        />
        <span>
          Link: {o.kind} <code>{o.to}</code>
        </span>
      </label>
    );
  };
  return { link, view };
}
