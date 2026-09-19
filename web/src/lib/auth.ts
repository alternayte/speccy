import { AuthAllError, createAuthClient } from "@alternayte/auth-all-client";
import { problemMessage } from "./problem";

// auth is the auth-all client of hosted mode, at /api/auth on this origin.
export const auth = createAuthClient({ baseUrl: window.location.origin });

// Messages in Speccy's words for the auth-all codes a person meets.
const messages: Record<string, string> = {
  INVALID_CREDENTIALS: "The email or the password is wrong.",
  RATE_LIMITED: "Too many attempts. Wait a few minutes, then try again.",
  LINK_INVALID: "The link is invalid, used, or expired. Ask an admin for a new one.",
  WEAK_PASSWORD: "The password needs at least 12 characters.",
  EMAIL_ALREADY_EXISTS: "An account with this email exists already. Sign in instead.",
  SIGN_UP_CLOSED: "Speccy has no open sign-up. Ask an admin for an invite link.",
  USER_DISABLED: "This account is disabled. Ask an admin.",
  INVALID_TOTP_CODE: "The code is wrong. Try the current code from your authenticator app.",
  LAST_ADMIN: "This is the last admin. Make another admin first.",
};

// authMessage returns the user-facing text of an auth-all error.
export function authMessage(err: unknown): string {
  if (err instanceof AuthAllError) return messages[err.code] ?? err.message;
  return problemMessage(err);
}

// speccyAuth calls a route of Speccy's auth-all plugin (invites and reset links).
export async function speccyAuth<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`/api/auth/speccy/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(body),
  });
  const data = (await res.json().catch(() => ({}))) as { error?: { code: string; message: string } } & T;
  if (!res.ok) {
    const e = data.error ?? { code: "INTERNAL", message: "Speccy could not reach the server." };
    throw new AuthAllError(e.code, e.message, res.status);
  }
  return data;
}

// tokenFromHash reads the token of an invite or reset link. It sits in the fragment, so it
// never reaches a server log.
export function tokenFromHash(): string {
  return new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "";
}

export const providerLabel: Record<string, string> = {
  oidc: "single sign-on",
  github: "GitHub",
};
