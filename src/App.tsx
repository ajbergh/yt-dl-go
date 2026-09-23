import { BrowserRouter, Route, Routes } from "react-router-dom";
import { ErrorBoundary } from "@/components/error-boundary";
import { AppShell } from "@/components/app-shell";
import { NotFoundPage } from "@/pages/not-found";
import { routes } from "@/routes";

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
  return "/";
}

const ROUTER_BASENAME = getRouterBasename();

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
      </BrowserRouter>
    </ErrorBoundary>
  );
}
