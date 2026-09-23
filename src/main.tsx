import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./index.css";

// Normalize `/index.html` to the SPA root before BrowserRouter mounts. This
// preserves query parameters and the hash without another network request.
if (window.location.pathname.endsWith("/index.html")) {
  const newPath = window.location.pathname.replace(/\/index\.html$/, "/");
  window.history.replaceState(
    null,
    "",
    newPath + window.location.search + window.location.hash,
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
