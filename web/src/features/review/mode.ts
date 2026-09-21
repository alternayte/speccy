import { useQuery } from "@tanstack/react-query";
import { useLocation } from "@tanstack/react-router";
import { useMe } from "@/features/account/me";
import { getBundleAccessOptions } from "@/lib/api/@tanstack/react-query.gen";

// useReviewerMode derives reviewer mode: a person who cannot edit the bundle gets the reduced
// surface, and a person who can edit asks for it with ?as=reviewer. Speccy stores nothing.
export function useReviewerMode(bundleId: string): { pending: boolean; reviewer: boolean } {
  const me = useMe();
  const hosted = me.data?.mode === "hosted";
  const access = useQuery({ ...getBundleAccessOptions({ path: { bundleId } }), enabled: hosted });
  const asked = new URLSearchParams(useLocation().searchStr).get("as") === "reviewer";
  if (me.isPending || (hosted && access.isPending)) return { pending: true, reviewer: false };
  const canEdit = !hosted || !!access.data?.can_edit;
  return { pending: false, reviewer: asked || !canEdit };
}
