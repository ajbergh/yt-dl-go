import path from "path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, searchForWorkspaceRoot } from "vite";
import type { Plugin } from "vite";
import devReloadSSE from "./vite-dev-reload";

// Dev-only shim for Vite's HMR WebSocket diagnostics. Vite HMR is disabled in
// favor of this project's SSE reload plugin, but the Vite client still probes
// its WebSocket endpoint. This shim suppresses that expected failure noise.
const DEV_WS_MUTE_SCRIPT = `
      // Mute Vite's HMR-failure noise. Native HMR is off (vite.config.ts:
      // server.hmr=false) because the runtime preview proxy is HTTP-only;
      // reload comes from the SSE dev-reload plugin instead. @vite/client
      // still attempts a WS connection regardless of hmr:false, so two layers:
      //  1. Stub WebSocket for subprotocol 'vite-hmr' — the synthetic close
      //     skips the network entirely, so the browser never logs its native
      //     "WebSocket connection ... failed:" line (that line bypasses
      //     console.* and can't be filtered any other way). Vite's transport
      //     rejects with "closed without opened", matching its expected path.
      //  2. Filter "failed to connect to websocket" from console.warn/error,
      //     covering Vite's own diagnostic at client.mjs:861/868.
      // Must run before @vite/client loads.
      (function () {
        const RealWS = window.WebSocket;
        function StubWS(url, protocols) {
          const isHmr =
            protocols === 'vite-hmr' ||
            (Array.isArray(protocols) && protocols.includes('vite-hmr'));
          if (!isHmr) return new RealWS(url, protocols);
          const listeners = { close: [], error: [], open: [], message: [] };
          const fake = {
            readyState: 0,
            url: url,
            protocol: '',
            send: function () {},
            close: function () {},
            addEventListener: function (type, fn) {
              (listeners[type] || (listeners[type] = [])).push(fn);
            },
            removeEventListener: function (type, fn) {
              const arr = listeners[type]; if (!arr) return;
              const i = arr.indexOf(fn); if (i >= 0) arr.splice(i, 1);
            },
            dispatchEvent: function () { return true; },
            onopen: null, onclose: null, onerror: null, onmessage: null,
          };
          queueMicrotask(function () {
            fake.readyState = 3;
            const ev = new CloseEvent('close', { code: 1006, reason: '', wasClean: false });
            listeners.close.forEach(function (fn) { try { fn.call(fake, ev); } catch (e) {} });
            if (typeof fake.onclose === 'function') { try { fake.onclose(ev); } catch (e) {} }
          });
          return fake;
        }
        StubWS.CONNECTING = 0;
        StubWS.OPEN = 1;
        StubWS.CLOSING = 2;
        StubWS.CLOSED = 3;
        StubWS.prototype = RealWS.prototype;
        window.WebSocket = StubWS;

        ['warn', 'error'].forEach(function (level) {
          const orig = console[level];
          console[level] = function () {
            const first = arguments[0];
            if (typeof first === 'string' && first.includes('failed to connect to websocket')) return;
            return orig.apply(console, arguments);
          };
        });
      })();
    `;

function devWsMute(): Plugin {
  return {
    name: "aether-dev-ws-mute",
    apply: "serve",
    transformIndexHtml(html: string) {
      return {
        html,
        tags: [
          { tag: "script", injectTo: "head-prepend", children: DEV_WS_MUTE_SCRIPT },
        ],
      };
    },
  };
}

