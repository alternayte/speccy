import { useQuery } from "@tanstack/react-query";
import { Navigate } from "@tanstack/react-router";
import { ErrorState, Loading } from "@/components/ui/states";
import type { SpecDoc } from "@/lib/api";
import { getBundleOptions, getSpecDocOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// firstDoc is the spec doc a bundle opens on: the first whose next action waits for this
// person, a Not Build Ready doc before the others, and otherwise the first by path.
export function firstDoc(docs: SpecDoc[]): SpecDoc | undefined {
  return (
    docs.find((d) => d.next_action && d.verdict?.result === "not_build_ready") ??
    docs.find((d) => d.next_action) ??
    docs[0]
  );
}

// BundleEntry opens a bundle on one of its spec docs.
export function BundleEntry({ bundleId }: { bundleId: string }) {
  const bundle = useQuery(getBundleOptions({ path: { bundleId } }));
  if (bundle.isPending) return <Loading />;
  if (bundle.isError) return <ErrorState message={problemMessage(bundle.error)} />;
  const doc = firstDoc(bundle.data.docs);
  if (!doc) return <ErrorState message="This bundle holds no spec doc." />;
  return <Navigate to="/bundles/$bundleId/docs/$docId" params={{ bundleId, docId: doc.id }} replace />;
}

// DocEntry opens one spec doc on its bundle's page.
export function DocEntry({ docId }: { docId: string }) {
  const doc = useQuery(getSpecDocOptions({ path: { docId } }));
  if (doc.isPending) return <Loading />;
  if (doc.isError) return <ErrorState message={problemMessage(doc.error)} />;
  return <Navigate to="/bundles/$bundleId/docs/$docId" params={{ bundleId: doc.data.bundle_id, docId }} replace />;
}
