import { AlertTriangle, Loader2 } from "lucide-react";
import type { ReactNode } from "react";

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 px-4 py-6 text-sm text-ink-3">
      <Loader2 aria-hidden className="size-4 animate-spin" />
      {label}
    </div>
  );
}

export function ErrorState({ message, action }: { message: string; action?: ReactNode }) {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-md border border-bad/30 bg-bad-soft px-3 py-2.5 text-sm text-ink"
    >
      <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-bad" />
      <div className="min-w-0 flex-1">{message}</div>
      {action}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="px-4 py-10 text-center">
      <p className="text-md font-medium text-ink">{title}</p>
      {children ? <div className="mx-auto mt-2 max-w-[48ch] text-sm text-ink-2">{children}</div> : null}
    </div>
  );
}
