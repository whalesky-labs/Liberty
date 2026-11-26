import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { gzipSync, gunzipSync } from "node:zlib";
import { assertBloubFrames } from "./generate-native-bloub.mjs";

const artifact = await readFile(new URL("../apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz", import.meta.url));
const frames = gunzipSync(artifact);

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
