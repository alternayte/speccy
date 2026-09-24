import { catalogSlugs } from "./check-catalog.gen";

// docsBase is the docs site. A custom domain changes this one line.
export const docsBase = "https://speccy-docs.pages.dev";

// checkDocsURL is the Check catalog entry of a check, or undefined for a check the catalog
// does not list, such as one that only a custom profile defines.
export function checkDocsURL(slug: string): string | undefined {
  return catalogSlugs.has(slug) ? `${docsBase}/reference/checks/#${slug}` : undefined;
}
