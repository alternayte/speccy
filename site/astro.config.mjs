// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightLlmsTxt from "starlight-llms-txt";
import starlightOpenAPI, { createOpenAPISidebarGroup } from "starlight-openapi";

// Cloudflare Web Analytics is cookie-free; it is on only when the deploy passes a beacon token.
const beacon = process.env.PUBLIC_CF_BEACON_TOKEN;
const api = createOpenAPISidebarGroup();

export default defineConfig({
  site: process.env.DOCS_SITE ?? "https://speccy-docs.pages.dev",
  integrations: [
    starlight({
      title: "Speccy",
      description: "Speccy reviews markdown specs and returns one verdict: Build Ready or Not Build Ready.",
      logo: { light: "./src/assets/logo-light.svg", dark: "./src/assets/logo-dark.svg" },
      favicon: "/favicon.svg",
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/alternayte/speccy" }],
      editLink: { baseUrl: "https://github.com/alternayte/speccy/edit/main/site/" },
      customCss: [
        "@fontsource-variable/inter",
        "@fontsource-variable/jetbrains-mono",
        "./src/styles/theme.css",
      ],
      head: beacon
        ? [
            {
              tag: "script",
              attrs: {
                defer: true,
                src: "https://static.cloudflareinsights.com/beacon.min.js",
                "data-cf-beacon": JSON.stringify({ token: beacon }),
              },
            },
          ]
        : [],
      sidebar: [
        { label: "Tutorials", items: [{ autogenerate: { directory: "tutorials" } }] },
        { label: "How-to guides", items: [{ autogenerate: { directory: "how-to" } }] },
        { label: "Concepts", items: [{ autogenerate: { directory: "concepts" } }] },
        {
          label: "Reference",
          items: [{ autogenerate: { directory: "reference" } }, api],
        },
        { label: "Operations", items: [{ autogenerate: { directory: "operations" } }] },
      ],
      plugins: [
        starlightOpenAPI([
          {
            base: "reference/api",
            schema: "../api/openapi.yaml",
            sidebar: { label: "HTTP API", group: api, operations: { badges: true } },
          },
        ]),
        starlightLlmsTxt({ projectName: "Speccy" }),
      ],
    }),
  ],
});
