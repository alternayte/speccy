import { useQuery } from "@tanstack/react-query";
import {
  Bold,
  Braces,
  CheckSquare,
  Code,
  FileOutput,
  Hash,
  Heading,
  Italic,
  Link2,
  List,
  ListOrdered,
  Quote,
  Table,
  Tag,
} from "lucide-react";
import { useState } from "react";
import { Menu, MenuItem } from "@/components/ui/menu";
import { getProfileOptions } from "@/lib/api/@tanstack/react-query.gen";

// The control bar writes markdown into the block the person is editing, and applies the fixes
// that the profile knows about (SDD §6.2). Every control is an edit: the person undoes it and
// saves it like anything they type.

export type Target = {
  // value is the markdown of the open block, and selection is what the person marked in it.
  value: string;
  start: number;
  end: number;
  // apply replaces the block's text and puts the caret where the control asks.
  apply: (value: string, caret: number) => void;
};

export function ControlBar({
  target,
  profileKey,
  markdown,
  onInsertSection,
}: {
  target: Target | null;
  profileKey: string;
  markdown: string;
  // onInsertSection writes the whole file: a missing heading goes where the template puts it,
  // which is not always inside the open block.
  onInsertSection: (markdown: string, heading: string) => void;
}) {
  const profile = useQuery(getProfileOptions({ path: { key: profileKey } }));
  const missing = missingHeadings(profile.data?.template ?? "", markdown);
  const nextId = nextTraceId(markdown);

  return (
    <div className="no-print flex flex-wrap items-center gap-1 border-b border-line bg-surface px-3 py-1.5">
      <Group>
        <Control
          title="Heading"
          onClick={() => target && line(target, "## ")}
          icon={<Heading className="size-3.5" />}
        />
        <Control title="Bold" onClick={() => target && wrap(target, "**")} icon={<Bold className="size-3.5" />} />
        <Control title="Italic" onClick={() => target && wrap(target, "*")} icon={<Italic className="size-3.5" />} />
        <Control title="Inline code" onClick={() => target && wrap(target, "`")} icon={<Code className="size-3.5" />} />
        <Control title="Link" onClick={() => target && link(target)} icon={<Link2 className="size-3.5" />} />
      </Group>
      <Rule />
      <Group>
        <Control
          title="Bullet list"
          onClick={() => target && line(target, "- ")}
          icon={<List className="size-3.5" />}
        />
        <Control
          title="Numbered list"
          onClick={() => target && line(target, "1. ")}
          icon={<ListOrdered className="size-3.5" />}
        />
        <Control
          title="Task"
          onClick={() => target && line(target, "- [ ] ")}
          icon={<CheckSquare className="size-3.5" />}
        />
        <Control title="Quote" onClick={() => target && line(target, "> ")} icon={<Quote className="size-3.5" />} />
        <Control
          title="Code block"
          onClick={() => target && insert(target, "```\n", "\n```")}
          icon={<Braces className="size-3.5" />}
        />
        <TablePicker target={target} />
      </Group>
      <Rule />
      <Group>
        <Menu
          trigger={
            <button
              type="button"
              title="Insert a heading that the profile requires"
              disabled={missing.length === 0}
              className="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-sunken hover:text-ink disabled:opacity-40"
            >
              <Hash aria-hidden className="size-3.5" />
              Missing heading
              {missing.length > 0 ? <span className="text-ink-3">{missing.length}</span> : null}
            </button>
          }
        >
          {missing.map((h) => (
            <MenuItem
              key={h}
              onSelect={() => onInsertSection(insertHeading(markdown, profile.data?.template ?? "", h), h)}
            >
              {h}
            </MenuItem>
          ))}
        </Menu>
        <Control
          title={`Insert the next trace ID (${nextId})`}
          onClick={() => target && insert(target, nextId + " ", "")}
          icon={<Tag className="size-3.5" />}
          label={nextId}
        />
        <Control
          title="Insert a requirement with an acceptance criterion"
          onClick={() => target && requirement(target, nextId)}
          icon={<Tag className="size-3.5" />}
          label="Requirement"
        />
        <Control
          title="Move this block to assets/ and leave a link"
          onClick={() => target && moveToAsset(target)}
          icon={<FileOutput className="size-3.5" />}
          label="To assets"
        />
      </Group>
      {target ? null : <span className="ml-1 text-2xs text-ink-3">Click text in the preview to edit it.</span>}
    </div>
  );
}

