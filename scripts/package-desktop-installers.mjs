import { cpSync, existsSync, mkdirSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";

export function packageWindows({ root, contents, output, name, arch, version, run }) {
  const candidates = [process.env.NSIS_PATH, "C:/Program Files (x86)/NSIS/makensis.exe", "C:/Program Files/NSIS/makensis.exe"];
  const compiler = candidates.find(file => file && existsSync(file)) || "makensis.exe";
  const module = run("go", ["list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v2"], { stdio: ["ignore", "pipe", "inherit"] });
  const bootstrapper = path.join(module, "internal/webview2runtime/MicrosoftEdgeWebview2Setup.exe");
  const installer = path.join(output, name + "-setup.exe");
  run(compiler, ["/V3", `/DPAYLOAD=${contents}`, `/DOUTPUT=${installer}`, `/DARCH=${arch}`, `/DVERSION=${version}`,
    `/DICON=${path.join(root, "src/server/build/windows/icon.ico")}`, `/DWEBVIEW=${bootstrapper}`,
    path.join(root, "scripts/desktop-installer.nsi")]);
  if (!existsSync(installer)) throw new Error("NSIS did not produce the Windows installer. Install NSIS 3.11 or set NSIS_PATH to makensis.exe.");
  return installer;
}

export function packageMacOS({ stage, contents, output, name, arch, version, run }) {
  const payload = path.join(stage, "pkg-root");
  mkdirSync(path.join(payload, "Applications"), { recursive: true });
  cpSync(path.join(contents, "YouTube Vault.app"), path.join(payload, "Applications/YouTube Vault.app"), { recursive: true });
  const components = path.join(stage, "components.plist");
  run("pkgbuild", ["--analyze", "--root", payload, components]);
  // Never relocate an upgrade into a build checkout or an extracted portable app.
  run("/usr/libexec/PlistBuddy", ["-c", "Set :0:BundleIsRelocatable false", components]);
  const component = path.join(stage, "application.pkg");
  run("pkgbuild", ["--root", payload, "--component-plist", components, "--identifier", "io.github.ajbergh.ytdlgo.desktop", "--version", version, "--install-location", "/", component]);
  const distribution = path.join(stage, "Distribution.xml");
  writeFileSync(distribution, `<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
  <title>YouTube Vault</title>
  <options customize="never" require-scripts="false" hostArchitectures="${arch === "arm64" ? "arm64" : "x86_64"}"/>
  <domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>
  <choices-outline><line choice="app"/></choices-outline>
  <choice id="app" visible="false"><pkg-ref id="io.github.ajbergh.ytdlgo.desktop"/></choice>
  <pkg-ref id="io.github.ajbergh.ytdlgo.desktop" version="${version}">application.pkg</pkg-ref>
</installer-gui-script>
`);
  const installer = path.join(output, name + ".pkg");
  run("productbuild", ["--distribution", distribution, "--package-path", stage, installer]);
  if (!existsSync(installer)) throw new Error("productbuild did not produce the macOS installer");
  return installer;
}

export function embedMacOSNotices(contents) {
  const destination = path.join(contents, "YouTube Vault.app/Contents/Resources/notices");
  mkdirSync(destination, { recursive: true });
  for (const file of readdirSync(contents).filter(file => !file.endsWith(".app"))) {
    cpSync(path.join(contents, file), path.join(destination, file), { recursive: true });
  }
}
