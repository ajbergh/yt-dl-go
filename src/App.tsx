import { BrowserRouter, Route, Routes } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { MotionConfig } from "motion/react";
import { ErrorBoundary } from "@/components/error-boundary";
import { ConfirmDialogProvider } from "@/components/confirm-dialog";
import { queryClient } from "@/lib/query-client";
import { AppShell } from "@/components/app-shell";
import { NotFoundPage } from "@/pages/not-found";
import { routes } from "@/routes";
import { AppViewSignal } from "@/lib/app-view-signal";
import { isPreviewMount } from "@/lib/app-view-serializer";

/**
 * React application root: installs shared providers, route rendering, the
 * error boundary, and optional embedded-preview diagnostics. The packaged Go
 * server hosts the app at `/`; `getRouterBasename` also supports a Vite base
 * path when that prefix matches the browser's current pathname.
 */
/**
 * Use Vite's build base only when the current path is actually mounted beneath
 * it. Otherwise treat the first URL path segment as the app mount, or `/` at
 * the origin root. The packaged executable serves from `/` with a relative base.
 */
function getRouterBasename(): string {
  const buildBase = import.meta.env.BASE_URL.replace(/\/$/, "");
  if (
    buildBase &&
    buildBase.startsWith("/") &&
    (window.location.pathname === buildBase ||
      window.location.pathname.startsWith(buildBase + "/"))
  ) {
    return buildBase;
  }
  const parts = window.location.pathname.split("/").filter(Boolean);
  return parts.length ? `/${parts[0]}/` : "/";
}
const ROUTER_BASENAME = getRouterBasename();

// Route patterns are sent only by the optional embedded-preview signal; URLs
// and path parameters are never substituted for these patterns.
const APP_VIEW_ROUTE_PATTERNS: readonly string[] = routes.map((route) => route.path);

// The app-view observer is enabled only for builds whose base starts with the
// embedded-preview `/v1/` prefix. Normal Vite builds and the executable leave
// it disabled; the signal is also a no-op unless the page is embedded.
const APP_VIEW_PREVIEW_MOUNT: boolean = isPreviewMount(import.meta.env.BASE_URL);

export function App() {
  return (
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <MotionConfig reducedMotion="user">
          <ConfirmDialogProvider>
            <BrowserRouter basename={ROUTER_BASENAME}>
              <Routes>
                <Route path="/" element={<AppShell />}>
                  {routes.map((r) =>
                    r.path === "/" ? (
                      <Route key={r.path} index element={r.element} />
                    ) : (
                      <Route key={r.path} path={r.path} element={r.element} />
                    ),
                  )}
                  <Route path="*" element={<NotFoundPage />} />
                </Route>
              </Routes>
              {APP_VIEW_PREVIEW_MOUNT && <AppViewSignal patterns={APP_VIEW_ROUTE_PATTERNS} />}
            </BrowserRouter>
          </ConfirmDialogProvider>
        </MotionConfig>
      </QueryClientProvider>
    </ErrorBoundary>
  );
}
