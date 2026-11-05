import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const server = path.join(root, "src/server");
const temporary = mkdtempSync(path.join(os.tmpdir(), "yt-dl-go-native-ui-"));
const data = path.join(temporary, "data");
mkdirSync(data, { mode: 0o700 });
chmodSync(data, 0o700);
function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: server, stdio: "inherit", ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status})`);
  return result;
}
if (process.platform === "win32") {
  // Elevated runners can create Administrators-owned temporary directories.
  // Match the release smoke fixture: set the owner as well as a protected DACL.
  run("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", `
    $ErrorActionPreference = 'Stop'
    $owner = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = [System.Security.AccessControl.DirectorySecurity]::new()
    $acl.SetOwner($owner)
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @($owner,
      [System.Security.Principal.SecurityIdentifier]::new('S-1-5-18'),
      [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
      $acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new(
        $sid, 'FullControl', 'ContainerInherit, ObjectInherit', 'None', 'Allow'))
    }
    [System.IO.Directory]::SetAccessControl($env:DESKTOP_SMOKE_DATA_DIR, $acl)
  `], { env: { ...process.env, DESKTOP_SMOKE_DATA_DIR: data } });
}
const filename = "youtube-vault" + (process.platform === "win32" ? ".exe" : "");
const packaged = process.argv[2] === "--executable";
let executable = packaged ? path.resolve(process.argv[3]) : path.join(temporary, filename);
if (!packaged) {
  const frontendEnv = { ...process.env };
  delete frontendEnv.VITE_CHECK_MODE;
  run(process.execPath, [path.join(root, "node_modules/vite/bin/vite.js"), "build", "--configLoader", "runner", "--outDir", path.join(server, "dist")], { cwd: root, env: frontendEnv });
  const tags = process.platform === "linux" ? "desktop,e2e,production,webkit2_41" : "desktop,e2e,production";
  const compiled = path.join(temporary, filename);
  const nativeEnv = { ...process.env, CGO_ENABLED: "1" };
  if (process.platform === "darwin") {
    // Match the native frameworks added by the Wails CLI to production builds.
    nativeEnv.CGO_LDFLAGS = `${process.env.CGO_LDFLAGS || ""} -framework UniformTypeIdentifiers -mmacosx-version-min=10.13`;
  }
  run("go", ["build", "-tags", tags, "-o", compiled, "."], { env: nativeEnv });
  executable = compiled;
  if (process.platform === "darwin") {
    const bundle = path.join(temporary, "YouTube Vault.app");
    cpSync(path.join(server, "build/bin/YouTube Vault.app"), bundle, { recursive: true });
    executable = path.join(bundle, "Contents/MacOS/youtube-vault");
    cpSync(compiled, executable);
    run("codesign", ["--force", "--deep", "--sign", "-", bundle]);
  }
}
if (packaged) {
  // Verify the exact artifact's embedded UI, database, metadata and restart path
  // through its supported diagnostic mode, using the private fixture above.
  const identity = run(executable, ["--version"], { stdio: ["ignore", "pipe", "inherit"] }).stdout.toString().trim();
  const match = /^youtube-downloader (\S+) \(commit (\S+), built (\S+)\)$/.exec(identity);
  assert.ok(match, `Unexpected packaged version: ${identity}`);
  if (process.env.VERSION) assert.equal(match[1], process.env.VERSION);
  if (process.env.COMMIT) assert.equal(match[2], process.env.COMMIT);
  if (process.env.BUILD_DATE) assert.equal(match[3], process.env.BUILD_DATE);
  run(process.execPath, [path.join(root, "scripts/release-smoke.mjs"), executable], {
    cwd: temporary,
    env: { ...process.env, RELEASE_VERSION: match[1], COMMIT: match[2], BUILD_DATE: match[3], YTDL_RELEASE_SMOKE_DATA_DIR: data },
  });
  // Local verification while the user's native application holds its global
  // single-instance lock. CI always exercises the window as well.
  if (process.argv.includes("--backend-only")) process.exit(0);
}
const output = path.join(temporary, "ui-ready.json");
const diagnosticLog = path.join(temporary, "desktop.log");
const env = { ...process.env, DATA_DIR: data, DESKTOP_SMOKE_OUTPUT: output, DESKTOP_LOG_FILE: diagnosticLog };
delete env.API_TOKEN;
delete env.WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS;
const child = spawn(executable, [], { cwd: temporary, env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
let logs = "";
child.stdout.on("data", chunk => { logs = (logs + chunk).slice(-12000); });
child.stderr.on("data", chunk => { logs = (logs + chunk).slice(-12000); });
child.on("error", error => { logs += String(error); });
try {
  const deadline = Date.now() + 90000;
  const ready = () => {
    if (!packaged) return existsSync(output);
    if (existsSync(diagnosticLog)) logs = readFileSync(diagnosticLog, "utf8");
    return /Desktop WebView DOM ready/.test(logs) && /Desktop Connection binding unblocked/.test(logs) && /Desktop service initialized: true/.test(logs);
  };
  while (!ready() && child.exitCode === null && child.signalCode === null && Date.now() < deadline) {
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  const stagesPath = `${output}.stages`;
  const stages = existsSync(stagesPath) ? readFileSync(stagesPath, "utf8").trim() : "none (no JavaScript-to-Go stage reports)";
  assert.ok(ready(), `Native UI did not connect to the Go service.\nE2E milestones: ${stages}\n${logs}`);
  if (packaged) {
    assert.equal(child.exitCode, null, `Packaged desktop application exited unexpectedly.\n${logs}`);
    console.log(`Packaged native window mounted and obtained its service connection (${process.platform}/${process.arch}).`);
  } else {
    assert.equal(JSON.parse(readFileSync(output, "utf8")).ready, true);
    const closed = await Promise.race([
      new Promise(resolve => child.exitCode === null ? child.once("exit", code => resolve(code === 0)) : resolve(child.exitCode === 0)),
      new Promise(resolve => setTimeout(() => resolve(false), 10000).unref()),
    ]);
    assert.ok(closed, `Desktop window did not shut down cleanly.\n${logs}`);
    console.log(`Native Wails UI connected and shut down cleanly (${process.platform}/${process.arch}).`);
    console.log(`Test evidence: ${output}`);
  }
} finally {
  if (child.exitCode === null) child.kill("SIGTERM");
}
