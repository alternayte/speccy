import type { View } from "@/features/editor/editor-pane";

// waiver is the waiver an inbox link opens: the rail selects the finding it excuses, and the
// preview focuses the section it covers.
// at is a range "start-end" of the file that a link opens in focus, such as a reference in a
// cell of the traceability matrix.
// as=reviewer puts a person who can edit into reviewer mode, to see what a reviewer sees.
export type BundleSearch = { file?: string; view?: View; waiver?: string; at?: string; as?: "reviewer" };

// validateBundleSearch reads the bundle page's URL search params.
export function validateBundleSearch(s: Record<string, unknown>): BundleSearch {
  const view = s.view === "code" || s.view === "preview" || s.view === "split" ? s.view : undefined;
  return {
    ...(typeof s.file === "string" && s.file ? { file: s.file } : {}),
    ...(view ? { view } : {}),
    ...(typeof s.waiver === "string" && s.waiver ? { waiver: s.waiver } : {}),
    ...(typeof s.at === "string" && /^\d+-\d+$/.test(s.at) ? { at: s.at } : {}),
    ...(s.as === "reviewer" ? { as: "reviewer" as const } : {}),
  };
}
