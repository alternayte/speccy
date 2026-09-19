import type { ReactNode } from "react";

// AuthCard frames the sign-in, invite, reset, and share pages.
export function AuthCard({ title, lead, children }: { title: string; lead?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-full items-start justify-center overflow-y-auto px-4 py-[12vh]">
      <div className="w-full max-w-[380px]">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {lead ? <p className="mt-1.5 text-sm text-ink-2">{lead}</p> : null}
        <div className="mt-6">{children}</div>
      </div>
    </div>
  );
}
