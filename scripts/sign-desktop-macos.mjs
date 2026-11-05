// Invoked on the staged .app before archiving/checksums, only when explicitly
// enabled on a protected tag-release job. Never echo signing credentials.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { copyFileSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";

assert.equal(process.platform, "darwin", "Signing requires macOS");
assert.equal(process.env.DESKTOP_MACOS_SIGN, "true", "Signing was not explicitly enabled");
assert.ok(process.env.GITHUB_REF?.startsWith("refs/tags/v"), "Signing requires a release tag");
const app = path.resolve(process.argv[2]);
const isInstaller = app.endsWith(".pkg");
const certificateKey = isInstaller ? "APPLE_INSTALLER_CERTIFICATE_P12_BASE64" : "APPLE_CERTIFICATE_P12_BASE64";
const passwordKey = isInstaller ? "APPLE_INSTALLER_CERTIFICATE_PASSWORD" : "APPLE_CERTIFICATE_PASSWORD";
const identityKey = isInstaller ? "APPLE_INSTALLER_SIGNING_IDENTITY" : "APPLE_SIGNING_IDENTITY";
for (const name of [certificateKey, passwordKey, identityKey, "APPLE_ID", "APPLE_TEAM_ID", "APPLE_APP_PASSWORD"]) {
  if (!process.env[name]) throw new Error(`Missing protected signing secret ${name}`);
}
const temporary = mkdtempSync(path.join(os.tmpdir(), "yt-dl-go-signing-"));
const keychain = path.join(temporary, "signing.keychain-db");
const password = randomBytes(32).toString("hex");
function run(command, args, capture = false) {
  const result = spawnSync(command, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  if (result.error || result.status !== 0) throw new Error(`${command} failed during macOS signing (exit ${result.status})`);
  return capture ? result.stdout : undefined;
}
try {
  const certificate = path.join(temporary, "identity.p12");
  writeFileSync(certificate, Buffer.from(process.env[certificateKey], "base64"), { mode: 0o600 });
  run("security", ["create-keychain", "-p", password, keychain]);
  run("security", ["set-keychain-settings", "-lut", "21600", keychain]);
  run("security", ["unlock-keychain", "-p", password, keychain]);
  run("security", ["import", certificate, "-k", keychain, "-P", process.env[passwordKey], "-T", isInstaller ? "/usr/bin/productsign" : "/usr/bin/codesign"]);
  run("security", ["set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-k", password, keychain]);
  // This Wails bundle contains a single Mach-O executable and system frameworks.
  if (isInstaller) {
    const signed = path.join(temporary, "signed.pkg");
    run("productsign", ["--timestamp", "--keychain", keychain, "--sign", process.env[identityKey], app, signed]);
    copyFileSync(signed, app);
    run("pkgutil", ["--check-signature", app]);
  } else {
    run("codesign", ["--force", "--options", "runtime", "--timestamp", "--keychain", keychain, "--sign", process.env[identityKey], app]);
    run("codesign", ["--verify", "--deep", "--strict", app]);
  }
  run("xcrun", ["notarytool", "store-credentials", "desktop-notary", "--keychain", keychain, "--apple-id", process.env.APPLE_ID, "--team-id", process.env.APPLE_TEAM_ID, "--password", process.env.APPLE_APP_PASSWORD]);
  const zip = path.join(temporary, "application.zip");
  if (!isInstaller) run("ditto", ["-c", "-k", "--keepParent", app, zip]);
  const submission = JSON.parse(run("xcrun", ["notarytool", "submit", isInstaller ? app : zip, "--keychain-profile", "desktop-notary", "--keychain", keychain, "--wait", "--timeout", "20m", "--output-format", "json"], true));
  assert.equal(submission.status, "Accepted", "Apple did not accept notarization");
  run("xcrun", ["stapler", "staple", app]);
  run("xcrun", ["stapler", "validate", app]);
  run("spctl", ["--assess", "--type", isInstaller ? "install" : "execute", app]);
  console.log("Developer ID signature and stapled notarization verified.");
} finally {
  spawnSync("security", ["delete-keychain", keychain], { stdio: "ignore" });
  rmSync(temporary, { recursive: true, force: true });
}
