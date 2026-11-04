import assert from "node:assert/strict";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Resvg } from "@resvg/resvg-js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = readFileSync(path.join(root, "public/app-icon.svg"));
const png = size => new Resvg(source, { fitTo: { mode: "width", value: size } }).render().asPng();
const sizes = [16, 24, 32, 48, 64, 128, 256];
const images = sizes.map(png);
const header = Buffer.alloc(6 + images.length * 16);
header.writeUInt16LE(1, 2);
header.writeUInt16LE(images.length, 4);
let offset = header.length;
for (let i = 0; i < sizes.length; i++) {
  const entry = 6 + i * 16;
  header[entry] = sizes[i] === 256 ? 0 : sizes[i];
  header[entry + 1] = header[entry];
  header.writeUInt16LE(1, entry + 4);
  header.writeUInt16LE(32, entry + 6);
  header.writeUInt32LE(images[i].length, entry + 8);
  header.writeUInt32LE(offset, entry + 12);
  offset += images[i].length;
}
const outputs = [
  ["src/server/build/appicon.png", png(1024)],
  ["src/server/build/windows/icon.ico", Buffer.concat([header, ...images])],
  ["public/app-icon.png", png(180)],
  ["public/favicon.ico", Buffer.concat([header, ...images])],
];
const check = process.argv.includes("--check");
for (const [relative, bytes] of outputs) {
  const destination = path.join(root, relative);
  if (check) {
    assert.ok(existsSync(destination) && readFileSync(destination).equals(bytes), `${relative} is stale; run npm run icons:generate`);
  } else {
    writeFileSync(destination, bytes);
  }
}
console.log(`Application icons ${check ? "verified" : "generated"} from public/app-icon.svg (PNG + ${sizes.join(", ")}px ICO).`);
