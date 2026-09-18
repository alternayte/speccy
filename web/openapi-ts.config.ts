import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../api/openapi.yaml",
  output: { path: "src/lib/api", postProcess: ["prettier"] },
  plugins: [
    { name: "@hey-api/client-fetch", baseUrl: "/api/v1" },
    "@hey-api/typescript",
    "@hey-api/sdk",
    { name: "@tanstack/react-query", queryOptions: true, mutationOptions: true, queryKeys: true },
  ],
});
