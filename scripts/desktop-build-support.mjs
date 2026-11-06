import { statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const isFile = file => {
  try { return statSync(file).isFile(); } catch { return false; }
};

export function resolveNSIS(env = process.env, repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")) {
  if (env.NSIS_PATH) {
    if (!isFile(env.NSIS_PATH)) throw new Error(`NSIS_PATH is not a compiler file: ${env.NSIS_PATH}`);
    return path.resolve(env.NSIS_PATH);
  }
  const candidates = [
    ...(env.PATH || "").split(path.delimiter).filter(Boolean).map(directory => path.join(directory.replace(/^"|"$/g, ""), "makensis.exe")),
    ...[env["ProgramFiles(x86)"] || "C:/Program Files (x86)", env.ProgramFiles || "C:/Program Files"].map(directory => path.join(directory, "NSIS", "makensis.exe")),
    path.join(repoRoot, ".tools/nsis/nsis-3.11/makensis.exe"),
  ];
  const compiler = candidates.find(isFile);
  if (!compiler) throw new Error("NSIS compiler not found. Install NSIS 3.11 (choco install nsis --version=3.11 --yes), or set NSIS_PATH to a portable makensis.exe.");
  return path.resolve(compiler);
}

// check/save still scan the local replacement. Omit it only from the report's
// remote URL lookup, then supply its declared upstream version and retained MIT
// license explicitly; go-licenses cannot resolve local replacement URLs.
export function completeDesktopLicenseReport(report, youtubeVersion, youtubeLicense) {
  const module = "github.com/kkdai/youtube/v2";
  if (!/^v\d+\.\d+\.\d+(?:[-+][\w.+-]+)?$/.test(youtubeVersion)) throw new Error("Invalid YouTube upstream version");
  if (!youtubeLicense.trimStart().startsWith("The MIT License (MIT)") || !youtubeLicense.includes("Permission is hereby granted, free of charge")) throw new Error("Local YouTube replacement must retain its MIT license");
  const rows = report.trim().split(/\r?\n/).filter(Boolean);
  if (!rows.length || rows.some(row => row.includes(module))) throw new Error("Unexpected desktop license report contents");
  rows.push(`${module},https://pkg.go.dev/${module}@${youtubeVersion},MIT`);
  return rows.sort().join("\n") + "\n";
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(resolveNSIS()); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
