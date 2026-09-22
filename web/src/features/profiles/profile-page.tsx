import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { clsx } from "clsx";
import { ArrowLeft, MessageSquarePlus, Save } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { useMe } from "@/features/account/me";
import { CodeEditor } from "@/features/editor/code-editor";
import { SideBySide } from "@/features/diff/side-by-side";
import { ProfileHelp } from "./profile-help";
import { ThreadView } from "@/features/threads/threads-panel";
import type { ProfileDetail } from "@/lib/api";
import {
  getProfileOptions,
  getProfileQueryKey,
  listPeopleOptions,
  listProfileThreadsOptions,
  listProfileThreadsQueryKey,
  openProfileThreadMutation,
  deleteProfileMutation,
  diffProfileVersionsOptions,
  listProfilesOptions,
  rollbackProfileMutation,
  setMaintainersMutation,
  updateProfileMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { relativeTime } from "@/features/bundle/time";

// ProfilePage edits one profile (REQ-013): its YAML and template, as a new version on save
// (REQ-012). Maintainers and admins edit; admins name the maintainers; members suggest
// changes as threads (REQ-015).
export function ProfilePage({ profileKey }: { profileKey: string }) {
  const profile = useQuery(getProfileOptions({ path: { key: profileKey } }));
  const admin = useMe().data?.role === "admin";
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[1600px] space-y-8 px-4 py-8 sm:px-6">
        <Link to="/profiles" className="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink">
          <ArrowLeft aria-hidden className="size-3.5" /> Profiles
        </Link>
        {profile.isPending ? (
          <Loading label="Loading the profile" />
        ) : profile.isError ? (
          <ErrorState message={problemMessage(profile.error)} />
        ) : (
          <>
            <header className="flex items-start gap-4">
              <div className="min-w-0 flex-1">
                <h1 className="text-xl font-semibold tracking-tight">{profile.data.name}</h1>
                <p className="mt-1 text-sm text-ink-2">
                  <span className="font-mono text-xs">{profile.data.key}</span> · version {profile.data.version} ·{" "}
                  {profile.data.origin}
                </p>
              </div>
              <ProfileHelp />
              {admin ? <DeleteProfile p={profile.data} /> : null}
            </header>
            <Editor p={profile.data} />
            <Suggestions profileKey={profileKey} />
            <Maintainers p={profile.data} />
            <Versions p={profile.data} />
          </>
        )}
      </div>
    </div>
  );
}

function Editor({ p }: { p: ProfileDetail }) {
  const qc = useQueryClient();
  const [yaml, setYaml] = useState(p.yaml);
  const [template, setTemplate] = useState(p.template);
  const save = useMutation({
    ...updateProfileMutation(),
    onSuccess: (d) => qc.setQueryData(getProfileQueryKey({ path: { key: p.key } }), d),
  });
  const dirty = yaml !== p.yaml || template !== p.template;
  const editable = p.can_edit && p.editable;
  const doSave = () => {
    if (editable && dirty && !save.isPending) save.mutate({ path: { key: p.key }, body: { yaml, template } });
  };
  return (
    <section>
      <div className="mb-2 flex items-end justify-between gap-4">
        <div>
          <h2 className="text-md font-semibold">Profile and template</h2>
          <p className="text-xs text-ink-2">
            {editable
              ? "Saving makes a new version. Earlier reviews keep the version they used."
              : "Only a maintainer of this profile or an admin can change it. Suggest a change below."}
          </p>
        </div>
        {editable ? (
          <Button
            variant="primary"
            size="sm"
            icon={<Save className="size-3.5" />}
            disabled={!dirty || save.isPending}
            onClick={doSave}
          >
            {save.isPending ? "Saving" : "Save a new version"}
          </Button>
        ) : null}
      </div>
      {save.isError ? (
        <div className="mb-2">
          <pre className="rounded-md border border-bad/40 bg-bad/10 p-3 text-xs whitespace-pre-wrap text-bad">
            {problemMessage(save.error)}
          </pre>
        </div>
      ) : null}
      {/* The editors are the work, so they take the screen: a pane that scrolls inside a page
          that scrolls makes a person move two things to read one. */}
      <div className="grid gap-3 lg:grid-cols-2">
        <div className="h-[calc(100vh-22rem)] min-h-[420px] overflow-hidden rounded-md border border-line">
          <CodeEditor
            docKey={`${p.key}.yaml@${p.version}`}
            initial={p.yaml}
            path={`${p.key}.yaml`}
            readOnly={!editable}
            onChange={setYaml}
            onSave={doSave}
          />
        </div>
        <div className="h-[calc(100vh-22rem)] min-h-[420px] overflow-hidden rounded-md border border-line">
          <CodeEditor
            docKey={`${p.key}.md@${p.version}`}
            initial={p.template}
            path="template.md"
            readOnly={!editable}
            onChange={setTemplate}
            onSave={doSave}
          />
        </div>
      </div>
    </section>
  );
}

