import { createFileRoute } from "@tanstack/react-router";
import { BundlesPage } from "@/features/bundle/bundles-page";

export const Route = createFileRoute("/")({
  component: BundlesPage,
});
