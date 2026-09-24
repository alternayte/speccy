import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, Download } from "lucide-react";
import { useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorState } from "@/components/ui/states";
import { takeHandoffZip, type SpecDoc } from "@/lib/api";
import { getSpecDocQueryKey, listHandoffsQueryKey } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { upstreamNames } from "./verdict";

// HandoffDialog hands a spec doc to a builder (REQ-136): it downloads the build packet as a
// .zip, and it gives a coding agent the command and the MCP prompt that take the packet
// themselves. Each path records its own handoff.
export function HandoffDialog({
  doc,
  folder,
  cli,
  open,
  onOpenChange,
  onTaken,
}: {
  doc: SpecDoc;
  // folder is the slug of the bundle folder, relative to the served folder.
  folder?: string;
  // cli is true when speccy handoff can read the spec doc: local mode, with the doc on disk.
  cli: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // onTaken runs after the download, so the page can show the new handoff in History.
  onTaken: () => void;
}) {
  const qc = useQueryClient();
  const labelId = useId();
  const [label, setLabel] = useState("");
  const [anyway, setAnyway] = useState(false);
  const why = whyNotReady(doc);
  const trimmed = label.trim();
  const take = useMutation({
    mutationFn: async () => {
      const { data, response } = await takeHandoffZip({
        path: { docId: doc.id },
        body: { label: trimmed || undefined, acknowledged: why ? anyway : undefined },
        parseAs: "blob",
        throwOnError: true,
      });
      return { blob: data, name: fileName(response.headers.get("Content-Disposition")) ?? `${packetName(doc)}.zip` };
    },
    onSuccess: ({ blob, name }) => {
      save(blob, name);
      qc.invalidateQueries({ queryKey: listHandoffsQueryKey({ path: { docId: doc.id } }) });
      qc.invalidateQueries({ queryKey: getSpecDocQueryKey({ path: { docId: doc.id } }) });
      close();
      onTaken();
    },
  });
  const close = () => {
    onOpenChange(false);
    setLabel("");
    setAnyway(false);
    take.reset();
  };

  const acknowledged = !!why && anyway;
  const path = !folder || folder === "." ? doc.path : `${folder}/${doc.path}`;
  // The folder sits beside the served folder: inside it, the scan would read the packet's
  // spec docs as new spec docs.
  const out = `../${baseName(doc.slug)}-build-packet`;
  const command = [
    "speccy handoff",
    shellQuote(path),
    "--out",
    shellQuote(out),
    ...(trimmed ? ["--label", shellQuote(trimmed)] : []),
    ...(acknowledged ? ["--acknowledged"] : []),
  ].join(" ");
  const args = [`bundle ${JSON.stringify(doc.slug)}`, ...(trimmed ? [`label ${JSON.stringify(trimmed)}`] : [])];
  if (acknowledged) args.push("acknowledged true");
  const prompt =
    `Call the Speccy MCP tool handoff_bundle with ${args.join(", ")}. ` +
    `Write the packet to the folder ${out}: each entry of files and links at its path, and handoff_md as HANDOFF.md. ` +
    "Then read HANDOFF.md and build from it.";

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => (o ? onOpenChange(true) : close())}
      title="Hand it to a builder"
      description="The build packet holds the spec doc, its assets, the linked spec docs and HANDOFF.md. Speccy records the version the builder took, and the verdict at that moment."
    >
      <div className="max-h-[64vh] space-y-4 overflow-y-auto">
        {why ? (
          <div className="rounded-md border border-warn/40 bg-warn-soft px-3 py-2.5 text-sm text-ink">
            <p>{why}</p>
            <p className="mt-1 text-xs text-ink-2">
              A builder can take the packet anyway. The handoff records the verdict, and History marks it
              &ldquo;(override)&rdquo;.
            </p>
            <label className="mt-2 flex items-center gap-2 text-sm font-medium">
              <input
                type="checkbox"
                className="accent-[var(--color-accent)]"
                checked={anyway}
                onChange={(e) => setAnyway(e.target.checked)}
              />
              Take it anyway
            </label>
          </div>
        ) : null}

        <div>
          <Label htmlFor={labelId}>Label (optional)</Label>
          <Input
            id={labelId}
            value={label}
            placeholder="A repo, a branch or a ticket"
            onChange={(e) => setLabel(e.target.value)}
          />
          <Button
            variant="primary"
            className="mt-3"
            icon={<Download className="size-4" />}
            disabled={(!!why && !anyway) || take.isPending}
            onClick={() => take.mutate()}
          >
            {take.isPending ? "Preparing the build packet" : "Download the build packet"}
          </Button>
          {take.error ? (
            <div className="mt-3">
              <ErrorState message={problemMessage(take.error)} />
            </div>
          ) : null}
        </div>

        <section className="border-t border-line pt-4">
          <h3 className="text-2xs font-semibold tracking-[var(--tracking-caps)] text-ink-3 uppercase">
            Copy for a coding agent
          </h3>
          <p className="mt-1 text-xs text-ink-2">
            The agent takes the packet itself, and Speccy records that handoff too.
          </p>
          {cli ? (
            <CopyField
              key={command}
              label="Command, run in the folder Speccy serves"
              copyLabel="Copy the command"
              value={command}
            />
          ) : null}
          <CopyField
            key={prompt}
            label="Prompt, for an agent with the Speccy MCP server"
            copyLabel="Copy the prompt"
            value={prompt}
          />
        </section>
      </div>
    </Dialog>
  );
}

