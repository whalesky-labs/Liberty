import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { gzipSync, gunzipSync } from "node:zlib";
import { assertBloubFrames, encodeBloubFrames, generateBloub } from "./generate-native-bloub.mjs";

const artifact = await readFile(new URL("../apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz", import.meta.url));
const frames = gunzipSync(artifact);

test("frame encoding normalizes only numeric geometry to the renderer's float32 precision", () => {
  const sample = { version: 2, source: "pinned-source", clips: { idle: { fps: 30, frames: [{ eyes: [{ alpha: 0.7626953125000056, matrix: "matrix(1,0,0,1,0,0)" }], bodyPath: "M0 0L1 1Z" }] } } };
  const expected = encodeBloubFrames(sample);
  const equivalent = structuredClone(sample);
  equivalent.clips.idle.frames[0].eyes[0].alpha = 0.7626953124999999;
  assert.ok(encodeBloubFrames(equivalent).equals(expected));
  assert.equal(JSON.parse(expected).clips.idle.frames[0].eyes[0].alpha, Math.fround(sample.clips.idle.frames[0].eyes[0].alpha));
  for (const mutate of [
    value => { value.clips.idle.frames[0].eyes[0].alpha += 2 ** -24; },
    value => { value.clips.idle.frames[0].bodyPath = "M0 0L1 1.01Z"; },
    value => { value.clips.idle.fps += Number.EPSILON * 30; },
    value => { value.source = "different-source"; },
  ]) {
    const changed = structuredClone(sample);
    mutate(changed);
    assert.throws(() => assertBloubFrames(gzipSync(encodeBloubFrames(changed)), expected), /Bloub animation drift|Cannot create a Buffer larger/);
  }
});

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
