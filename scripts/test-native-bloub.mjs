import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { gzipSync, gunzipSync } from "node:zlib";
import { assertBloubFrames, generateBloub } from "./generate-native-bloub.mjs";

const artifact = await readFile(new URL("../apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz", import.meta.url));
const frames = gunzipSync(artifact);

test("imported generation rebuilds missing frames and check mode rejects stale data", async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), "native-bloub-generation-"));
  context.after(() => rm(root, { recursive: true, force: true }));
  await cp(new URL("../packages/desktop-resources/presentation/bloub", import.meta.url), path.join(root, "packages/desktop-resources/presentation/bloub"), { recursive: true });
  const output = path.join(root, "apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz");
  await mkdir(path.dirname(output), { recursive: true });
  await assert.rejects(generateBloub({ root, check: true }), { code: "ENOENT" });
  await generateBloub({ root });
  assertBloubFrames(await readFile(output), frames);
  await generateBloub({ root, check: true });
  await writeFile(output, gzipSync(Buffer.from('{"frames":[]}')));
  await assert.rejects(generateBloub({ root, check: true }), /Bloub animation drift/);
});

test("frame verification accepts host compression differences without changing JSON", () => {
  const alternate = gzipSync(frames, { level: 1 });
  alternate[9] = 3;
  assert.notDeepEqual(alternate, artifact);
  assertBloubFrames(alternate, frames);
  assertBloubFrames(artifact, frames);
});

test("frame verification rejects a valid gzip with changed animation metadata", () => {
  const changed = Buffer.from(frames.toString().replace('"fps":30', '"fps":29'));
  assert.notDeepEqual(changed, frames);
  assert.throws(() => assertBloubFrames(gzipSync(changed), frames), /Bloub animation drift/);
});

test("frame verification rejects corrupted and oversized uncompressed content", () => {
  const corrupt = Buffer.from(artifact);
  corrupt[corrupt.length - 8] ^= 1;
  assert.throws(() => assertBloubFrames(corrupt, frames));
  assert.throws(() => assertBloubFrames(gzipSync(Buffer.concat([frames, Buffer.from("xx")])), frames));
});
