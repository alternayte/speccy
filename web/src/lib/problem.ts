import type { Problem } from "./api";

// problemMessage returns the user-facing text of an API error (RFC 9457 detail).
export function problemMessage(err: unknown): string {
  const p = err as Partial<Problem> | null;
  if (p && typeof p === "object" && typeof p.detail === "string" && p.detail) return p.detail;
  // fetch rejects with a TypeError ("Failed to fetch") when the server cannot be reached.
  if (err instanceof TypeError) return "Speccy could not reach the server. Check that it is running, then try again.";
  if (err instanceof Error && err.message) return err.message;
  return "Speccy could not reach the server. Check that it is running, then try again.";
}

export function problemCode(err: unknown): string | undefined {
  const p = err as Partial<Problem> | null;
  return p && typeof p === "object" && typeof p.code === "string" ? p.code : undefined;
}
