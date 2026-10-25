import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { initializeDesktop } from "./lib/desktop";
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

async function bootstrap() {
  const container = document.getElementById("root")!;
  try {
    await initializeDesktop();
    createRoot(container).render(<StrictMode><App /></StrictMode>);
  } catch (error) {
    const heading = document.createElement("h1");
    heading.textContent = "YouTube Vault could not start";
    const detail = document.createElement("p");
    detail.textContent = error instanceof Error ? error.message : "Restart the application to try again.";
    container.replaceChildren(heading, detail);
  }
}

void bootstrap();
