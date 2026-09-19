import { createFileRoute } from "@tanstack/react-router";
import { SignInPage } from "@/features/account/sign-in-page";

export const Route = createFileRoute("/sign-in")({
  validateSearch: (s: Record<string, unknown>): { redirect?: string } =>
    typeof s.redirect === "string" ? { redirect: s.redirect } : {},
  component: function SignIn() {
    return <SignInPage redirect={Route.useSearch().redirect} />;
  },
});
