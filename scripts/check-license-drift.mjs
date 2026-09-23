import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const generatedPaths = [
  "src/server/THIRD_PARTY_NOTICES.md",
  "src/server/GO_THIRD_PARTY_NOTICES.md",
  "src/server/NPM_THIRD_PARTY_NOTICES.md",
  "src/server/licenses",
];

for (const generator of ["generate-go-notices.mjs", "generate-npm-notices.mjs"]) {
  execFileSync(process.execPath, [path.join(repoRoot, "scripts", generator)], {
    cwd: repoRoot,
    stdio: "inherit",
  });
}

const changed = execFileSync("git", ["status", "--porcelain", "--", ...generatedPaths], {
  cwd: repoRoot,
  encoding: "utf8",
});
if (changed.trim()) {
  process.stderr.write("Third-party notice files are out of date. Regenerate them with `npm run licenses:generate:go` and `npm run licenses:generate`.\n");
  process.stderr.write(changed);
  process.exitCode = 1;
}
