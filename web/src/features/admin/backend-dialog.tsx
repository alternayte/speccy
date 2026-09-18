import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { clsx } from "clsx";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import type { Backend, BackendInput, BackendKind } from "@/lib/api";
import {
  createBackendMutation,
  listBackendsQueryKey,
  listPresetsOptions,
  updateBackendMutation,
} from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

export const kinds: { kind: BackendKind; label: string; hint: string }[] = [
  { kind: "anthropic", label: "Anthropic", hint: "Claude models with an API key." },
  { kind: "openai", label: "OpenAI", hint: "OpenAI models with an API key." },
  { kind: "openrouter", label: "OpenRouter", hint: "Many providers through one key." },
  { kind: "deepseek", label: "DeepSeek", hint: "DeepSeek models with an API key." },
  { kind: "agent_cli", label: "Agent CLI", hint: "A CLI on this machine, such as Claude Code, on your subscription." },
];

// BackendDialog adds or edits a backend. A stored key is never shown; leave it empty to keep it.
export function BackendDialog({
  open,
  onOpenChange,
  editing,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editing?: Backend;
}) {
  const qc = useQueryClient();
  const presets = useQuery({ ...listPresetsOptions(), enabled: open });
  const [kind, setKind] = useState<BackendKind>(editing?.kind ?? "anthropic");
  const [name, setName] = useState(editing?.name ?? "");
  const [secret, setSecret] = useState("");
  const [baseUrl, setBaseUrl] = useState(editing?.base_url ?? "");
  const [preset, setPreset] = useState(editing?.preset ?? "claude");
  const [command, setCommand] = useState((editing?.command ?? []).join("\n"));
  const [promptVia, setPromptVia] = useState<"stdin" | "file">((editing?.prompt_via as "stdin" | "file") ?? "stdin");
  const done = () => {
    qc.invalidateQueries({ queryKey: listBackendsQueryKey() });
    onOpenChange(false);
  };
  const create = useMutation({ ...createBackendMutation(), onSuccess: done });
  const update = useMutation({ ...updateBackendMutation(), onSuccess: done });
  const saving = create.isPending || update.isPending;
  const error = create.error ?? update.error;

  const body: BackendInput = {
    kind,
    name: name.trim() || kinds.find((k) => k.kind === kind)!.label,
    ...(secret.trim() ? { secret: secret.trim() } : {}),
    ...(baseUrl.trim() ? { base_url: baseUrl.trim() } : {}),
    ...(kind === "agent_cli"
      ? {
          preset,
          ...(preset === "custom"
            ? {
                command: command.split("\n").filter((l, i, all) => l !== "" || i < all.length - 1),
                prompt_via: promptVia,
              }
            : {}),
        }
      : {}),
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? `Edit ${editing.name}` : "Add a backend"}
      description="A backend is a model service. Assign it to review roles below."
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (editing) update.mutate({ path: { backendId: editing.id }, body });
          else create.mutate({ body });
        }}
      >
        {!editing ? (
          <fieldset className="min-w-0">
            <legend className="mb-1 block text-xs font-medium text-ink-2">Kind</legend>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              {kinds.map((k) => (
                <label
                  key={k.kind}
                  title={k.hint}
                  className={clsx(
                    "flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm transition-colors",
                    kind === k.kind ? "border-accent bg-accent-soft" : "border-line hover:bg-sunken",
                  )}
                >
                  <input
                    type="radio"
                    name="kind"
                    checked={kind === k.kind}
                    onChange={() => setKind(k.kind)}
                    className="accent-[var(--accent)]"
                  />
                  {k.label}
                </label>
              ))}
            </div>
            <p className="mt-1 text-xs text-ink-3">{kinds.find((k) => k.kind === kind)!.hint}</p>
          </fieldset>
        ) : null}

        <div>
          <Label htmlFor="be-name">Name</Label>
          <Input
            id="be-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={kinds.find((k) => k.kind === kind)!.label}
          />
        </div>

        {kind !== "agent_cli" ? (
          <>
            <div>
              <Label htmlFor="be-key">API key</Label>
              <Input
                id="be-key"
                type="password"
                autoComplete="off"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder={
                  editing?.has_secret ? `Stored, ends in ${editing.secret_last4}. Leave empty to keep it.` : ""
                }
              />
              <p className="mt-1 text-xs text-ink-3">Speccy encrypts the key and never shows it again.</p>
            </div>
            <div>
              <Label htmlFor="be-url">Base URL (optional)</Label>
              <Input
                id="be-url"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="The provider's default"
              />
            </div>
          </>
        ) : (
          <fieldset className="min-w-0 space-y-2">
            <legend className="mb-1 block text-xs font-medium text-ink-2">CLI</legend>
            {[...(presets.data?.items ?? []), null].map((p) => {
              const value = p?.name ?? "custom";
              return (
                <label
                  key={value}
                  className={clsx(
                    "flex min-w-0 cursor-pointer items-start gap-2 overflow-hidden rounded-md border px-3 py-2 transition-colors",
                    preset === value ? "border-accent bg-accent-soft" : "border-line hover:bg-sunken",
                  )}
                >
                  <input
                    type="radio"
                    name="preset"
                    checked={preset === value}
                    onChange={() => setPreset(value)}
                    className="mt-1 accent-[var(--accent)]"
                  />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2 text-sm font-medium text-ink">
                      {value}
                      {p ? (
                        <span className={clsx("text-2xs font-normal", p.installed ? "text-ok" : "text-ink-3")}>
                          {p.installed ? "installed" : "not found on PATH"}
                        </span>
                      ) : null}
                    </span>
                    <span className="block truncate font-mono text-2xs text-ink-3" title={p ? p.verified : undefined}>
                      {p ? p.command.map((a) => (a === "" ? '""' : a)).join(" ") : "Your own command"}
                    </span>
                  </span>
                </label>
              );
            })}
            {preset === "custom" ? (
              <div className="space-y-2">
                <div>
                  <Label htmlFor="be-cmd">Command, one argument per line</Label>
                  <Textarea
                    id="be-cmd"
                    rows={5}
                    value={command}
                    onChange={(e) => setCommand(e.target.value)}
                    placeholder={"my-agent\n--json\n--model\n{model}"}
                  />
                  <p className="mt-1 text-xs text-ink-3">
                    Speccy replaces {"{model}"}, {"{schema}"}, and {"{prompt_file}"}. The CLI must print the JSON
                    answer.
                  </p>
                </div>
                <div className="flex gap-4 text-sm">
                  {(["stdin", "file"] as const).map((v) => (
                    <label key={v} className="flex items-center gap-1.5">
                      <input
                        type="radio"
                        name="via"
                        checked={promptVia === v}
                        onChange={() => setPromptVia(v)}
                        className="accent-[var(--accent)]"
                      />
                      {v === "stdin" ? "Prompt on stdin" : "Prompt in prompt.md"}
                    </label>
                  ))}
                </div>
              </div>
            ) : null}
          </fieldset>
        )}

        {error ? <ErrorState message={problemMessage(error)} /> : null}
        <div className="flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={saving}>
            {saving ? "Saving" : editing ? "Save" : "Add"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
