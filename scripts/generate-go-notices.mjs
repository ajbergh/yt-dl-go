import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const serverRoot = path.join(repoRoot, "src", "server");
const licensesRoot = path.join(serverRoot, "licenses");
const goLicensesRoot = path.join(licensesRoot, "go");
const reportPath = path.join(serverRoot, "GO_THIRD_PARTY_NOTICES.md");
const csvPath = path.join(licensesRoot, "go-dependencies.csv");
const ignoredPackages = ["youtube-downloader", "github.com/dop251/goja/ftoa"];
const cacheDirectory = mkdtempSync(path.join(repoRoot, ".go-licenses-cache-"));
process.on("exit", () => rmSync(cacheDirectory, { recursive: true, force: true }));

function run(command, args, options = {}) {
  return execFileSync(command, args, {
    cwd: serverRoot,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    env: { ...process.env, GOCACHE: cacheDirectory },
    ...options,
  });
}

function parseCsvLine(line) {
  const fields = [];
  let value = "";
  let quoted = false;
  for (let index = 0; index < line.length; index += 1) {
    const character = line[index];
    if (character === '"') {
      if (quoted && line[index + 1] === '"') {
        value += '"';
        index += 1;
      } else {
        quoted = !quoted;
      }
    } else if (character === "," && !quoted) {
      fields.push(value);
      value = "";
    } else {
      value += character;
    }
  }
  fields.push(value);
  return fields;
}

const ignoreArgs = ignoredPackages.flatMap(value => ["--ignore", value]);
run("go-licenses", ["save", "./...", "--save_path=licenses/go", "--force", ...ignoreArgs], {
  stdio: ["ignore", "ignore", "inherit"],
});
run("go-licenses", ["check", "./...", "--disallowed_types=forbidden,unknown", ...ignoreArgs], {
  stdio: ["ignore", "ignore", "inherit"],
});
const csv = run("go-licenses", ["report", "./...", ...ignoreArgs]);
const rows = csv.trim().split(/\r?\n/).filter(Boolean).map(parseCsvLine);
if (rows.length === 0) throw new Error("go-licenses returned no dependency report");
if (rows.some(row => !row[0] || !row[2] || /\bunknown\b/i.test(row[2]))) {
  const unknown = rows.filter(row => !row[0] || !row[2] || /\bunknown\b/i.test(row[2]));
  throw new Error(`Unknown Go dependency license(s): ${JSON.stringify(unknown)}`);
}

const modules = run("go", ["list", "-m", "-f", "{{.Path}}\t{{.Version}}", "all"])
  .trim()
  .split(/\r?\n/)
  .map(line => line.split("\t"))
  .filter(([modulePath, version]) => modulePath && version)
  .sort(([a], [b]) => b.length - a.length);
const versionFor = packagePath => modules.find(([modulePath]) => packagePath === modulePath || packagePath.startsWith(`${modulePath}/`))?.[1] || "unknown";
const moduleFor = packagePath => modules.find(([modulePath]) => packagePath === modulePath || packagePath.startsWith(`${modulePath}/`))?.[0] || packagePath;

const goRoot = run("go", ["env", "GOROOT"]).trim();
const standardLicenseDirectory = path.join(goLicensesRoot, "Go-Standard-Library");
mkdirSync(standardLicenseDirectory, { recursive: true });
copyFileSync(path.join(goRoot, "LICENSE"), path.join(standardLicenseDirectory, "LICENSE"));

const gojaRoot = run("go", ["list", "-m", "-f", "{{.Dir}}", "github.com/dop251/goja"]).trim();
const gojaLicenseDirectory = path.join(goLicensesRoot, "github.com", "dop251", "goja");
const ftoaLicenseDirectory = path.join(gojaLicenseDirectory, "ftoa");
const v8LicenseDirectory = path.join(ftoaLicenseDirectory, "internal", "fast");
mkdirSync(v8LicenseDirectory, { recursive: true });
copyFileSync(path.join(gojaRoot, "ftoa", "LICENSE_LUCENE"), path.join(ftoaLicenseDirectory, "LICENSE_LUCENE"));
copyFileSync(path.join(gojaRoot, "ftoa", "internal", "fast", "LICENSE_V8"), path.join(v8LicenseDirectory, "LICENSE_V8"));

const sortedRows = rows.sort((a, b) => a[0].localeCompare(b[0]));
writeFileSync(csvPath, `${sortedRows.map(row => row.map(field => `"${String(field).replaceAll('"', '""')}"`).join(",")).join("\n")}\n`, "utf8");

const markdownRows = sortedRows.map(([packagePath, licenseURL, licenseName]) => {
  const modulePath = moduleFor(packagePath);
  const version = versionFor(packagePath);
  const source = licenseURL && licenseURL !== "Unknown"
    ? `[source/license](${licenseURL})`
    : `[source](https://pkg.go.dev/${packagePath}@${version})`;
  const licenseFiles = `[license files](licenses/go/${modulePath})`;
  return `| \`${packagePath}\` | \`${version}\` | ${licenseName} | ${source} | ${licenseFiles} |`;
});
markdownRows.push(
  "| `github.com/dop251/goja/ftoa` (Lucent-derived implementation) | goja module version | Lucent license | [source](https://github.com/dop251/goja) | [text](licenses/go/github.com/dop251/goja/ftoa/LICENSE_LUCENE) |",
  "| `github.com/dop251/goja/ftoa/internal/fast` (V8-derived implementation) | goja module version | BSD-3-Clause | [source](https://github.com/dop251/goja) | [text](licenses/go/github.com/dop251/goja/ftoa/internal/fast/LICENSE_V8) |",
  `| Go standard library and runtime | \`${run("go", ["version"]).trim()}\` | BSD-style | [source and license](https://go.dev/LICENSE) | [text](licenses/go/Go-Standard-Library/LICENSE) |`,
);

writeFileSync(reportPath, [
  "# Go third-party notices",
  "",
  "Generated from the Go packages used by the application with `go-licenses`. The CSV companion preserves the tool output; redistributed license texts and the LGPL component's source are under `licenses/go/`.",
  "",
  "| Package | Version | License | Source | Included text/source |",
  "| --- | --- | --- | --- | --- |",
  ...markdownRows,
  "",
].join("\n"), "utf8");
console.log(`Generated Go notices for ${sortedRows.length + 3} packages, including the Go runtime and goja embedded notices.`);
