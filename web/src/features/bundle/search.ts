import type { View } from "@/features/editor/editor-pane";

export type BundleSearch = { file?: string; view?: View };

// validateBundleSearch reads the bundle page's URL search params.
export function validateBundleSearch(s: Record<string, unknown>): BundleSearch {
  const view = s.view === "code" || s.view === "preview" || s.view === "split" ? s.view : undefined;
  return {
    ...(typeof s.file === "string" && s.file ? { file: s.file } : {}),
    ...(view ? { view } : {}),
  };
}
