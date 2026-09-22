import { clsx } from "clsx";
import { Dialog as D } from "radix-ui";
import type { ReactNode } from "react";

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  wide = false,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children?: ReactNode;
  footer?: ReactNode;
  // wide is for a dialog that explains something. A column of prose in 480px scrolls inside a
  // box on a screen with room to spare.
  wide?: boolean;
}) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-scrim" />
        <D.Content
          className={clsx(
            "fixed top-[12vh] left-1/2 z-50 w-[calc(100vw-2rem)] -translate-x-1/2 rounded-lg border border-line bg-surface p-5 shadow-pop focus:outline-none",
            wide ? "max-w-[860px]" : "max-w-[480px]",
          )}
        >
          <D.Title className="text-md font-semibold text-ink">{title}</D.Title>
          {description ? (
            <D.Description className="mt-1 text-sm text-ink-2">{description}</D.Description>
          ) : (
            <D.Description className="sr-only">{title}</D.Description>
          )}
          {children ? <div className="mt-4">{children}</div> : null}
          {footer ? <div className="mt-5 flex justify-end gap-2">{footer}</div> : null}
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}
