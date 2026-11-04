import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Resvg } from "@resvg/resvg-js";

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
  if (goos === "windows") {
    const binary = readFileSync(executable);
    assert.deepEqual(binary, readFileSync(path.join(output, name + ".exe")));
    const ico = readFileSync(path.join(root, "src/server/build/windows/icon.ico"));
    const pe = binary.readUInt32LE(0x3c);
    assert.equal(binary.toString("ascii", pe, pe + 4), "PE\0\0");
    const sectionCount = binary.readUInt16LE(pe + 6);
    const sectionStart = pe + 24 + binary.readUInt16LE(pe + 20);
    let resources;
    for (let i = 0; i < sectionCount; i++) {
      const section = sectionStart + i * 40;
      if (binary.toString("ascii", section, section + 8).replace(/\0/g, "") !== ".rsrc") continue;
      const start = binary.readUInt32LE(section + 20);
      const size = binary.readUInt32LE(section + 16);
      resources = binary.subarray(start, start + size);
    }
    assert.ok(resources, "Windows EXE is missing its resource section");
    // Wails preserves the individual ICO images when embedding PE resources.
    // Search only resources: the frontend also embeds the matching favicon.
    for (let i = 0; i < ico.readUInt16LE(4); i++) {
      const entry = 6 + i * 16;
      const start = ico.readUInt32LE(entry + 12);
      const length = ico.readUInt32LE(entry + 8);
      assert.ok(resources.includes(ico.subarray(start, start + length)), `Windows EXE is missing icon image ${i}`);
    }
  }
  if (goos === "darwin") {
    const icon = readFileSync(path.join(contents, "YouTube Vault.app/Contents/Resources/iconfile.icns"));
    assert.equal(icon.toString("ascii", 0, 4), "icns");
    let largest;
    for (let position = 8; position < icon.length;) {
      const length = icon.readUInt32BE(position + 4);
      assert.ok(length > 8 && position + length <= icon.length, "Invalid ICNS image record");
      if (icon.toString("ascii", position, position + 4) === "ic10") largest = icon.subarray(position + 8, position + length);
      position += length;
    }
    assert.ok(largest, "macOS icon is missing its 1024px representation");
    const pixels = png => new Resvg(`<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024"><image width="1024" height="1024" href="data:image/png;base64,${png.toString("base64")}"/></svg>`).render().pixels;
    assert.deepEqual(pixels(largest), pixels(readFileSync(path.join(root, "src/server/build/appicon.png"))), "macOS bundle icon does not match the shared source");
  }
  run(process.execPath, [path.join(root, "scripts/desktop-smoke.mjs"), "--executable", executable, ...process.argv.slice(2)]);
  if (goos === "linux") {
    const debRoot = path.join(temporary, "deb");
    run("dpkg-deb", ["-x", path.join(output, name + ".deb"), debRoot]);
    assert.deepEqual(readFileSync(path.join(debRoot, "usr/bin/youtube-vault")), readFileSync(executable));
    const icon = readFileSync(path.join(root, "src/server/build/linux/appicon.png"));
    const dimensions = `${icon.readUInt32BE(16)}x${icon.readUInt32BE(20)}`;
    assert.deepEqual(readFileSync(path.join(debRoot, "usr/share/icons/hicolor", dimensions, "apps/youtube-vault.png")), icon);
    run("desktop-file-validate", [path.join(debRoot, "usr/share/applications/youtube-vault.desktop")]);
  }
  console.log(`Verified final desktop package (${goos}/${arch}).`);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
