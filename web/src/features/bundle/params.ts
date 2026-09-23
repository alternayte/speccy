import { useParams } from "@tanstack/react-router";

// useBundleId returns the bundle of the spec doc page that is open, from the route.
export function useBundleId(): string {
  const { bundleId } = useParams({ strict: false });
  return bundleId ?? "";
}
