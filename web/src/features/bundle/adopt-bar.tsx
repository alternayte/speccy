import { useMutation, useQueryClient } from "@tanstack/react-query";
import { FileCog } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/states";
import type { Adopt } from "@/lib/api";
import { adoptFrontmatterMutation, getBundleOptions } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";

// AdoptBar offers to write the type and the size that the review used into the main doc's
// frontmatter (REQ-135). It appears after the first verdict, when the doc names neither, so
// the author chooses to make the choice stick once they have seen the value.
export function AdoptBar({ bundleId, adopt, canEdit }: { bundleId: string; adopt?: Adopt; canEdit: boolean }) {
  const qc = useQueryClient();
  const write = useMutation({
    ...adoptFrontmatterMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: getBundleOptions({ path: { bundleId } }).queryKey }),
  });
  if (!adopt || (!adopt.type && !adopt.size) || !canEdit) return null;
  const parts = [adopt.type ? `type: ${adopt.type}` : "", adopt.size ? `size: ${adopt.size}` : ""].filter(Boolean);
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-line bg-sunken px-4 py-2 sm:px-5">
      <FileCog aria-hidden className="size-4 shrink-0 text-ink-3" />
      <p className="min-w-0 text-xs text-ink-2">
        This doc names no {adopt.type && adopt.size ? "type or size" : adopt.type ? "type" : "size"}. The review used{" "}
        <span className="font-mono text-ink">{parts.join(", ")}</span>.
      </p>
      <Button
        size="sm"
        className="ml-auto"
        disabled={write.isPending}
        onClick={() => write.mutate({ path: { bundleId } })}
      >
        Write it into the doc
      </Button>
      {write.isError ? <ErrorState message={problemMessage(write.error)} /> : null}
    </div>
  );
}
