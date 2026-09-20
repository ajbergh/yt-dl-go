import { QueryClient } from "@tanstack/react-query";

// Shared defaults for any TanStack queries used by the app. The downloader's
// live job list is currently refreshed directly by HomePage, not through Query.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000,
      retry: 1,
    },
  },
});
