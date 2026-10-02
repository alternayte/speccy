import { useQuery } from "@tanstack/react-query";
import { clsx } from "clsx";
import { Check, ChevronsUpDown } from "lucide-react";
import { Popover } from "radix-ui";
import { useState } from "react";
import { Input } from "@/components/ui/input";
import type { Backend, BackendModel } from "@/lib/api";
import { listBackendModelsOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// shown is how many matches the list renders. OpenRouter has several hundred models, and the
// search field narrows them.
const shown = 100;

// ModelPicker picks the model of a role. For an API backend it reads the model list live from
// the backend, through the server, which holds the key, and a search field narrows the list.
// An agent CLI has no models endpoint, so it keeps the free-text field. When the list does not
// load, the picker shows the cause and the free-text field, so an outage does not stop the
// setup. A model that is assigned and is no longer in the list stays, with a note.
export function ModelPicker({
  backend,
  role,
  value,
  onChange,
  onPick,
}: {
  backend?: Backend;
  role: string;
  value: string;
  onChange: (model: string) => void;
  // onPick gets the model a person picked from the list, with its prices when it has them.
  onPick: (model: BackendModel) => void;
}) {
  const listed = !!backend && backend.kind !== "agent_cli";
  const models = useQuery({
    ...listBackendModelsOptions({ path: { backendId: backend?.id ?? "" } }),
    enabled: listed,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const freeText = (
    <Input
      aria-label={`Model for ${role}`}
      className="w-52"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder="Model"
    />
  );
  if (!listed) return freeText;
  if (models.isError)
    return (
      <div className="flex basis-full flex-wrap items-center gap-2">
        {freeText}
        <p role="alert" className="min-w-0 flex-1 text-xs text-warn">
          {problemMessage(models.error)}
        </p>
      </div>
    );
  const items = models.data?.items ?? [];
  const words = search.toLowerCase().split(/\s+/).filter(Boolean);
  const matches = items.filter((m) => {
    const text = `${m.id} ${m.name ?? ""}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
  const gone = !!value && !models.isPending && !items.some((m) => m.id === value);
  return (
    <>
      <Popover.Root
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setSearch("");
        }}
      >
        <Popover.Trigger asChild>
          <button
            type="button"
            role="combobox"
            aria-expanded={open}
            aria-label={`Model for ${role}`}
            disabled={models.isPending}
            className="flex h-8 w-64 items-center justify-between gap-2 rounded-md border border-line-strong bg-surface px-2.5 text-left text-sm text-ink focus:border-focus focus:outline-none disabled:text-ink-3"
          >
            <span className={clsx("min-w-0 truncate", value ? "font-mono text-xs" : "text-ink-3")}>
              {models.isPending ? "Loading the models" : value || "Pick a model"}
            </span>
            <ChevronsUpDown aria-hidden className="size-3.5 shrink-0 text-ink-3" />
          </button>
        </Popover.Trigger>
        <Popover.Portal>
          <Popover.Content
            align="start"
            sideOffset={4}
            className="z-50 w-[min(26rem,calc(100vw-2rem))] rounded-md border border-line bg-surface p-1.5 shadow-pop"
          >
            <Input
              aria-label={`Search the models of ${backend.name}`}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={`Search ${items.length} models`}
              autoFocus
            />
            <ul role="listbox" aria-label={`Models of ${backend.name}`} className="mt-1.5 max-h-72 overflow-y-auto">
              {matches.slice(0, shown).map((m) => (
                <li key={m.id} role="option" aria-selected={m.id === value}>
                  <button
                    type="button"
                    onClick={() => {
                      onPick(m);
                      setOpen(false);
                      setSearch("");
                    }}
                    className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left hover:bg-sunken focus:bg-sunken focus:outline-none"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-xs text-ink">{m.id}</span>
                      {m.name ? <span className="block truncate text-2xs text-ink-3">{m.name}</span> : null}
                    </span>
                    {m.price_in_per_mtok != null && m.price_out_per_mtok != null ? (
                      <span className="shrink-0 font-mono text-2xs text-ink-3">
                        ${m.price_in_per_mtok} / ${m.price_out_per_mtok}
                      </span>
                    ) : null}
                    {m.id === value ? <Check aria-hidden className="size-3.5 shrink-0 text-accent" /> : null}
                  </button>
                </li>
              ))}
            </ul>
            <p className="px-2 pt-1.5 text-2xs text-ink-3">
              {matches.length === 0
                ? "No model matches."
                : matches.length > shown
                  ? `The first ${shown} of ${matches.length} models. Type to narrow the list.`
                  : `${matches.length} model${matches.length === 1 ? "" : "s"}.`}
            </p>
          </Popover.Content>
        </Popover.Portal>
      </Popover.Root>
      {gone ? (
        <p className="order-last basis-full text-xs text-warn">
          {value} is not in the model list of {backend.name} now. It stays assigned until you pick another model.
        </p>
      ) : null}
    </>
  );
}
