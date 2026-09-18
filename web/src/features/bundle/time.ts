const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
];

const fmt = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

// relativeTime returns "5 minutes ago" for an ISO time.
export function relativeTime(iso: string, now = Date.now()): string {
  const secs = Math.round((new Date(iso).getTime() - now) / 1000);
  for (const [unit, size] of units) {
    if (Math.abs(secs) >= size) return fmt.format(Math.round(secs / size), unit);
  }
  return "just now";
}
