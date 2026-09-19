import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { OneTimeValue } from "@/features/account/account-page";
import { useMe } from "@/features/account/me";
import type { Settings } from "@/lib/api";
import {
  createInviteMutation,
  createResetLinkMutation,
  getSettingsOptions,
  getSettingsQueryKey,
  listInvitesOptions,
  listInvitesQueryKey,
  revokeInviteMutation,
  setSettingsMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { auth, authMessage } from "@/lib/auth";
import { problemMessage } from "@/lib/problem";
import { Section } from "./admin-page";

// PeopleSection lists the accounts, with role changes, disable, and reset links (REQ-082).
export function PeopleSection() {
  const qc = useQueryClient();
  const me = useMe();
  const users = useQuery({ queryKey: ["admin-users"], queryFn: () => auth.admin.listUsers() });
  const [reset, setReset] = useState<{ email: string; url: string }>();
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin-users"] });
  const role = useMutation({
    mutationFn: (v: { id: string; role: string }) => auth.admin.setUserRole(v.id, { role: v.role }),
    onSuccess: refresh,
  });
  const toggle = useMutation({
    mutationFn: (v: { id: string; disable: boolean }) =>
      v.disable ? auth.admin.disableUser(v.id) : auth.admin.enableUser(v.id),
    onSuccess: refresh,
  });
  const resetLink = useMutation({
    ...createResetLinkMutation(),
    onSuccess: (res, v) => setReset({ email: v.body.email, url: res.url }),
  });
  const error = role.error ?? toggle.error;
  return (
    <Section title="Accounts">
      {users.isPending ? (
        <Loading label="Loading people" />
      ) : users.isError ? (
        <div className="p-3">
          <ErrorState message={authMessage(users.error)} />
        </div>
      ) : users.data.users.length === 0 ? (
        <Empty title="No accounts yet" />
      ) : (
        <ul className="divide-y divide-line">
          {users.data.users.map((u) => {
            const self = u.id === me.data?.user_id;
            return (
              <li key={u.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5 text-sm">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">{u.name || u.email}</p>
                  <p className="truncate text-xs text-ink-3">
                    {u.email}
                    {u.disabledAt ? " · disabled" : ""}
                    {self ? " · you" : ""}
                  </p>
                </div>
                <select
                  aria-label={`Role of ${u.email}`}
                  value={u.role === "admin" ? "admin" : "member"}
                  disabled={self || role.isPending}
                  onChange={(e) => role.mutate({ id: u.id, role: e.target.value })}
                  className="h-7 rounded-md border border-line-strong bg-surface px-1.5 text-xs"
                >
                  <option value="member">Member</option>
                  <option value="admin">Admin</option>
                </select>
                <Button
                  size="sm"
                  icon={<KeyRound className="size-3.5" />}
                  onClick={() => resetLink.mutate({ body: { email: u.email } })}
                  disabled={resetLink.isPending}
                >
                  Reset link
                </Button>
                {self ? null : (
                  <Button size="sm" onClick={() => toggle.mutate({ id: u.id, disable: !u.disabledAt })}>
                    {u.disabledAt ? "Enable" : "Disable"}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {error ? (
        <div className="border-t border-line p-3">
          <ErrorState message={authMessage(error)} />
        </div>
      ) : null}
      {resetLink.isError ? (
        <div className="border-t border-line p-3">
          <ErrorState message={problemMessage(resetLink.error)} />
        </div>
      ) : null}
      {reset ? (
        <div className="border-t border-line px-4 pb-4">
          <OneTimeValue
            label={`Reset link for ${reset.email}. It works once, for 24 hours. Speccy shows it one time.`}
            value={reset.url}
          />
        </div>
      ) : null}
    </Section>
  );
}

// InvitesSection makes and lists invite links (REQ-081).
export function InvitesSection() {
  const qc = useQueryClient();
  const invites = useQuery(listInvitesOptions());
  const [role, setRole] = useState<"member" | "admin">("member");
  const [made, setMade] = useState<string>();
  const create = useMutation({
    ...createInviteMutation(),
    onSuccess: (res) => {
      setMade(res.url);
      qc.invalidateQueries({ queryKey: listInvitesQueryKey() });
    },
  });
  const revoke = useMutation({
    ...revokeInviteMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: listInvitesQueryKey() }),
  });
  return (
    <Section
      title="Invite links"
      action={
        <div className="flex items-center gap-2">
          <select
            aria-label="Role of the invite"
            value={role}
            onChange={(e) => setRole(e.target.value as "member" | "admin")}
            className="h-7 rounded-md border border-line-strong bg-surface px-1.5 text-xs"
          >
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </select>
          <Button
            variant="primary"
            size="sm"
            icon={<Plus className="size-3.5" />}
            onClick={() => create.mutate({ body: { role } })}
            disabled={create.isPending}
          >
            Make an invite link
          </Button>
        </div>
      }
    >
      {made ? (
        <div className="px-4 pb-4">
          <OneTimeValue label="The invite link works once. Speccy shows it one time." value={made} />
        </div>
      ) : null}
      {create.isError ? (
        <div className="p-3">
          <ErrorState message={problemMessage(create.error)} />
        </div>
      ) : null}
      {invites.isPending ? (
        <Loading label="Loading invites" />
      ) : invites.isError ? (
        <div className="p-3">
          <ErrorState message={problemMessage(invites.error)} />
        </div>
      ) : invites.data.items.length === 0 ? (
        <Empty title="No invite links yet" />
      ) : (
        <ul className="divide-y divide-line">
          {invites.data.items.map((i) => (
            <li key={i.id} className="flex items-center gap-4 px-4 py-2 text-sm">
              <span className="w-16 capitalize">{i.role}</span>
              <span className="text-xs text-ink-2">
                {i.status === "used"
                  ? `Used by ${i.used_by ?? "someone"}`
                  : i.status === "pending"
                    ? `Open until ${new Date(i.expires_at).toLocaleDateString()}`
                    : i.status === "revoked"
                      ? "Revoked"
                      : "Expired"}
              </span>
              <span className="ml-auto text-xs text-ink-3">{new Date(i.created_at).toLocaleDateString()}</span>
              {i.status === "pending" ? (
                <Button size="sm" onClick={() => revoke.mutate({ path: { inviteId: i.id } })}>
                  Revoke
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

// SettingsSection sets the workspace settings (REQ-009, REQ-081, REQ-105).
export function SettingsSection({ hosted }: { hosted: boolean }) {
  const qc = useQueryClient();
  const settings = useQuery(getSettingsOptions());
  const [draft, setDraft] = useState<Settings>();
  const save = useMutation({
    ...setSettingsMutation(),
    onSuccess: () => {
      setDraft(undefined);
      qc.invalidateQueries({ queryKey: getSettingsQueryKey() });
    },
  });
  if (settings.isPending) return <Loading label="Loading the settings" />;
  if (settings.isError) return <ErrorState message={problemMessage(settings.error)} />;
  const s = draft ?? settings.data;
  const field = (key: keyof Settings, label: string, help: string) => (
    <div>
      <Label htmlFor={key}>{label}</Label>
      <Input
        id={key}
        inputMode="numeric"
        className="w-28"
        value={String(s[key])}
        onChange={(e) => setDraft({ ...s, [key]: Number(e.target.value.replace(/[^0-9]/g, "")) || 0 })}
      />
      <p className="mt-1 text-xs text-ink-3">{help}</p>
    </div>
  );
  return (
    <Section title="Workspace settings">
      <form
        className="grid gap-4 px-4 py-4 sm:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate({ body: s });
        }}
      >
        {field("parallel_calls", "Model calls at a time", "Per review run. 1 to 16.")}
        {hosted ? (
          <>
            {field("max_file_mb", "Largest file (MB)", "1 to 50.")}
            {field("max_bundle_mb", "Largest bundle (MB)", "At least the file limit, at most 500.")}
            {field("invite_ttl_days", "Invite links expire after (days)", "1 to 90.")}
          </>
        ) : null}
        <div className="sm:col-span-2">
          <Button type="submit" size="sm" disabled={!draft || save.isPending}>
            Save
          </Button>
          {save.isError ? (
            <div className="mt-2">
              <ErrorState message={problemMessage(save.error)} />
            </div>
          ) : null}
        </div>
      </form>
    </Section>
  );
}
