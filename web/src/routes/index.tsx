import { createFileRoute } from "@tanstack/react-router";
import { HomePage } from "@/features/meta/home-page";

export const Route = createFileRoute("/")({
  component: HomePage,
});
