import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const server = path.join(root, "src/server");
const goos = { win32: "windows", darwin: "darwin", linux: "linux" }[process.platform];
const hostArch = { x64: "amd64", arm64: "arm64" }[process.arch];
const target = process.argv[2] || `${goos}/${hostArch}`;
const [targetOS, arch] = target.split("/");
if (!goos || targetOS !== goos || !["amd64", "arm64"].includes(arch) || target !== `${targetOS}/${arch}`) {
  throw new Error("Build on the target OS. Supported targets: windows, darwin, linux / amd64, arm64.");
}
if (goos !== "windows" && arch !== hostArch) throw new Error("Build Linux and macOS packages on a runner with the target architecture.");

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: server, stdio: "inherit", ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${path.basename(command)} failed (${result.status})`);
  return result.stdout?.toString().trim();
}

const goRoot = run("go", ["env", "GOROOT"], { stdio: ["ignore", "pipe", "inherit"] });
const tools = path.join(os.tmpdir(), "yt-dl-go-desktop-tools-v2.17.0");
mkdirSync(tools, { recursive: true });
const env = {
  ...process.env, GOROOT: goRoot, GOTOOLCHAIN: "local", GOBIN: tools,
  GOOS: goos, GOARCH: hostArch, CGO_ENABLED: "1",
  PATH: [path.join(goRoot, "bin"), tools, process.env.PATH].join(path.delimiter),
};
const exe = name => path.join(tools, name + (goos === "windows" ? ".exe" : ""));
for (const [name, module] of [
  ["wails", "github.com/wailsapp/wails/v2/cmd/wails@v2.17.0"],
  ["go-licenses", "github.com/google/go-licenses/v2@v2.0.1"],
]) {
  if (!existsSync(exe(name))) run("go", ["install", module], { env });
}
const version = process.env.VERSION || "dev";
const commit = process.env.COMMIT || run("git", ["rev-parse", "HEAD"], { cwd: root, stdio: ["ignore", "pipe", "inherit"] });
const buildDate = process.env.BUILD_DATE || new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
for (const value of [version, commit, buildDate]) {
  if (!/^[A-Za-z0-9:._+\-]+$/.test(value)) throw new Error("Invalid build metadata");
}
const tags = goos === "linux" ? "desktop,webkit2_41" : "desktop";
const flags = `-s -w -X main.buildVersion=${version} -X main.buildCommit=${commit} -X main.buildDate=${buildDate}`;
const frontendEnv = { ...env };
delete frontendEnv.VITE_CHECK_MODE;
run(process.execPath, [path.join(root, "node_modules/vite/bin/vite.js"), "build", "--configLoader", "runner", "--outDir", path.join(server, "dist")], { cwd: root, env: frontendEnv });
if (!existsSync(path.join(server, "dist/index.html"))) throw new Error("Desktop frontend did not produce index.html");
run(exe("wails"), ["build", "-s", "-m", "-skipbindings", "-platform", target, "-tags", tags, "-ldflags", flags], { env });

const output = path.join(root, "dist/desktop", `${goos}-${arch}`);
mkdirSync(output, { recursive: true });
const stage = mkdtempSync(path.join(output, ".package-"));
const name = `youtube-vault-${goos}-${arch}`;
const contents = path.join(stage, name);
mkdirSync(contents);
try {
  const buildBin = path.join(server, "build/bin");
  if (goos === "darwin") {
    const app = readdirSync(buildBin).find(name => name.endsWith(".app"));
    if (!app) throw new Error("Wails did not produce a macOS app bundle");
    cpSync(path.join(buildBin, app), path.join(contents, app), { recursive: true });
  } else {
    const binary = "youtube-vault" + (goos === "windows" ? ".exe" : "");
    cpSync(path.join(buildBin, binary), path.join(contents, binary));
  }
  for (const file of ["README.md", "LICENSE"]) cpSync(path.join(root, file), path.join(contents, file));
  for (const file of ["THIRD_PARTY_NOTICES.md", "GO_THIRD_PARTY_NOTICES.md", "NPM_THIRD_PARTY_NOTICES.md", "LGPL_RELINKING.md"]) {
    cpSync(path.join(server, file), path.join(contents, file));
  }
  cpSync(path.join(root, "docs/DESKTOP.md"), path.join(contents, "DESKTOP.md"));
  cpSync(path.join(server, "licenses"), path.join(contents, "licenses"), { recursive: true });
  const licenseEnv = { ...env, GOOS: goos, GOARCH: arch, GOFLAGS: `-tags=${tags}` };
  const ignored = ["--ignore", "youtube-downloader", "--ignore", "github.com/dop251/goja/ftoa"];
  run(exe("go-licenses"), ["check", ".", "--disallowed_types=forbidden,unknown", ...ignored], { env: licenseEnv });
  const report = run(exe("go-licenses"), ["report", ".", ...ignored], { env: licenseEnv, stdio: ["ignore", "pipe", "inherit"] });
  writeFileSync(path.join(contents, "DESKTOP_GO_LICENSES.csv"), report + "\n");
  run(exe("go-licenses"), ["save", ".", `--save_path=${path.join(contents, "desktop-licenses")}`, ...ignored], { env: licenseEnv });
  const archive = path.join(output, name + (goos === "windows" ? ".zip" : ".tar.gz"));
  if (goos === "windows") {
    const quote = text => "'" + text.replaceAll("'", "''") + "'";
    const script = `$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; Compress-Archive -LiteralPath ${quote(contents)} -DestinationPath ${quote(archive)} -Force`;
    run("powershell.exe", ["-NoProfile", "-NonInteractive", "-EncodedCommand", Buffer.from(script, "utf16le").toString("base64")], { env });
  } else {
    run("tar", ["-czf", archive, "-C", stage, name], { env });
  }
  const digest = createHash("sha256").update(readFileSync(archive)).digest("hex");
  writeFileSync(archive + ".sha256", `${digest}  ${path.basename(archive)}\n`);
  console.log(`Desktop package: ${archive}`);
  console.log(`SHA-256: ${digest}`);
} finally {
  const relative = path.relative(output, stage);
  if (!relative.startsWith(".package-") || relative.includes(path.sep)) throw new Error("Unexpected package staging directory");
  rmSync(stage, { recursive: true, force: true });
}
