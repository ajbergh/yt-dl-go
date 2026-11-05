import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function verifyDesktopCoverage(directory) {
  for (const os of ["windows", "darwin", "linux"]) {
    for (const arch of ["amd64", "arm64"]) {
      const name = `youtube-vault-${os}-${arch}`;
      const extensions = os === "windows" ? [".exe", ".zip", "-setup.exe"] : [".tar.gz", os === "darwin" ? ".pkg" : ".deb"];
      for (const extension of extensions) {
        const file = path.join(directory, name + extension);
        assert.ok(existsSync(file), `Missing desktop release artifact: ${path.basename(file)}`);
        assert.ok(existsSync(file + ".sha256"), `Missing checksum: ${path.basename(file)}`);
      }
    }
  }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  verifyDesktopCoverage(path.resolve(process.argv[2] || "dist/release"));
}