function Group({ children }: { children: React.ReactNode }) {
  return <div className="flex items-center gap-0.5">{children}</div>;
}

function Rule() {
  return <span aria-hidden className="mx-1 h-4 w-px bg-line" />;
}

function Control({
  title,
  onClick,
  icon,
  label,
}: {
  title: string;
  onClick: () => void;
  icon: React.ReactNode;
  label?: string;
}) {
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      // The bar acts on the open block, so it must not take the focus from it.
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      className="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-sunken hover:text-ink"
    >
      {icon}
      {label ? <span className="font-mono">{label}</span> : null}
    </button>
  );
}

// TablePicker draws the grid that the person sweeps to choose the size, as a table dialog would
// ask for two numbers and a click.
function TablePicker({ target }: { target: Target | null }) {
  const [size, setSize] = useState<[number, number]>([0, 0]);
  const [open, setOpen] = useState(false);
  return (
    <div className="relative">
      <Control title="Table" onClick={() => setOpen(!open)} icon={<Table className="size-3.5" />} />
      {open ? (
        <div
          className="absolute top-8 left-0 z-20 rounded-md border border-line bg-surface p-2 shadow-pop"
          onMouseLeave={() => setSize([0, 0])}
        >
          <div className="grid grid-cols-8 gap-0.5">
            {Array.from({ length: 64 }, (_, i) => {
              const r = Math.floor(i / 8) + 1;
              const c = (i % 8) + 1;
              const on = r <= size[0] && c <= size[1];
              return (
                <button
                  key={i}
                  type="button"
                  aria-label={`${r} by ${c}`}
                  onMouseEnter={() => setSize([r, c])}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => {
                    if (target) insert(target, table(r, c), "");
                    setOpen(false);
                  }}
                  className={`size-3 rounded-[2px] border ${on ? "border-accent bg-accent-soft" : "border-line"}`}
                />
              );
            })}
          </div>
          <p className="mt-1 text-center text-2xs text-ink-3">
            {size[0] || 0} × {size[1] || 0}
          </p>
        </div>
      ) : null}
    </div>
  );
}

function table(rows: number, cols: number): string {
  const row = (cells: string[]) => `| ${cells.join(" | ")} |`;
  const head = row(Array.from({ length: cols }, (_, i) => `Column ${i + 1}`));
  const rule = row(Array.from({ length: cols }, () => "---"));
  const body = Array.from({ length: rows }, () => row(Array.from({ length: cols }, () => " ")));
  return [head, rule, ...body].join("\n");
}

// wrap puts marks around the selection, or around the word the caret sits in.
function wrap(t: Target, mark: string) {
  const [s, e] = word(t);
  const next = t.value.slice(0, s) + mark + t.value.slice(s, e) + mark + t.value.slice(e);
  t.apply(next, e + mark.length * 2);
}

// line puts a prefix on the line the caret sits in, and takes it off again when it is there.
function line(t: Target, prefix: string) {
  const s = t.value.lastIndexOf("\n", Math.max(0, t.start - 1)) + 1;
  const rest = t.value.slice(s);
  const has = rest.startsWith(prefix);
  const next = t.value.slice(0, s) + (has ? rest.slice(prefix.length) : prefix + rest);
  t.apply(next, t.start + (has ? -prefix.length : prefix.length));
}

function insert(t: Target, before: string, after: string) {
  const next = t.value.slice(0, t.start) + before + t.value.slice(t.start, t.end) + after + t.value.slice(t.end);
  t.apply(next, t.start + before.length);
}

function link(t: Target) {
  const [s, e] = word(t);
  const text = t.value.slice(s, e) || "text";
  const next = `${t.value.slice(0, s)}[${text}](url)${t.value.slice(e)}`;
  t.apply(next, s + text.length + 3);
}

