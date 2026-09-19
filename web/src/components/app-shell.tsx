import { useQueryClient } from "@tanstack/react-query";
import { Link, Navigate, Outlet, useLocation, useNavigate } from "@tanstack/react-router";
import { CircleUser, LogOut, UserRound } from "lucide-react";
import { useMe } from "@/features/account/me";
import { auth } from "@/lib/auth";
import { Loading } from "./ui/states";
import { Menu, MenuItem } from "./ui/menu";
import { ThemeToggle } from "./theme";

// Pages that work without a sign-in in hosted mode.
const openPaths = ["/sign-in", "/invite", "/reset"];

export function AppShell() {
  const me = useMe();
  const { pathname } = useLocation();
  const open = openPaths.includes(pathname) || pathname.startsWith("/share/");
  const m = me.data;
  const hosted = m?.mode === "hosted";

  if (me.isPending) return <Loading />;
  // SDD §3: hosted mode needs a sign-in, or a guest cookie for one bundle.
  if (hosted && !open) {
    if (m?.guest) {
      const own = `/bundles/${m.guest.bundle_id}`;
      if (!pathname.startsWith(own))
        return <Navigate to="/bundles/$bundleId" params={{ bundleId: m.guest.bundle_id }} />;
    } else if (!m?.signed_in) {
      return <Navigate to="/sign-in" search={{ redirect: pathname === "/" ? undefined : pathname }} />;
    }
  }
  const member = !hosted || m?.signed_in;
  const admin = !hosted || m?.role === "admin";

  return (
    <div className="flex h-full flex-col">
      <header className="no-print flex h-11 shrink-0 items-center gap-4 border-b border-line bg-surface px-4">
        <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight text-ink">
          <span aria-hidden className="inline-block size-3 rounded-sm bg-accent" />
          Speccy
        </Link>
        {member ? (
          <nav className="flex items-center gap-1 text-sm">
            <Link
              to="/"
              activeOptions={{ exact: true }}
              className="rounded-md px-2 py-1 text-ink-2 hover:bg-sunken hover:text-ink"
              activeProps={{ className: "text-ink" }}
            >
              Bundles
            </Link>
            {admin ? (
              <Link
                to="/admin"
                className="rounded-md px-2 py-1 text-ink-2 hover:bg-sunken hover:text-ink"
                activeProps={{ className: "text-ink" }}
              >
                Admin
              </Link>
            ) : null}
          </nav>
        ) : null}
        <div className="ml-auto flex items-center gap-2">
          {hosted && m?.guest ? (
            <span className="text-xs text-ink-2">
              Guest: <span className="font-medium text-ink">{m.guest.name}</span>
            </span>
          ) : null}
          {hosted && m?.signed_in ? <UserMenu email={m.email ?? ""} /> : null}
          <ThemeToggle />
        </div>
      </header>
      <div className="min-h-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}

function UserMenu({ email }: { email: string }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return (
    <Menu
      trigger={
        <button
          type="button"
          className="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-sunken hover:text-ink"
        >
          <CircleUser aria-hidden className="size-4" />
          <span className="hidden max-w-[200px] truncate sm:inline">{email}</span>
          <span className="sr-only sm:hidden">Account menu</span>
        </button>
      }
    >
      <MenuItem icon={<UserRound className="size-3.5" />} onSelect={() => navigate({ to: "/account" })}>
        Account
      </MenuItem>
      <MenuItem
        icon={<LogOut className="size-3.5" />}
        onSelect={async () => {
          await auth.signOut().catch(() => undefined);
          qc.clear();
          navigate({ to: "/sign-in" });
        }}
      >
        Sign out
      </MenuItem>
    </Menu>
  );
}