// whyNotReady says in words why the verdict does not allow a handoff, or nothing when it is
// Build Ready. The server refuses the same cases.
function whyNotReady(doc: SpecDoc): string | undefined {
  const v = doc.verdict;
  if (!v) return "Speccy has not reviewed this spec doc, so it cannot say it is Build Ready.";
  if (v.result === "build_ready") return undefined;
  if (v.result === "stale") {
    if (v.stale_reason === "upstream_changed") {
      const names = upstreamNames(v);
      return `The verdict is stale: ${names || "a linked spec doc"} changed after the last review. Run the review again.`;
    }
    if (v.version_number !== doc.current_version.number)
      return `The verdict is stale: it is for version ${v.version_number}, and the current version is ${doc.current_version.number}. Run the review again.`;
    return "The verdict is stale: the last review failed. Run the review again.";
  }
  const fix: string[] = [];
  if (v.must > 0) fix.push(`${v.must} MUST finding${v.must === 1 ? "" : "s"} to fix`);
  const blocking = v.blocking_threads ?? 0;
  if (blocking > 0) fix.push(`${blocking} open blocking thread${blocking === 1 ? "" : "s"} to resolve`);
  return fix.length
    ? `This spec doc is Not Build Ready: ${fix.join(" and ")}.`
    : "This spec doc is Not Build Ready. A required link or decision is missing.";
}

function CopyField({ label, copyLabel, value }: { label: string; copyLabel: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-ink-2">{label}</p>
        <Button
          size="sm"
          variant="ghost"
          icon={<Copy className="size-3.5" />}
          aria-label={copied ? "Copied" : copyLabel}
          onClick={async () => {
            await navigator.clipboard?.writeText(value);
            setCopied(true);
          }}
        >
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <pre className="mt-1 rounded-md border border-line bg-sunken px-2.5 py-2 font-mono text-xs break-words whitespace-pre-wrap text-ink">
        {value}
      </pre>
    </div>
  );
}

// packetName is the name the server gives the .zip, for a response that does not name it.
function packetName(doc: SpecDoc): string {
  return `${baseName(doc.slug)}-v${doc.current_version.number}-build-packet`;
}

function baseName(slug: string): string {
  const base = slug.split("/").filter(Boolean).pop();
  return base && base !== "." ? base : "bundle";
}

// fileName reads the file name out of a Content-Disposition header.
function fileName(header: string | null): string | undefined {
  const m = header?.match(/filename="([^"]+)"/);
  return m?.[1];
}

// shellQuote quotes a word for a POSIX shell when it holds more than safe characters.
function shellQuote(s: string): string {
  return /^[\w./@:+-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}

// save hands the browser a file to download.
function save(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