function requirement(t: Target, id: string) {
  const text = `- **${id}** The system MUST … Acceptance: a tester checks …`;
  insert(t, text, "");
}

// moveToAsset is the fix for lint.asset-nudge: the block goes to assets/ and a link stays.
function moveToAsset(t: Target) {
  const name = `assets/${slugOf(t.value)}.md`;
  t.apply(`See [${slugOf(t.value)}](${name}).`, 0);
  // The block's text goes to the clipboard, so the person pastes it into the new file. Speccy
  // writes one file per save, and a second file needs a second save.
  navigator.clipboard?.writeText(t.value).catch(() => undefined);
}

function slugOf(text: string): string {
  const first = text
    .replace(/[#>*`|]/g, " ")
    .trim()
    .split(/\s+/)
    .slice(0, 4)
    .join("-")
    .toLowerCase();
  return first.replace(/[^a-z0-9-]/g, "") || "block";
}

// word is the selection, or the word under the caret when nothing is selected.
function word(t: Target): [number, number] {
  if (t.end > t.start) return [t.start, t.end];
  let s = t.start;
  let e = t.start;
  while (s > 0 && /\S/.test(t.value[s - 1]!)) s--;
  while (e < t.value.length && /\S/.test(t.value[e]!)) e++;
  return [s, e];
}

// missingHeadings are the headings that the template marks with <!-- required --> and the doc
// does not have. The match ignores a section number, as lint.required-headings does, so
// "## 5. Decisions" counts as "Decisions".
export function missingHeadings(template: string, markdown: string): string[] {
  const have = new Set(headings(markdown).map(normalize));
  return headings(template)
    .filter(required)
    .map(strip)
    .filter((h) => !have.has(normalize(h)));
}

const requiredMark = /<!--\s*required\s*-->\s*$/;

function required(heading: string): boolean {
  return requiredMark.test(heading);
}

function strip(heading: string): string {
  return heading.replace(requiredMark, "").trimEnd();
}

function headings(md: string): string[] {
  return md
    .split("\n")
    .filter((l) => /^#{1,6}\s/.test(l))
    .map((l) => l.trim());
}

function normalize(h: string): string {
  return h
    .replace(/^#+\s*/, "")
    .replace(/^\d+(\.\d+)*\.?\s*/, "")
    .trim()
    .toLowerCase();
}

// insertHeading puts the heading where the template has it: after the heading that comes before
// it in the template and exists in the doc, or at the end.
export function insertHeading(markdown: string, template: string, heading: string): string {
  const order = headings(template).map(strip);
  const at = order.indexOf(heading);
  const lines = markdown.split("\n");
  for (let i = at - 1; i >= 0; i--) {
    const before = normalize(order[i]!);
    const found = lines.findIndex((l) => /^#{1,6}\s/.test(l) && normalize(l) === before);
    if (found >= 0) {
      const next = lines.findIndex((l, j) => j > found && /^#{1,6}\s/.test(l));
      const cut = next < 0 ? lines.length : next;
      return [...lines.slice(0, cut), heading, "", ...lines.slice(cut)].join("\n");
    }
  }
  return `${markdown.replace(/\s*$/, "")}\n\n${heading}\n\n`;
}

// nextTraceId is the next free ID of the prefix the doc uses most.
export function nextTraceId(markdown: string): string {
  const counts = new Map<string, number[]>();
  for (const m of markdown.matchAll(/\b([A-Z]{2,6})-(\d{1,5})\b/g)) {
    const list = counts.get(m[1]!) ?? [];
    list.push(Number(m[2]));
    counts.set(m[1]!, list);
  }
  let best = "REQ";
  let most = 0;
  counts.forEach((list, prefix) => {
    if (list.length > most) {
      most = list.length;
      best = prefix;
    }
  });
  const used = counts.get(best) ?? [];
  const width = used.length > 0 ? String(Math.max(...used)).length : 3;
  return `${best}-${String(Math.max(0, ...used) + 1).padStart(width, "0")}`;
}
