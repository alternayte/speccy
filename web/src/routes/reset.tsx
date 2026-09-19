import { createFileRoute } from "@tanstack/react-router";
import { ResetPage } from "@/features/account/reset-page";

export const Route = createFileRoute("/reset")({ component: ResetPage });