function Suggestions({ profileKey }: { profileKey: string }) {
  const qc = useQueryClient();
  const me = useMe();
  const threads = useQuery(listProfileThreadsOptions({ path: { key: profileKey } }));
  const [open, setOpen] = useState<string>();
  const [check, setCheck] = useState("");
  const [body, setBody] = useState("");
  const create = useMutation({
    ...openProfileThreadMutation(),
    onSuccess: (t) => {
      setBody("");
      setCheck("");
      qc.invalidateQueries({ queryKey: listProfileThreadsQueryKey({ path: { key: profileKey } }) });
      setOpen(t.id);
    },
  });
  if (open) {
    return (
      <section className="rounded-lg border border-line bg-surface">
        <ThreadView threadId={open} member={!me.data?.guest} onBack={() => setOpen(undefined)} />
      </section>
    );
  }
  return (
    <section>
      <h2 className="text-md font-semibold">Suggestions</h2>
      <p className="text-xs text-ink-2">
        Suggest a change to a check, the template, or a limit. A maintainer applies it by editing the profile.
      </p>
      <form
        className="mt-3 grid gap-2 sm:grid-cols-[200px_1fr_auto] sm:items-start"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate({
            path: { key: profileKey },
            body: {
              anchor_kind: "check",
              anchor: { check_slug: check.trim() || "profile" },
              addressed_to: "humans",
              body,
            },
          });
        }}
      >
        <div>
          <Label htmlFor="check">Check, or blank for the profile</Label>
          <Input id="check" placeholder="sdd.limits" value={check} onChange={(e) => setCheck(e.target.value)} />
        </div>
        <div>
          <Label htmlFor="suggestion">Suggestion</Label>
          <Textarea
            id="suggestion"
            rows={2}
            className="font-sans text-sm"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            required
          />
        </div>
        <Button
          type="submit"
          className="sm:mt-5"
          icon={<MessageSquarePlus className="size-3.5" />}
          disabled={create.isPending || !body.trim()}
        >
          Suggest
        </Button>
      </form>
      {create.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(create.error)} />
        </div>
      ) : null}
      <div className="mt-3 overflow-hidden rounded-lg border border-line bg-surface">
        {threads.isPending ? (
          <Loading label="Loading suggestions" />
        ) : threads.isError ? (
          <div className="p-3">
            <ErrorState message={problemMessage(threads.error)} />
          </div>
        ) : threads.data.items.length === 0 ? (
          <Empty title="No suggestions" />
        ) : (
          <ul className="divide-y divide-line">
            {threads.data.items.map((t) => (
              <li key={t.id}>
                <button
                  type="button"
                  onClick={() => setOpen(t.id)}
                  className="block w-full px-4 py-2.5 text-left hover:bg-sunken"
                >
                  <span className="flex items-baseline gap-2 text-xs">
                    <span className={t.status === "open" ? "font-semibold text-accent" : "font-semibold text-ok"}>
                      {t.status === "open" ? "Open" : "Resolved"}
                    </span>
                    <span className="font-mono text-ink-3">{String(t.anchor.check_slug ?? "profile")}</span>
                    <span className="ml-auto text-ink-3">{relativeTime(t.last_message_at)}</span>
                  </span>
                  <span className="mt-0.5 block text-sm">{t.title}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function Maintainers({ p }: { p: ProfileDetail }) {
  const qc = useQueryClient();
  const me = useMe();
  const admin = me.data?.mode === "local" || me.data?.role === "admin";
  const people = useQuery({ ...listPeopleOptions(), enabled: admin || p.maintainers.length > 0 });
  const set = useMutation({
    ...setMaintainersMutation(),
    onSuccess: (d) => qc.setQueryData(getProfileQueryKey({ path: { key: p.key } }), d),
  });
  const name = (id: string) => people.data?.items.find((x) => x.id === id)?.name ?? id;
  if (me.data?.mode === "local") return null;
  return (
    <section>
      <h2 className="text-md font-semibold">Maintainers</h2>
      <p className="text-xs text-ink-2">
        Maintainers edit this profile and approve the waivers that its policy gives them.
      </p>
      {admin && people.data ? (
        <ul className="mt-3 space-y-1">
          {people.data.items.map((x) => (
            <li key={x.id}>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  className="accent-[var(--color-accent)]"
                  checked={p.maintainers.includes(x.id)}
                  disabled={set.isPending}
                  onChange={(e) => {
                    const next = e.target.checked ? [...p.maintainers, x.id] : p.maintainers.filter((m) => m !== x.id);
                    set.mutate({ path: { key: p.key }, body: { user_ids: next } });
                  }}
                />
                {x.name} <span className="text-xs text-ink-3">{x.email}</span>
              </label>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-2 text-sm">{p.maintainers.length ? p.maintainers.map(name).join(", ") : "None yet."}</p>
      )}
      {set.isError ? <ErrorState message={problemMessage(set.error)} /> : null}
    </section>
  );
}

// DeleteProfile removes a doc type. A built-in ships in the binary, and a profile a bundle
// names still answers for that bundle's rubric, so the server refuses both and says why.
function DeleteProfile({ p }: { p: ProfileDetail }) {
  const go = useNavigate();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const del = useMutation({
    ...deleteProfileMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: listProfilesOptions().queryKey });
      setOpen(false);
      void go({ to: "/profiles" });
    },
  });
  return (
    <>
      <Button variant="ghost" onClick={() => setOpen(true)}>
        Delete
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={`Delete ${p.name}`}
        description="The profile and its versions go. A bundle that names this doc type keeps Speccy from removing it."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button variant="danger" disabled={del.isPending} onClick={() => del.mutate({ path: { key: p.key } })}>
              Delete the profile
            </Button>
          </>
        }
      >
        {del.isError ? <ErrorState message={problemMessage(del.error)} /> : null}
      </Dialog>
    </>
  );
}

// Versions lists the versions of a profile, diffs any two, and rolls an old one forward. A
// rollback writes a new version with the old text: a review run pins the version it used, so
// no number ever changes meaning.
function Versions({ p }: { p: ProfileDetail }) {
  const qc = useQueryClient();
  const [pick, setPick] = useState<number | null>(null);
  const latest = p.versions[0]?.version ?? p.version;
  const from = pick ?? p.versions[1]?.version ?? latest;
  const to = pick === null ? latest : latest;
  const diff = useQuery({
    ...diffProfileVersionsOptions({ path: { key: p.key }, query: { from_version: from, to_version: to } }),
    enabled: from !== to,
  });
  const rollback = useMutation({
    ...rollbackProfileMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: getProfileOptions({ path: { key: p.key } }).queryKey }),
  });
  return (
    <section>
      <h2 className="text-md font-semibold">Versions</h2>
      <p className="mt-1 text-sm text-ink-2">
        A rollback writes a new version with the text of the one you pick. Earlier reviews keep the version they used.
      </p>
      <ul className="mt-2 space-y-1 text-sm">
        {p.versions.map((v) => (
          <li key={v.version} className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => setPick(v.version === latest ? null : v.version)}
              className={clsx(
                "w-12 rounded-sm px-1 text-left font-mono text-xs",
                from === v.version && v.version !== latest ? "bg-accent-soft text-ink" : "text-ink-2",
              )}
            >
              v{v.version}
            </button>
            <span className="min-w-0 flex-1 truncate text-ink-2" title={`${v.created_by} · ${v.origin}`}>
              {v.created_by}
              {/^rolled back to /.test(v.origin) ? (
                <span className="ml-2 text-xs text-ink-3">{v.origin.replace(/ by .*$/, "")}</span>
              ) : null}
            </span>
            <span className="text-xs text-ink-3">{relativeTime(v.created_at)}</span>
            {p.can_edit && v.version !== latest ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={rollback.isPending}
                onClick={() => rollback.mutate({ path: { key: p.key }, body: { version: v.version } })}
              >
                Roll back
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
      {rollback.isError ? (
        <div className="mt-2">
          <ErrorState message={problemMessage(rollback.error)} />
        </div>
      ) : null}
      {from !== to && diff.data ? (
        <div className="mt-4 space-y-4">
          <div>
            <h3 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
              {p.key}.yaml · v{from} to v{to}
            </h3>
            <div className="mt-1 overflow-hidden rounded-md border border-line">
              <SideBySide ops={diff.data.yaml} />
            </div>
          </div>
          <div>
            <h3 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
              template · v{from} to v{to}
            </h3>
            <div className="mt-1 overflow-hidden rounded-md border border-line">
              <SideBySide ops={diff.data.template} />
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}
