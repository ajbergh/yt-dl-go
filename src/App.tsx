import { BrowserRouter, Route, Routes } from "react-router-dom";
import { ErrorBoundary } from "@/components/error-boundary";
import { AppShell } from "@/components/app-shell";
import { NotFoundPage } from "@/pages/not-found";
import { routes } from "@/routes";
import { AppViewSignal } from "@/lib/app-view-signal";
import { isPreviewMount } from "@/lib/app-view-serializer";

/**
 * React application root. The packaged Go server hosts the app at `/`;
 * `getRouterBasename` also supports a Vite base path when that prefix matches
 * the browser's current pathname.
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
const APP_VIEW_ROUTE_PATTERNS: readonly string[] = routes.map((route) => route.path);
const APP_VIEW_PREVIEW_MOUNT: boolean = isPreviewMount(import.meta.env.BASE_URL);

export function App() {
  return (
    <ErrorBoundary>
      <BrowserRouter basename={ROUTER_BASENAME}>
        <Routes>
          <Route path="/" element={<AppShell />}>
            {routes.map((route) =>
              route.path === "/" ? (
                <Route key={route.path} index element={route.element} />
              ) : (
                <Route key={route.path} path={route.path} element={route.element} />
              ),
            )}
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Routes>
        {APP_VIEW_PREVIEW_MOUNT && <AppViewSignal patterns={APP_VIEW_ROUTE_PATTERNS} />}
      </BrowserRouter>
    </ErrorBoundary>
  );
}
