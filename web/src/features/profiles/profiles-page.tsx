import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Empty, ErrorState, Loading } from "@/components/ui/states";
import { useMe } from "@/features/account/me";
import { createProfileMutation, getProfileOptions, listProfilesOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// ProfilesPage lists the doc types (REQ-010).
export function ProfilesPage() {
  const profiles = useQuery(listProfilesOptions());
  const admin = useMe().data?.role === "admin";
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[760px] px-4 py-8 sm:px-6">
        <div className="flex items-start gap-4">
          <div className="min-w-0 flex-1">
            <h1 className="text-xl font-semibold tracking-tight">Profiles</h1>
            <p className="mt-1 text-sm text-ink-2">
              A profile sets the template, the checks, and the limits of one doc type. Anyone can suggest a change.
            </p>
          </div>
          {admin ? <NewProfile keys={profiles.data?.items.map((p) => p.key) ?? []} /> : null}
        </div>
        <div className="mt-6 overflow-hidden rounded-lg border border-line bg-surface">
          {profiles.isPending ? (
            <Loading label="Loading profiles" />
          ) : profiles.isError ? (
            <div className="p-3">
              <ErrorState message={problemMessage(profiles.error)} />
            </div>
          ) : profiles.data.items.length === 0 ? (
            <Empty title="No profiles" />
          ) : (
            <ul className="divide-y divide-line">
              {profiles.data.items.map((p) => (
                <li key={p.key}>
                  <Link
                    to="/profiles/$key"
                    params={{ key: p.key }}
                    className="flex items-baseline gap-3 px-4 py-3 hover:bg-sunken"
                  >
                    <span className="font-medium">{p.name}</span>
                    <span className="font-mono text-xs text-ink-3">{p.key}</span>
                    <span className="ml-auto text-xs text-ink-3">
                      v{p.version} · {p.origin}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {profiles.data?.problems.length ? (
            <div className="border-t border-line p-3">
              {profiles.data.problems.map((pr) => (
                <ErrorState key={pr} message={pr} />
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

// NewProfile makes a doc type. Start from names the profile whose YAML and template prefill the
// form, so a team that wants a near copy does not retype one.
function NewProfile({ keys }: { keys: string[] }) {
  const go = useNavigate();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [from, setFrom] = useState("");
  const source = useQuery({ ...getProfileOptions({ path: { key: from } }), enabled: open && from !== "" });
  const create = useMutation({
    ...createProfileMutation(),
    onSuccess: (p) => {
      void qc.invalidateQueries({ queryKey: listProfilesOptions().queryKey });
      setOpen(false);
      void go({ to: "/profiles/$key", params: { key: p.key } });
    },
  });
  const starter = source.data;
  return (
    <>
      <Button onClick={() => setOpen(true)}>New profile</Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title="New profile"
        description="A key names the doc type. Start from an existing profile to begin with its YAML and template."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              disabled={!key || !name || create.isPending || (from !== "" && !starter)}
              onClick={() =>
                create.mutate({
                  body: {
                    key,
                    yaml: starter
                      ? starter.yaml.replace(/^key:.*$/m, `key: ${key}`).replace(/^name:.*$/m, `name: ${name}`)
                      : newYAML(key, name),
                    template: starter ? starter.template : "# <Title>\n",
                  },
                })
              }
            >
              Create
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <label className="block text-sm">
            <span className="text-ink-2">Key</span>
            <Input className="mt-1" value={key} onChange={(e) => setKey(e.target.value)} placeholder="rfc" />
          </label>
          <label className="block text-sm">
            <span className="text-ink-2">Name</span>
            <Input
              className="mt-1"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Request for comments"
            />
          </label>
          <label className="block text-sm">
            <span className="text-ink-2">Start from</span>
            <select
              className="mt-1 h-8 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-ink"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            >
              <option value="">Nothing: an empty profile</option>
              {keys.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          {create.isError ? <ErrorState message={problemMessage(create.error)} /> : null}
        </div>
      </Dialog>
    </>
  );
}

// newYAML is the smallest profile that loads: a key, a name, a template and no checks.
function newYAML(key: string, name: string) {
  return `key: ${key}\nname: ${name}\ntemplate: templates/${key}.md\nchecks: []\n`;
}
