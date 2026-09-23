import path from "path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, searchForWorkspaceRoot } from "vite";
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
  console.log(`[vite.config] mode=${mode} envDir=${envDir} base=${base}`);

  return {
    plugins: [react(), tailwindcss()],
    base,
    // Use an optional external cache directory in restricted/container
    // workspaces; otherwise keep Vite's regenerable cache in this project.
    cacheDir: process.env.AETHER_VITE_CACHE_DIR || path.resolve(envDir, ".vite-cache"),
    resolve: {
      alias: {
        "@": path.resolve(envDir, "src"),
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
      watch: {
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
      // Pin the small production dependency set so local development does not
      // discover/prebundle React-linked packages in multiple passes.
      holdUntilCrawlEnd: true,
      include: [
        "react",
        "react/jsx-runtime",
        "react/jsx-dev-runtime",
        "react-dom",
        "react-dom/client",
        "react-router-dom",
        "@dnd-kit/core",
        "@dnd-kit/sortable",
        "@dnd-kit/utilities",
        "lucide-react",
      ],
    },
  };
});
