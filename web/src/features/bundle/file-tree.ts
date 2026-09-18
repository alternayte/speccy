export type TreeNode =
  { kind: "folder"; name: string; path: string; children: TreeNode[] } | { kind: "file"; name: string; path: string };

// buildTree turns sorted file paths into folders and files. Folders sort before files.
export function buildTree(paths: string[]): TreeNode[] {
  const root: TreeNode[] = [];
  const folders = new Map<string, TreeNode[]>([["", root]]);
  for (const p of paths) {
    const parts = p.split("/");
    let dir = "";
    for (let i = 0; i < parts.length - 1; i++) {
      const next = dir ? `${dir}/${parts[i]}` : parts[i]!;
      if (!folders.has(next)) {
        const children: TreeNode[] = [];
        folders.get(dir)!.push({ kind: "folder", name: parts[i]!, path: next, children });
        folders.set(next, children);
      }
      dir = next;
    }
    folders.get(dir)!.push({ kind: "file", name: parts[parts.length - 1]!, path: p });
  }
  const sort = (nodes: TreeNode[]) => {
    nodes.sort((a, b) => (a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === "folder" ? -1 : 1));
    nodes.forEach((n) => n.kind === "folder" && sort(n.children));
  };
  sort(root);
  return root;
}

const imageExt = /\.(png|jpe?g|gif|webp|svg|avif)$/i;

// markdownLink returns a link to path from the main doc, which is at the bundle root.
export function markdownLink(path: string): string {
  const name = path.split("/").pop() ?? path;
  const target = path.split("/").map(encodeURIComponent).join("/");
  return imageExt.test(path) ? `![${name}](${target})` : `[${name}](${target})`;
}
