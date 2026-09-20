import { StrictMode, useEffect } from "react";
import { createRoot } from "react-dom/client";
// Install the optional preview diagnostics before importing App so runtime
// errors during App module evaluation can be captured. The Go executable does
// not implement the Vite-only `/__dev/console` diagnostics endpoint.
import { reportPrivateRuntimeError } from "./lib/console-capture";
import { App } from "./App";
import { postAppRuntimeErrorToCoworkParent } from "./lib/cowork-parent-transport";
import { postAppMountedToCoworkParent } from "./lib/app-mounted-transport";
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

// Record a post-commit animation-frame timing mark and, when embedded in a
// supported preview host, send its fixed-shape mounted signal. requestAnimationFrame
// runs before paint; this is a first-frame checkpoint, not proof that pixels
// have already been painted.
function MountSignal() {
  useEffect(() => {
    requestAnimationFrame(() => {
      performance.mark("aether:app-mounted");
      postAppMountedToCoworkParent();
    });
  }, []);
  return null;
}

createRoot(document.getElementById("root")!, {
  onCaughtError(error, errorInfo) {
    reportPrivateRuntimeError(
      "[React caught error]",
      error,
      errorInfo.componentStack,
    );
  },
  onUncaughtError(error, errorInfo) {
    reportPrivateRuntimeError(
      "[React uncaught error]",
      error,
      errorInfo.componentStack,
    );
    postAppRuntimeErrorToCoworkParent();
  },
  onRecoverableError(error, errorInfo) {
    reportPrivateRuntimeError(
      "[React recoverable error]",
      error,
      errorInfo.componentStack,
    );
  },
}).render(
  <StrictMode>
    <App />
    <MountSignal />
  </StrictMode>
);
