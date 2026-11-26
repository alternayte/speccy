import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import { getMeQueryKey } from "./lib/api/@tanstack/react-query.gen";
import { client } from "./lib/api/client.gen";
import { statusError } from "./lib/problem";
import { routeTree } from "./routeTree.gen";

client.setConfig({ baseUrl: "/api/v1" });

const queryClient = new QueryClient();

// Hosted mode: a 401 means the session ended. Reading "me" again sends the person to sign in.
client.interceptors.response.use((res) => {
  if (res.status === 401 && !res.url.endsWith("/api/v1/me")) {
    void queryClient.invalidateQueries({ queryKey: getMeQueryKey() });
  }
  return res;
});
// An error answer that is not a Problem, such as a proxy's 504 page, names its status instead of
// reading as "could not reach the server".
client.interceptors.error.use((err, res) => statusError(err, res));
const router = createRouter({ routeTree, context: { queryClient } });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const root = document.getElementById("root");
if (!root) throw new Error("The page has no #root element.");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
