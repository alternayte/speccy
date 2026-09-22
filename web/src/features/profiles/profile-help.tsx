import { Info } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";

// key is one top-level key of the profile schema, with what it decides. profileKeys is checked
// against the schema by a test, so a new key cannot ship with nothing said about it.
const keys: { key: string; text: string }[] = [
  { key: "key", text: "The doc type a frontmatter type: selects." },
  { key: "name", text: "The label people read." },
  { key: "template", text: "The template path. Its required markers become the required headings." },
  { key: "limits", text: "Words in a doc, in a section and in a sentence; lines in a code block; rows in a table." },
  { key: "links", text: "Which doc types this one links up to, and which bundles a large doc must cover." },
  { key: "trace", text: "The trace ID prefixes this doc uses, and the upstream prefixes it must reference." },
  { key: "verify", text: "The post-build gate: the trace IDs it verifies, and the bounds of its repo scan." },
  { key: "waivers", text: "Who approves a waiver of a SHOULD, and of a MUST." },
  { key: "approvals", text: "How many human approvals a bundle needs." },
  { key: "divergence", text: "How many readers answer the build questions, how many questions, and their themes." },
  {
    key: "grounding",
    text: "Which domains a source may come from, their tier and freshness, and a section's claim class.",
  },
  { key: "lint", text: "The level of a lint rule, or off, and extra slop phrases." },
  { key: "checks", text: "The rubric: each check's slug, level, stage and question." },
];

/** profileKeys is the list the panel explains. A test compares it with the profile schema. */
export const profileKeys = keys.map((k) => k.key);

// ProfileHelp explains the profile and its template where a maintainer edits them. The long
// form is docs/profiles.md; this says enough to make the next change without leaving the page.
export function ProfileHelp() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" onClick={() => setOpen(true)} aria-label="What a profile holds">
        <Info aria-hidden className="size-3.5" /> Help
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        wide
        title="What a profile holds"
        description="A profile is the configuration of one doc type: its template, its checks, its limits and its policies."
      >
        <div className="max-h-[70vh] space-y-5 overflow-y-auto pr-1 text-sm">
          <section>
            <h3 className="font-semibold text-ink">The template marks the required headings</h3>
            <p className="mt-1 text-ink-2">
              A heading that ends with <code className="font-mono text-xs">{"<!-- required -->"}</code> is required at
              every size. A heading that names a size, such as{" "}
              <code className="font-mono text-xs">{"<!-- required: app -->"}</code>, is required at that size and
              larger. A feature doc does not need it.
            </p>
            <pre className="mt-2 overflow-x-auto rounded-md border border-line bg-sunken p-2 font-mono text-xs">
              {"## Decisions <!-- required -->\n## Data model <!-- required: app -->"}
            </pre>
          </section>

          <section>
            <h3 className="font-semibold text-ink">Size decides what a doc must answer</h3>
            <p className="mt-1 text-ink-2">
              A doc declares <code className="font-mono text-xs">size:</code> in its frontmatter:{" "}
              <strong>feature</strong> for one change a team ships, <strong>app</strong> for a system with parts that
              call each other, <strong>initiative</strong> for work several systems share. Size is not importance and
              not effort: it is how much of the world the doc has to describe. It decides which headings the template
              requires, and which checks run.
            </p>
          </section>

          <section>
            <h3 className="font-semibold text-ink">A check</h3>
            <p className="mt-1 text-ink-2">
              <strong>level</strong> decides whether a failure stops the verdict: MUST blocks, SHOULD does not, INFO
              never does. <strong>stage</strong> decides when it runs: rubric, grounding, divergence or coherence.{" "}
              <strong>sizes</strong> limits it to the doc sizes you name; empty means every size.
            </p>
          </section>

          <section>
            <h3 className="font-semibold text-ink">Every key</h3>
            <dl className="mt-1 space-y-1">
              {keys.map((k) => (
                <div key={k.key} className="flex gap-2">
                  <dt className="w-24 shrink-0 font-mono text-xs text-ink">{k.key}</dt>
                  <dd className="min-w-0 flex-1 text-ink-2">{k.text}</dd>
                </div>
              ))}
            </dl>
          </section>

          <section>
            <h3 className="font-semibold text-ink">Three changes people usually want</h3>
            <ul className="mt-1 list-disc space-y-1 pl-5 text-ink-2">
              <li>
                A section only large docs need: mark it{" "}
                <code className="font-mono text-xs">{"<!-- required: app -->"}</code> in the template, not in the
                checks.
              </li>
              <li>
                A check that fires on every doc of a repo you just adopted: relax it while the repo catches up, with{" "}
                <code className="font-mono text-xs">adoption.relaxed</code> in{" "}
                <code className="font-mono text-xs">.speccy.yaml</code>. It still reports, and it does not block.
              </li>
              <li>
                A limit people waive again and again: that limit is wrong. Change the limit instead of approving the
                same waiver.
              </li>
            </ul>
          </section>

          <p className="text-ink-3">
            Every save is a new version, and an earlier review keeps the version it used. Versions below diffs any two
            and rolls an old one forward. The long form is <code className="font-mono">docs/profiles.md</code>.
          </p>
        </div>
      </Dialog>
    </>
  );
}
