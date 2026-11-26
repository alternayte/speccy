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

// statusError turns an error answer that is not a Problem, such as a proxy's HTML page, into an
// Error that names the HTTP status. A proxy status adds the likely cause
// (docs/specs/breakdown-proposal-job.md). A Problem and a network failure stay as they are.
export function statusError(err: unknown, res: Response | undefined): unknown {
  const p = err as Partial<Problem> | null;
  if (!res || (p && typeof p === "object" && typeof p.detail === "string" && p.detail)) return err;
  const status = `${res.status}${res.statusText ? ` ${res.statusText}` : ""}`;
  const proxy = res.status === 502 || res.status === 503 || res.status === 504;
  return new Error(
    `The server answered ${status}.${proxy ? " A proxy in front of Speccy may have cut a long request." : ""}`,
  );
}
