import { createHash } from "node:crypto";
import { lstat, mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gunzipSync } from "node:zlib";

const sourceDirectory = fileURLToPath(new URL("../../packages/desktop-resources/fixtures/native-v10/", import.meta.url));
const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");
const safePath = (name) => typeof name === "string" && name.length > 0 && !/[\\:\x00]/.test(name)
  && !name.startsWith("/") && name.split("/").every((part) => part && part !== "." && part !== "..");

// The fixture archive contains only closed synthetic databases and documents.
// Parse regular ustar records ourselves so tests need neither Rust nor tar on Windows.
export async function unpackNativeFixtures(directory, source = sourceDirectory) {
  if (!path.isAbsolute(directory) || !(await lstat(directory)).isDirectory() || (await readdir(directory)).length) {
    throw new Error("Native fixtures require an empty isolated directory");
  }
  const manifest = JSON.parse(await readFile(path.join(source, "manifest.json"), "utf8"));
  if (manifest.format !== 1 || manifest.schemaVersion !== 10 || !/^[a-f0-9]{64}$/.test(manifest.referenceSourceSHA256)
      || !Array.isArray(manifest.files) || !manifest.files.length || manifest.files.length > 4096
      || manifest.archive?.path !== "samples.tar.gz") throw new Error("Invalid native fixture manifest");
  const expected = new Map();
  for (const file of manifest.files) {
    if (!safePath(file.path) || !Number.isSafeInteger(file.bytes) || file.bytes < 0 || !/^[a-f0-9]{64}$/.test(file.sha256)
        || expected.has(file.path)) throw new Error("Invalid native fixture inventory");
    expected.set(file.path, file);
  }
  const compressed = await readFile(path.join(source, manifest.archive.path));
  if (compressed.length !== manifest.archive.bytes || digest(compressed) !== manifest.archive.sha256) {
    throw new Error("Native fixture archive integrity failed");
  }
  const archive = gunzipSync(compressed, { maxOutputLength: 512 * 1024 * 1024 });
  const records = [];
  for (let offset = 0; offset < archive.length;) {
    const header = archive.subarray(offset, offset + 512);
    if (header.length !== 512) throw new Error("Truncated native fixture archive");
    if (header.every((byte) => byte === 0)) {
      if (!archive.subarray(offset).every((byte) => byte === 0)) throw new Error("Invalid archive trailer");
      break;
    }
    const text = (start, end) => header.subarray(start, end).toString("utf8").replace(/\0.*$/s, "");
    const octal = (start, end) => {
      const value = text(start, end).trim();
      if (!/^[0-7]+$/.test(value)) throw new Error("Invalid native fixture tar number");
      return Number.parseInt(value, 8);
    };
    const checksum = [...header].reduce((sum, byte, index) => sum + (index >= 148 && index < 156 ? 32 : byte), 0);
    if (checksum !== octal(148, 156) || !text(257, 263).startsWith("ustar")) throw new Error("Invalid native fixture tar header");
    const prefix = text(345, 500);
    const name = `${prefix ? `${prefix}/` : ""}${text(0, 100)}`;
    const size = octal(124, 136);
    const type = text(156, 157);
    const file = expected.get(name);
    if (!safePath(name) || (type !== "0" && type !== "") || !file || size !== file.bytes
        || offset + 512 + size > archive.length) throw new Error("Unexpected native fixture archive entry");
    const bytes = archive.subarray(offset + 512, offset + 512 + size);
    if (digest(bytes) !== file.sha256) throw new Error("Native fixture file integrity failed");
    expected.delete(name);
    records.push({ name, bytes });
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  if (expected.size) throw new Error("Native fixture archive is incomplete");
  // Validate every byte and path before publishing any fixture.
  for (const { name, bytes } of records) {
    const destination = path.join(directory, ...name.split("/"));
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, bytes, { flag: "wx", mode: 0o600 });
  }
  return manifest;
}
