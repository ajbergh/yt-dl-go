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
  const who = run("whoami.exe", ["/user", "/fo", "csv", "/nh"], { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] });
  const sid = who.stdout.match(/S-1-5-[\d-]+/)?.[0];
  if (!sid) throw new Error("Cannot identify the smoke-test data owner");
  run("icacls.exe", [data, "/inheritance:r", "/grant:r", `*${sid}:(OI)(CI)F`]);
}
const filename = "youtube-vault" + (process.platform === "win32" ? ".exe" : "");
const frontendEnv = { ...process.env };
delete frontendEnv.VITE_CHECK_MODE;
run(process.execPath, [path.join(root, "node_modules/vite/bin/vite.js"), "build", "--configLoader", "runner", "--outDir", path.join(server, "dist")], { cwd: root, env: frontendEnv });
const tags = process.platform === "linux" ? "desktop,e2e,production,webkit2_41" : "desktop,e2e,production";
const compiled = path.join(temporary, filename);
run("go", ["build", "-tags", tags, "-o", compiled, "."]);
let executable = compiled;
if (process.platform === "darwin") {
  const bundle = path.join(temporary, "YouTube Vault.app");
  cpSync(path.join(server, "build/bin/YouTube Vault.app"), bundle, { recursive: true });
  executable = path.join(bundle, "Contents/MacOS/youtube-vault");
  cpSync(compiled, executable);
  run("codesign", ["--force", "--deep", "--sign", "-", bundle]);
}
const output = path.join(temporary, "ui-ready.json");
const env = { ...process.env, DATA_DIR: data, DESKTOP_SMOKE_OUTPUT: output };
delete env.API_TOKEN;
delete env.WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS;
const child = spawn(executable, [], { cwd: root, env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
let logs = "";
child.stdout.on("data", chunk => { logs = (logs + chunk).slice(-12000); });
child.stderr.on("data", chunk => { logs = (logs + chunk).slice(-12000); });
child.on("error", error => { logs += String(error); });
try {
  const deadline = Date.now() + 90000;
  while (!existsSync(output) && child.exitCode === null && Date.now() < deadline) {
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  assert.ok(existsSync(output), `Native UI did not connect to the Go service.\n${logs}`);
  assert.equal(JSON.parse(readFileSync(output, "utf8")).ready, true);
  const closed = await Promise.race([
    new Promise(resolve => child.exitCode === null ? child.once("exit", code => resolve(code === 0)) : resolve(child.exitCode === 0)),
    new Promise(resolve => setTimeout(() => resolve(false), 10000).unref()),
  ]);
  assert.ok(closed, `Desktop window did not shut down cleanly.\n${logs}`);
  console.log(`Native Wails UI connected and shut down cleanly (${process.platform}/${process.arch}).`);
  console.log(`Test evidence: ${output}`);
} finally {
  if (child.exitCode === null) child.kill("SIGTERM");
}
