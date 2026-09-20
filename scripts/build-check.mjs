import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const viteCLI = fileURLToPath(new URL("../node_modules/vite/bin/vite.js", import.meta.url));
const result = spawnSync(process.execPath, [viteCLI, "build", "--configLoader", "runner"], {
  env: { ...process.env, VITE_CHECK_MODE: "true" },
  stdio: "inherit",
});

if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}

process.exit(result.status ?? 1);
