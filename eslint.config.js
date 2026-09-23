import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

// Flat config (ESLint 9). Standard Vite + React + TypeScript setup.
// `generated/` is excluded because generated output is not maintained here.
export default tseslint.config(
  {
    ignores: [
      "dist",
      "node_modules",
      "generated",
      "dev_mock_new_ui",
      ".vite-cache",
      "*.timestamp-*.mjs",
    ],
  },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
      // Keep imports aligned with the packages and routing model used by this
      // Vite/React application, including transitive package entry points.
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["framer-motion"],
              message:
                "Use `motion/react` instead (e.g. `import { motion, AnimatePresence } from \"motion/react\"`).",
            },
            {
              group: ["@radix-ui/react-*"],
              message:
                "Import Radix primitives from the unified `radix-ui` package (e.g. `import { Dialog } from \"radix-ui\"`), or use the vendored `@/components/ui/*` wrappers.",
            },
            {
              group: ["next", "next/*"],
              message:
                "This is a Vite SPA, not Next.js. Use react-router-dom for routing and plain modules — there is no `next` runtime.",
            },
            {
              group: ["redux", "react-redux", "jotai", "recoil"],
              message:
                "Use Zustand (`@/lib/store`) for shared UI state; keep persisted jobs and preferences in the Go API.",
            },
          ],
        },
      ],
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "MemberExpression[object.name='window'][property.name=/^(parent|top)$/]",
          message:
            "Production code must not access a parent frame.",
        },
        {
          selector:
            "CallExpression[callee.name='postMessage'], CallExpression[callee.property.name='postMessage']",
          message:
            "Production code must not post messages to another window.",
        },
      ],
    },
  },
  {
    // Vendored UI wrappers may co-export a component and its `cva` variants
    // (for example, `buttonVariants`), which trips react-refresh. Disable that
    // rule only for the UI-wrapper directory.
    files: ["src/components/ui/**/*.{ts,tsx}"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
  {
    // The development reload plugin handles Vite internals that are not fully
    // typed and intentionally ignores some best-effort broadcast failures.
    // Limit these lint exceptions to that plugin.
    files: ["vite-dev-reload.ts"],
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "no-empty": "off",
    },
  }
);
