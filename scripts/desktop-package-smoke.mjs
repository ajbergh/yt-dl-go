import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const goos = { win32: "windows", darwin: "darwin", linux: "linux" }[process.platform];
const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
const name = `youtube-vault-${goos}-${arch}`;
const output = path.join(path.resolve(root, process.env.DESKTOP_OUTPUT_ROOT || "dist/desktop"), `${goos}-${arch}`);
function run(command, args, options = {}) {
  const result = spawnSync(command, args, { stdio: "inherit", ...options });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, `${command} failed (${result.status})`);
}
for (const file of readdirSync(output).filter(file => /\.(exe|zip|tar\.gz|deb)$/.test(file))) {
  const digest = createHash("sha256").update(readFileSync(path.join(output, file))).digest("hex");
  assert.equal(readFileSync(path.join(output, file + ".sha256"), "utf8").trim(), `${digest}  ${file}`);
}
const temporary = mkdtempSync(path.join(os.tmpdir(), "yt-dl-go-desktop-package-"));
try {
  const archive = path.join(output, name + (goos === "windows" ? ".zip" : ".tar.gz"));
  if (goos === "windows") {
    run("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; Expand-Archive -LiteralPath $env:DESKTOP_PACKAGE_ARCHIVE -DestinationPath $env:DESKTOP_PACKAGE_STAGE"], {
      env: { ...process.env, DESKTOP_PACKAGE_ARCHIVE: archive, DESKTOP_PACKAGE_STAGE: temporary },
    });
  } else {
    run("tar", ["-xzf", archive, "-C", temporary]);
  }
  const contents = path.join(temporary, name);
  for (const file of ["LICENSE", "THIRD_PARTY_NOTICES.md", "DESKTOP_GO_LICENSES.csv", "desktop-licenses", "licenses"]) assert.ok(existsSync(path.join(contents, file)), `Package missing ${file}`);
  const executable = goos === "darwin"
    ? path.join(contents, "YouTube Vault.app/Contents/MacOS/youtube-vault")
    : path.join(contents, "youtube-vault" + (goos === "windows" ? ".exe" : ""));
  if (goos === "windows") assert.deepEqual(readFileSync(executable), readFileSync(path.join(output, name + ".exe")));
  run(process.execPath, [path.join(root, "scripts/desktop-smoke.mjs"), "--executable", executable, ...process.argv.slice(2)]);
  if (goos === "linux") {
    const debRoot = path.join(temporary, "deb");
    run("dpkg-deb", ["-x", path.join(output, name + ".deb"), debRoot]);
    assert.deepEqual(readFileSync(path.join(debRoot, "usr/bin/youtube-vault")), readFileSync(executable));
    run("desktop-file-validate", [path.join(debRoot, "usr/share/applications/youtube-vault.desktop")]);
  }
  console.log(`Verified final desktop package (${goos}/${arch}).`);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
