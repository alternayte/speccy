import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { suggestLinks } from "@/lib/api";

// Scope says which bundles count as docs beside the one being adopted: the bundles on disk, or
// the bundles of one GitHub source.
type Scope = { local: true } | { source_id: string };

// useLinkOffer asks which link Speccy offers for one doc about to be adopted with profile, and
// holds whether the person keeps it. It offers a link only when exactly one doc in the same
// folder has the upstream type; Speccy never makes it without the person.
export function useLinkOffer(path: string, profile: string, scope: Scope) {
  const offer = useQuery({
    queryKey: ["suggestLinks", path, profile, scope],
    enabled: !!profile,
    queryFn: async () => {
      const res = await suggestLinks({ body: { docs: [{ path, profile }], ...scope }, throwOnError: true });
      return res.data.items[0] ?? null;
    },
  });
  const [keep, setKeep] = useState(true);
  const link = offer.data && keep ? { kind: offer.data.kind, target: offer.data.to } : undefined;
  const view = offer.data ? (
    <label className="flex w-full items-center gap-2 text-2xs text-ink-2">
      <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
      <span>
        Link: {offer.data.kind} <code>{offer.data.to}</code>
      </span>
    </label>
  ) : null;
  return { link, view };
}
