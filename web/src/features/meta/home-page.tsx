import { useQuery } from "@tanstack/react-query";
import { getMetaOptions } from "@/lib/api/@tanstack/react-query.gen";

export function HomePage() {
  const meta = useQuery(getMetaOptions());
  return (
    <main className="mx-auto max-w-[72ch] px-4 py-16">
      <h1 className="text-3xl font-semibold tracking-tight">Speccy</h1>
      <p className="mt-4 text-neutral-600 dark:text-neutral-400">
        Speccy reviews markdown spec bundles and returns one verdict: Build Ready or Not Build Ready.
      </p>
      <p className="mt-8 text-sm text-neutral-500" aria-live="polite">
        {meta.isPending && "Connecting to the server."}
        {meta.isError && "The server did not answer. Check that Speccy is running."}
        {meta.isSuccess && `Version ${meta.data.version}, ${meta.data.mode} mode.`}
      </p>
    </main>
  );
}
