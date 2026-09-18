import { clsx } from "clsx";
import { DropdownMenu as M } from "radix-ui";
import type { ReactNode } from "react";

export function Menu({ trigger, children }: { trigger: ReactNode; children: ReactNode }) {
  return (
    <M.Root>
      <M.Trigger asChild>{trigger}</M.Trigger>
      <M.Portal>
        <M.Content
          align="end"
          sideOffset={4}
          className="z-50 min-w-44 rounded-md border border-line bg-surface p-1 text-sm shadow-pop"
        >
          {children}
        </M.Content>
      </M.Portal>
    </M.Root>
  );
}

export function MenuItem({
  onSelect,
  icon,
  danger,
  children,
}: {
  onSelect: () => void;
  icon?: ReactNode;
  danger?: boolean;
  children: ReactNode;
}) {
  return (
    <M.Item
      onSelect={onSelect}
      className={clsx(
        "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 outline-none select-none data-[highlighted]:bg-sunken",
        danger ? "text-bad" : "text-ink",
      )}
    >
      {icon}
      {children}
    </M.Item>
  );
}
