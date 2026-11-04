import { chmodSync, cpSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

// Use Debian's shared-library scanner rather than guessing the runner's ABI.
export function packageLinux({ root, stage, contents, output, name, arch, version, run }) {
  const packageRoot = path.join(stage, "deb");
  const controlDir = path.join(packageRoot, "DEBIAN");
  const bin = path.join(packageRoot, "usr/bin");
  const applications = path.join(packageRoot, "usr/share/applications");
  const icon = path.join(root, "src/server/build/appicon.png");
  const iconBytes = readFileSync(icon);
  const dimensions = `${iconBytes.readUInt32BE(16)}x${iconBytes.readUInt32BE(20)}`;
  const icons = path.join(packageRoot, "usr/share/icons/hicolor", dimensions, "apps");
  const docs = path.join(packageRoot, "usr/share/doc/youtube-vault");
  for (const dir of [controlDir, bin, applications, icons, docs]) mkdirSync(dir, { recursive: true, mode: 0o755 });
  cpSync(path.join(contents, "youtube-vault"), path.join(bin, "youtube-vault"));
  chmodSync(path.join(bin, "youtube-vault"), 0o755);
  cpSync(path.join(root, "scripts/linux/youtube-vault.desktop"), path.join(applications, "youtube-vault.desktop"));
  cpSync(icon, path.join(icons, "youtube-vault.png"));
  cpSync(contents, docs, { recursive: true, filter: source => source !== path.join(contents, "youtube-vault") });
  const debianDir = path.join(stage, "debian");
  mkdirSync(debianDir);
  writeFileSync(path.join(debianDir, "control"), "Source: youtube-vault\n\nPackage: youtube-vault\nArchitecture: any\nDescription: YouTube Vault desktop application\n");
  const result = run("dpkg-shlibdeps", ["-O", path.join(bin, "youtube-vault")], { cwd: stage, stdio: ["ignore", "pipe", "inherit"] });
  const dependencies = result.split("\n").find(line => line.startsWith("shlibs:Depends="))?.slice("shlibs:Depends=".length);
  if (!dependencies) throw new Error("dpkg-shlibdeps did not report runtime dependencies");
  writeFileSync(path.join(controlDir, "control"), [
    "Package: youtube-vault", `Version: ${version}`, `Architecture: ${arch}`,
    "Maintainer: yt-dl-go contributors <noreply@github.com>",
    "Section: video", "Priority: optional", `Depends: ${dependencies}`,
    "Homepage: https://github.com/ajbergh/yt-dl-go", "Description: YouTube Vault native desktop application", "",
  ].join("\n"));
  const deb = path.join(output, name + ".deb");
  run("dpkg-deb", ["--build", "--root-owner-group", packageRoot, deb]);
  return deb;
}
