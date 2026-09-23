import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const viteCLI = fileURLToPath(new URL("../node_modules/vite/bin/vite.js", import.meta.url));
const forbidden = ["ancestorOrigins", "m365.cloud.microsoft", "__dev/console"];
const outDir = mkdtempSync(join(tmpdir(), "yt-dl-go-build-check-"));

try {
  const env = { ...process.env };
  delete env.VITE_CHECK_MODE;
  const result = spawnSync(
    process.execPath,
    [viteCLI, "build", "--configLoader", "runner", "--outDir", outDir],
    { env, stdio: "inherit" },
  );
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`Vite build failed with status ${result.status}`);

  const assetsDir = join(outDir, "assets");
  const scripts = readdirSync(assetsDir).filter((name) => name.endsWith(".js"));
  if (scripts.length === 0) throw new Error("Production build contains no JavaScript assets");
  for (const name of scripts) {
    const source = readFileSync(join(assetsDir, name), "utf8");
    for (const token of forbidden) {
      if (source.includes(token)) {
        throw new Error(`Production bundle ${name} contains forbidden string ${token}`);
      }
    }
  }
  console.log(`Checked ${scripts.length} production JavaScript asset(s) for preview-host code`);
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  rmSync(outDir, { recursive: true, force: true });
}
