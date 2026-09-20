// A drop from Finder or Explorer carries files, folders, or both. The browser gives a folder
// only through the entries API, so Speccy walks it and keeps each file's path inside the folder.

export type Dropped = { path: string; file: File };

// filesFromDrop reads everything the person dropped, folders included.
export async function filesFromDrop(dt: DataTransfer): Promise<Dropped[]> {
  const entries = Array.from(dt.items)
    .filter((i) => i.kind === "file")
    .map((i) => (i as DataTransferItem & { webkitGetAsEntry?: () => FileSystemEntry | null }).webkitGetAsEntry?.())
    .filter((e): e is FileSystemEntry => !!e);
  if (entries.length === 0) {
    return Array.from(dt.files).map((file) => ({ path: file.name, file }));
  }
  const out: Dropped[] = [];
  for (const entry of entries) await walk(entry, "", out);
  out.sort((a, b) => a.path.localeCompare(b.path));
  return out;
}

async function walk(entry: FileSystemEntry, prefix: string, out: Dropped[]): Promise<void> {
  if (entry.isFile) {
    const file = await new Promise<File | null>((resolve) =>
      (entry as FileSystemFileEntry).file(resolve, () => resolve(null)),
    );
    // A folder holds files Speccy never shows, such as .DS_Store.
    if (file && !entry.name.startsWith(".")) out.push({ path: prefix + entry.name, file });
    return;
  }
  if (!entry.isDirectory || entry.name.startsWith(".")) return;
  const reader = (entry as FileSystemDirectoryEntry).createReader();
  for (;;) {
    const batch = await new Promise<FileSystemEntry[]>((resolve) => reader.readEntries(resolve, () => resolve([])));
    if (batch.length === 0) return;
    for (const child of batch) await walk(child, `${prefix + entry.name}/`, out);
  }
}

// Bundle is one bundle to make from a drop on the bundles list: a folder, or one loose file.
export type DroppedBundle = { name: string; files: Dropped[] };

// bundlesFromDrop groups a drop: one bundle per folder, and one per loose markdown or .zip file.
export function bundlesFromDrop(items: Dropped[]): DroppedBundle[] {
  const folders = new Map<string, Dropped[]>();
  const loose: DroppedBundle[] = [];
  for (const item of items) {
    const slash = item.path.indexOf("/");
    if (slash < 0) {
      if (isMarkdown(item.path) || isZip(item.path)) loose.push({ name: stem(item.path), files: [item] });
      continue;
    }
    const folder = item.path.slice(0, slash);
    const rest = item.path.slice(slash + 1);
    folders.set(folder, [...(folders.get(folder) ?? []), { path: rest, file: item.file }]);
  }
  const out: DroppedBundle[] = [];
  folders.forEach((files, name) => out.push({ name, files }));
  return [...out, ...loose];
}

// mainDocOf is the markdown file that becomes the bundle's main doc: the one at the top of the
// folder, and README last, as a README is usually about the folder and not the spec.
export function mainDocOf(files: Dropped[]): Dropped | undefined {
  const md = files.filter((f) => isMarkdown(f.path));
  const shallow = md.filter((f) => !f.path.includes("/"));
  const pick = shallow.length > 0 ? shallow : md;
  return pick.find((f) => !/^readme\./i.test(baseName(f.path))) ?? pick[0];
}

export function isMarkdown(path: string): boolean {
  return /\.(md|markdown)$/i.test(path);
}

export function isZip(path: string): boolean {
  return /\.zip$/i.test(path);
}

// stem is the file name without its extension: the folder name a dropped file gets.
export function stem(path: string): string {
  const name = baseName(path);
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(0, dot) : name;
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

// keepBothName adds a number to a name that a bundle already holds: "diagram (2).png".
export function keepBothName(path: string, taken: Set<string>): string {
  const dot = path.lastIndexOf(".");
  const stem = dot > path.lastIndexOf("/") ? path.slice(0, dot) : path;
  const ext = dot > path.lastIndexOf("/") ? path.slice(dot) : "";
  for (let n = 2; ; n++) {
    const next = `${stem} (${n})${ext}`;
    if (!taken.has(next)) return next;
  }
}