export default defineConfig(({ mode }) => {
  // Resolve project files from the directory where Vite is launched; this repo's
  // npm scripts run it from the repository root.
  const envDir = process.cwd();
  const env = loadEnv(mode, envDir, "");
  // An embedding preview can provide a path prefix through VITE_BASE. Normal
  // local development and production builds use relative asset paths.
  const base = env.VITE_BASE || "./";
  // Intentional: visible at dev-server startup so we can confirm which base Vite
  // is actually using when debugging proxy issues. Safe to leave in — only runs
  // once per `vite` invocation.
  console.log(`[vite.config] mode=${mode} envDir=${envDir} base=${base} reload=sse`);

  return {
    plugins: [react(), tailwindcss(), devReloadSSE(), devWsMute()],
    base,
    // Use an optional external cache directory in restricted/container
    // workspaces; otherwise keep Vite's regenerable cache in this project.
    cacheDir: process.env.AETHER_VITE_CACHE_DIR || path.resolve(envDir, ".vite-cache"),
    resolve: {
      alias: {
        "@": path.resolve(envDir, "src"),
        // Azure Artifacts can mirror TanStack packages without their build/
        // outputs. Vite dev can consume the shipped sources directly.
        "@tanstack/react-query": path.resolve(envDir, "node_modules/@tanstack/react-query/src/index.ts"),
        "@tanstack/query-core": path.resolve(envDir, "node_modules/@tanstack/query-core/src/index.ts"),
        "@tanstack/react-table": path.resolve(envDir, "node_modules/@tanstack/react-table/src/index.tsx"),
        "@tanstack/table-core": path.resolve(envDir, "node_modules/@tanstack/table-core/src/index.ts"),
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      write: process.env.VITE_CHECK_MODE !== "true",
      rollupOptions: {
        output: {
          // Keep the embedded SPA in one JS chunk to avoid extra requests for
          // this desktop-local interface. Revisit if future code splitting is added.
          inlineDynamicImports: true,
        },
      },
    },
    server: {
      port: 5173,
      strictPort: true,
      // An external optimized-dependency cache is served through `/@fs/` URLs.
      // Add it to Vite's strict filesystem allowlist only when configured;
      // otherwise leave Vite's default filesystem policy unchanged.
      ...(process.env.AETHER_VITE_CACHE_DIR
        ? {
            fs: {
              allow: [
                searchForWorkspaceRoot(process.cwd()),
                process.env.AETHER_VITE_CACHE_DIR,
              ],
            },
          }
        : {}),
      // Keep the development server local; it is not intended to be exposed
      // as a network service.
      host: "127.0.0.1",
      // This project uses its HTTP Server-Sent Events reload channel instead
      // of Vite's native WebSocket HMR.
      hmr: false,
      // Polling also works on filesystems that do not reliably deliver native
      // file-change notifications.
      watch: {
        usePolling: true,
        interval: 500,
        ignored: [
          "**/node_modules/**",
          "**/.git/**",
          "**/.vite-cache/**",
          "**/dist/**",
          "**/.dev-server.log",
          "**/.browser-errors.log",
          // Diagnostic sidecar files must not trigger a page reload when their
          // contents are updated.
          "**/.browser-errors.seq",
          // Vite creates temporary bundled config files in the project root.
          // Ignore them to avoid reloads during config evaluation.
          "**/*.timestamp-*.mjs",
          "**/*.timestamp-*.cjs",
          // Host-managed app metadata is not part of the Vite source bundle.
          "**/ms.config.json",
        ],
      },
    },
    optimizeDeps: {
      // Pin React + every React-importing dep here. Omitting `radix-ui`
      // lets Vite optimize its deep imports on-demand via the CJS-interop
      // path, which can yield `React.useContext === null` at runtime.
      //
      // An aliased dependency whose target is TSX is intentionally omitted:
      // Vite cannot prebundle TSX targets, so listing one here only produces a
      // warning. `@tanstack/react-table` is served through the JSX transform.
      //
      // `holdUntilCrawlEnd: true` reverts Vite 5.1+'s default partial-prebundle
      // behavior. Without it, Vite ships an initial prebundle while still
      // discovering deps, then re-prebundles when new deps surface — producing
      // multiple `?v=<hash>` versions of `react.js` in one load. Separate React
      // module identities can break hooks and context providers.
      holdUntilCrawlEnd: true,
      include: [
        "react",
        "react/jsx-runtime",
        "react/jsx-dev-runtime",
        "react-dom",
        "react-dom/client",
        // Radix UI unified package — every shadcn (new-york) ui primitive
        // imports from `radix-ui` (e.g. `import { Dialog } from "radix-ui"`).
        // Pin it to avoid on-demand re-optimization when UI primitives import it.
        "radix-ui",
        "@dnd-kit/core",
        "@dnd-kit/sortable",
        "@dnd-kit/utilities",
        // Optional host SDK integration: optimize its `/data` subpath if used.
        "@microsoft/managed-apps/data",
        // These packages are aliased to shipped TypeScript sources. Pinning
        // them here makes Vite prepare their optimized dependencies at server
        // startup rather than on the first page request.
        "@tanstack/react-query",
        "class-variance-authority",
        "clsx",
        "cmdk",
        "date-fns",
        "zustand",
        "lucide-react",
        "motion",
        "react-day-picker",
        "react-router-dom",
        "recharts",
        "sonner",
        "tailwind-merge",
        "unpdf",
        "xlsx",
      ],
    },
  };
});
