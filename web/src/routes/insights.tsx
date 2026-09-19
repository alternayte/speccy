import { createFileRoute } from "@tanstack/react-router";
import { InsightsPage } from "@/features/insights/insights-page";

export const Route = createFileRoute("/insights")({ component: InsightsPage });
