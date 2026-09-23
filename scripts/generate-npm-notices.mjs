import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const checker = path.join(repoRoot, "tools", "license-checker", "node_modules", "license-checker", "bin", "license-checker");
const customFormat = path.join(repoRoot, "scripts", "license-checker-format.json");
const licensesRoot = path.join(repoRoot, "src", "server", "licenses");
const outputDirectory = path.join(licensesRoot, "npm");
const reportPath = path.join(repoRoot, "src", "server", "NPM_THIRD_PARTY_NOTICES.md");
const rootPackage = JSON.parse(readFileSync(path.join(repoRoot, "package.json"), "utf8"));
const rootPackageKey = `${rootPackage.name}@${rootPackage.version}`;

if (path.relative(repoRoot, outputDirectory).startsWith("..")) {
  throw new Error(`Refusing to write outside the repository: ${outputDirectory}`);
}

const report = execFileSync(process.execPath, [
  checker,
  "--production",
  "--json",
  "--customPath",
  customFormat,
], {
  cwd: repoRoot,
  encoding: "utf8",
  maxBuffer: 64 * 1024 * 1024,
});
const packages = JSON.parse(report);
const rows = Object.entries(packages)
  .filter(([packageKey]) => packageKey !== rootPackageKey)
  .sort(([a], [b]) => a.localeCompare(b));
if (rows.length === 0) {
  throw new Error("license-checker returned no production dependencies");
}

const generated = [];
for (const [packageKey, details] of rows) {
  const license = String(details.licenses || "").trim();
  const licenseText = String(details.licenseText || "").trim();
  if (!license || /\b(unknown|unlicensed)\b/i.test(license)) {
    throw new Error(`Unknown or unlicensed npm dependency: ${packageKey} (${license || "missing"})`);
  }
  if (!licenseText) {
    throw new Error(`No license text found for npm dependency: ${packageKey}`);
  }
  const safeName = packageKey.replace(/[^A-Za-z0-9._-]+/g, "-").replace(/-+/g, "-");
  const suffix = createHash("sha256").update(packageKey).digest("hex").slice(0, 8);
  const fileName = `${safeName}-${suffix}.txt`;
  generated.push({
    packageKey,
    version: details.version || "unknown",
    license,
    repository: String(details.repository || "").replace(/^git\+/, "").replace(/\.git$/, ""),
    fileName,
    licenseText,
  });
}

rmSync(outputDirectory, { recursive: true, force: true });
mkdirSync(outputDirectory, { recursive: true });
for (const item of generated) {
  writeFileSync(path.join(outputDirectory, item.fileName), `${item.licenseText}\n`, "utf8");
}

const markdown = [
  "# npm third-party notices",
  "",
  "Generated from production dependencies in the root `package-lock.json` with `license-checker`. License texts are in `licenses/npm/`.",
  "",
  "| Package | Version | License | Source | License text |",
  "| --- | --- | --- | --- | --- |",
  ...generated.map(item => {
    const source = item.repository ? `[repository](${item.repository})` : "Not listed";
    return `| \`${item.packageKey}\` | \`${item.version}\` | ${item.license} | ${source} | [text](licenses/npm/${item.fileName}) |`;
  }),
  "",
];
writeFileSync(reportPath, markdown.join("\n"), "utf8");
console.log(`Generated npm notices for ${generated.length} production packages.`);
