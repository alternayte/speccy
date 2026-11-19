import { createFileRoute } from "@tanstack/react-router";
import { SetupGitHubPage } from "@/features/setup/setup-page";

export const Route = createFileRoute("/setup/github")({ component: SetupGitHubPage });
