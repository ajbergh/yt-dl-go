import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { packageLinux } from "./package-desktop-linux.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
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
      assert.match(readFileSync(path.join(deb, "usr/share/applications/youtube-vault.desktop"), "utf8"), /Exec=youtube-vault\n/);
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
