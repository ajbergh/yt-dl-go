import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { packageLinux } from "./package-desktop-linux.mjs";
import { embedMacOSNotices } from "./package-desktop-installers.mjs";
import { verifyDesktopCoverage } from "./release-desktop-coverage.mjs";
import { completeDesktopLicenseReport, resolveNSIS } from "./desktop-build-support.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

test("NSIS discovery validates overrides and finds PATH or Program Files installations before packaging", () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "desktop-nsis-test-"));
  try {
    const compiler = path.join(directory, "makensis.exe");
    const env = { PATH: "", ProgramFiles: directory, "ProgramFiles(x86)": directory };
    const resolve = env => resolveNSIS(env, directory);
    assert.throws(() => resolve(env), /Install NSIS 3\.11/);
    assert.throws(() => resolve({ ...env, NSIS_PATH: directory }), /not a compiler file/);
    writeFileSync(compiler, "fixture");
    assert.equal(resolve({ ...env, NSIS_PATH: compiler }), compiler);
    assert.equal(resolve({ ...env, PATH: `"${directory}"` }), compiler);
    assert.throws(() => resolve({ ...env, PATH: directory, NSIS_PATH: path.join(directory, "missing.exe") }), /NSIS_PATH/);
    const portable = path.join(directory, ".tools/nsis/nsis-3.11/makensis.exe");
    mkdirSync(path.dirname(portable), { recursive: true });
    writeFileSync(portable, "fixture");
    assert.equal(resolve(env), portable);
    mkdirSync(path.join(directory, "NSIS"));
    const installed = path.join(directory, "NSIS/makensis.exe");
    writeFileSync(installed, "fixture");
    assert.equal(resolve(env), installed);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("desktop report preserves scanned dependencies and supplies local replacement provenance without remote lookup", () => {
  const report = 'example.org/module,https://example.org/LICENSE,Apache-2.0\n';
  const license = readFileSync(path.join(root, "src/server/third_party/youtube/LICENSE"), "utf8");
  const result = completeDesktopLicenseReport(report, "v2.10.6", license);
  assert.ok(result.includes(report.trim()));
  assert.match(result, /^github\.com\/kkdai\/youtube\/v2,https:\/\/pkg\.go\.dev\/github\.com\/kkdai\/youtube\/v2@v2\.10\.6,MIT$/m);
  assert.throws(() => completeDesktopLicenseReport(report, "Unknown", license), /version/);
  assert.throws(() => completeDesktopLicenseReport(report, "v2.10.6", "unclassified license"), /MIT/);
  assert.throws(() => completeDesktopLicenseReport(result, "v2.10.6", license), /Unexpected/);
  assert.throws(() => completeDesktopLicenseReport("", "v2.10.6", license), /Unexpected/);
});

test("Release assembly requires portable and installer artifacts for every platform and architecture", () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "desktop-coverage-test-"));
  try {
    for (const os of ["windows", "darwin", "linux"]) {
      for (const arch of ["amd64", "arm64"]) {
        for (const extension of os === "windows" ? [".exe", ".zip", "-setup.exe"] : [".tar.gz", os === "darwin" ? ".pkg" : ".deb"]) {
          const file = path.join(directory, `youtube-vault-${os}-${arch}${extension}`);
          writeFileSync(file, "artifact");
          writeFileSync(file + ".sha256", "checksum");
        }
      }
    }
    verifyDesktopCoverage(directory);
    for (const missing of ["youtube-vault-windows-arm64-setup.exe", "youtube-vault-darwin-amd64.pkg", "youtube-vault-linux-arm64.tar.gz"]) {
      rmSync(path.join(directory, missing));
      assert.throws(() => verifyDesktopCoverage(directory), /Missing desktop release artifact/);
      writeFileSync(path.join(directory, missing), "artifact");
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("macOS installer app carries all required notices inside its signed bundle", () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "desktop-notices-test-"));
  try {
    mkdirSync(path.join(directory, "YouTube Vault.app/Contents/Resources"), { recursive: true });
    mkdirSync(path.join(directory, "desktop-licenses/dependency"), { recursive: true });
    writeFileSync(path.join(directory, "LICENSE"), "project license");
    writeFileSync(path.join(directory, "desktop-licenses/dependency/LICENSE"), "dependency license");
    embedMacOSNotices(directory);
    const notices = path.join(directory, "YouTube Vault.app/Contents/Resources/notices");
    assert.equal(readFileSync(path.join(notices, "LICENSE"), "utf8"), "project license");
    assert.equal(readFileSync(path.join(notices, "desktop-licenses/dependency/LICENSE"), "utf8"), "dependency license");
    assert.equal(existsSync(path.join(notices, "YouTube Vault.app")), false);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});
test("Debian package preserves notices, installs desktop integration, and uses scanned ABI dependencies", () => {
  const stage = mkdtempSync(path.join(os.tmpdir(), "desktop-packaging-test-"));
  try {
    const contents = path.join(stage, "contents");
    mkdirSync(path.join(contents, "licenses"), { recursive: true });
    writeFileSync(path.join(contents, "youtube-vault"), "fake native binary");
    writeFileSync(path.join(contents, "licenses/NOTICE"), "required attribution");
    let built = false;
    packageLinux({ root, stage, contents, output: stage, name: "test", arch: "arm64", version: "1.2.3", run(command, args) {
      if (command === "dpkg-shlibdeps") return "shlibs:Depends=libc6 (>= 2.38), libgtk-3-0t64, libwebkit2gtk-4.1-0\n";
      assert.equal(command, "dpkg-deb");
      assert.ok(args.includes("--root-owner-group"));
      const deb = path.join(stage, "deb");
      const control = readFileSync(path.join(deb, "DEBIAN/control"), "utf8");
      assert.match(control, /Architecture: arm64\n/);
      assert.match(control, /Version: 1\.2\.3\n/);
      assert.match(control, /Depends: libc6 \(>= 2\.38\), libgtk-3-0t64, libwebkit2gtk-4\.1-0\n/);
      assert.equal(readFileSync(path.join(deb, "usr/bin/youtube-vault"), "utf8"), "fake native binary");
      assert.equal(readFileSync(path.join(deb, "usr/share/doc/youtube-vault/licenses/NOTICE"), "utf8"), "required attribution");
      assert.equal(existsSync(path.join(deb, "usr/share/doc/youtube-vault/youtube-vault")), false);
      assert.match(readFileSync(path.join(deb, "usr/share/applications/youtube-vault.desktop"), "utf8"), /Exec=youtube-vault\r?\n/);
      const icon = readFileSync(path.join(root, "src/server/build/linux/appicon.png"));
      const dimensions = `${icon.readUInt32BE(16)}x${icon.readUInt32BE(20)}`;
      assert.equal(dimensions, "256x256");
      assert.deepEqual(readFileSync(path.join(deb, "usr/share/icons/hicolor", dimensions, "apps/youtube-vault.png")), icon);
      built = true;
    } });
    assert.equal(built, true);
  } finally {
    rmSync(stage, { recursive: true, force: true });
  }
});

test("Debian packaging fails closed if runtime dependency scanning produces no result", () => {
  const stage = mkdtempSync(path.join(os.tmpdir(), "desktop-packaging-test-"));
  try {
    const contents = path.join(stage, "contents");
    mkdirSync(contents);
    writeFileSync(path.join(contents, "youtube-vault"), "binary");
    assert.throws(() => packageLinux({ root, stage, contents, output: stage, name: "test", arch: "amd64", version: "1.0.0", run: () => "" }), /did not report runtime dependencies/);
  } finally {
    rmSync(stage, { recursive: true, force: true });
  }
});
