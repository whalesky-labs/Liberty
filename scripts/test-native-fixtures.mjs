import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { gzipSync } from "node:zlib";
import { unpackNativeFixtures } from "./lib/native-fixtures.mjs";

const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
async function temporary(context) {
  const root = await mkdtemp(path.join(os.tmpdir(), "liberty-fixture-test-"));
  context.after(() => rm(root, { recursive: true, force: true }));
  const output = path.join(root, "output");
  await mkdir(output);
  return { root, output };
}

test("unpacks and verifies the retired client's synthetic migration and document samples", async (context) => {
  const { output } = await temporary(context);
  const manifest = await unpackNativeFixtures(output);
  assert.equal(manifest.synthetic, true);
  assert.equal(manifest.schemaVersion, 10);
  assert.equal((await readFile(path.join(output, "liberty.sqlite3"))).subarray(0, 16).toString(), "SQLite format 3\0");
  const expected = JSON.parse(await readFile(path.join(output, "expected.json"), "utf8"));
  assert.equal(expected.jobs.length, 14);
  assert.ok(manifest.files.some((file) => file.path === "rust-members.xlsx"));
  assert.ok(manifest.files.some((file) => file.path === "rust-escaped-members.xlsx"));
  assert.ok(manifest.files.some((file) => file.path === "reads.sqlite3"));
});

// Build a small independent ustar record to exercise hostile, authenticated
// archive inputs without rewriting or copying the large historical samples.
async function fixture(context, { name = "sample.json", type = "0", corrupt = false, missing = false, wrongDigest = false } = {}) {
  const { root, output } = await temporary(context);
  const source = path.join(root, "source");
  await mkdir(source);
  const bytes = Buffer.from("{}\n");
  const tar = Buffer.alloc(2048);
  tar.write(name, 0, 100);
  tar.write("0000600\0", 100);
  tar.write("0000000\0", 108);
  tar.write("0000000\0", 116);
  tar.write("00000000003\0", 124);
  tar.write("00000000000\0", 136);
  tar.fill(32, 148, 156);
  tar.write(type, 156);
  tar.write("ustar\x00", 257);
  const checksum = tar.subarray(0, 512).reduce((sum, byte) => sum + byte, 0);
  tar.write(`${checksum.toString(8).padStart(6, "0")}\0 `, 148);
  bytes.copy(tar, 512);
  const archive = gzipSync(tar);
  const manifest = {
    format: 1, schemaVersion: 10, referenceSourceSHA256: "a".repeat(64),
    archive: { path: "samples.tar.gz", bytes: archive.length, sha256: hash(archive) },
    files: [{ path: "sample.json", bytes: bytes.length, sha256: wrongDigest ? "b".repeat(64) : hash(bytes) }],
  };
  if (missing) manifest.files.push({ path: "missing.json", bytes: 0, sha256: hash("") });
  if (corrupt) archive[0] ^= 1;
  await writeFile(path.join(source, "samples.tar.gz"), archive);
  await writeFile(path.join(source, "manifest.json"), JSON.stringify(manifest));
  return { source, output };
}

for (const [name, options, message] of [
  ["corrupt compressed bytes", { corrupt: true }, /archive integrity/],
  ["path traversal", { name: "../outside.json" }, /archive entry/],
  ["symbolic links", { type: "2" }, /archive entry/],
  ["hard links", { type: "1" }, /archive entry/],
  ["missing records", { missing: true }, /incomplete/],
  ["wrong file digests", { wrongDigest: true }, /file integrity/],
]) {
  test(`rejects ${name} before writing any fixture`, async (context) => {
    const { source, output } = await fixture(context, options);
    await assert.rejects(unpackNativeFixtures(output, source), message);
    assert.deepEqual(await readdir(output), []);
  });
}

test("refuses to overwrite an existing fixture directory", async (context) => {
  const { source, output } = await fixture(context);
  await writeFile(path.join(output, "sample.json"), "keep");
  await assert.rejects(unpackNativeFixtures(output, source), /empty isolated directory/);
  assert.equal(await readFile(path.join(output, "sample.json"), "utf8"), "keep");
});
