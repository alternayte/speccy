import { Link, Outlet } from "@tanstack/react-router";
import { ThemeToggle } from "./theme";

export function AppShell() {
  return (
    <div className="flex h-full flex-col">
      <header className="no-print flex h-11 shrink-0 items-center gap-4 border-b border-line bg-surface px-4">
        <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight text-ink">
          <span aria-hidden className="inline-block size-3 rounded-sm bg-accent" />
          Speccy
        </Link>
        <nav className="flex items-center gap-1 text-sm">
          <Link
            to="/"
            activeOptions={{ exact: true }}
            className="rounded-md px-2 py-1 text-ink-2 hover:bg-sunken hover:text-ink"
            activeProps={{ className: "text-ink" }}
          >
            Bundles
          </Link>
          <Link
            to="/admin"
            className="rounded-md px-2 py-1 text-ink-2 hover:bg-sunken hover:text-ink"
            activeProps={{ className: "text-ink" }}
          >
            Admin
          </Link>
        </nav>
        <div className="ml-auto">
          <ThemeToggle />
        </div>
      </header>
      <div className="min-h-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}
