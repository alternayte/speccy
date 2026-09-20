import { clsx } from "clsx";
import { useCallback, useEffect, useRef, useState } from "react";

// A divider is the separator between two panes. The person drags it, or focuses it and uses the
// arrow keys. Each width stays in this browser: a width is a preference of one screen, not a
// fact about the bundle, so it never travels to another device.

type Options = {
  // key is the storage key of this divider.
  key: string;
  // from says which edge the width is measured from.
  from: "left" | "right";
  min: number;
  max: number;
  // initial is the width with nothing stored, and the width a double click returns to. A
  // function runs once, for a width that depends on the window.
  initial: number | (() => number);
};

export function useDivider({ key, from, min, max, initial }: Options) {
  const start = useRef(0);
  if (start.current === 0) start.current = typeof initial === "function" ? initial() : initial;
  const [width, setWidth] = useState(() => {
    try {
      const v = Number(localStorage.getItem(key));
      return Number.isFinite(v) && v >= min && v <= max ? v : start.current;
    } catch {
      return start.current;
    }
  });
  const dragging = useRef(false);

  const put = useCallback(
    (v: number) => {
      const next = Math.round(Math.min(max, Math.max(min, v)));
      setWidth(next);
      try {
        localStorage.setItem(key, String(next));
      } catch {
        // Storage is not available: the width lasts for this page load.
      }
    },
    [key, min, max],
  );

  useEffect(() => {
    const move = (e: PointerEvent) => {
      if (!dragging.current) return;
      e.preventDefault();
      put(from === "left" ? e.clientX : window.innerWidth - e.clientX);
    };
    const up = () => {
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
  }, [from, put]);

  const props = {
    role: "separator" as const,
    "aria-orientation": "vertical" as const,
    "aria-valuenow": width,
    "aria-valuemin": min,
    "aria-valuemax": max,
    tabIndex: 0,
    onPointerDown: () => {
      dragging.current = true;
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
    },
    onDoubleClick: () => put(start.current),
    onKeyDown: (e: React.KeyboardEvent) => {
      const step = e.shiftKey ? 48 : 12;
      const towards = from === "left" ? 1 : -1;
      if (e.key === "ArrowLeft") put(width - step * towards);
      else if (e.key === "ArrowRight") put(width + step * towards);
      else if (e.key === "Home") put(start.current);
      else return;
      e.preventDefault();
    },
  };
  return { width, props };
}

// Divider draws the separator: a thin line that widens under the pointer.
export function Divider({
  label,
  className,
  ...props
}: { label: string; className?: string } & ReturnType<typeof useDivider>["props"]) {
  return (
    <div
      {...props}
      aria-label={label}
      title={`${label}. Drag it, or use the arrow keys. Double click resets it.`}
      className={clsx(
        "no-print group relative z-10 w-1 shrink-0 cursor-col-resize bg-line transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none",
        className,
      )}
    >
      {/* A wider hit area than the line, so the pointer catches it. */}
      <span aria-hidden className="absolute inset-y-0 -left-1 -right-1" />
    </div>
  );
}
