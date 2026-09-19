import { createFileRoute } from "@tanstack/react-router";
import { InvitePage } from "@/features/account/invite-page";

export const Route = createFileRoute("/invite")({ component: InvitePage });
