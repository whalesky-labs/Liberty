import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync, gunzipSync } from "node:zlib";
import { loadBloub, sampleBloub } from "./lib/bloub-animation.mjs";

export function encodeBloubFrames(sample) {
  // The Go renderer stores numeric frame geometry as float32. Normalize to
  // that exact precision before serializing so host math's last double bits
  // cannot change the embedded data. SVG path strings and clip metadata retain
  // their original values and remain subject to byte-for-byte drift checks.
  const clips = {};
  for (const [name, clip] of Object.entries(sample.clips)) {
    const frames = JSON.parse(JSON.stringify(clip.frames, (_, value) => typeof value === "number" ? Math.fround(value) : value));
    clips[name] = { ...clip, frames };
  }
  return Buffer.from(JSON.stringify({ ...sample, clips }));
}

export function assertBloubFrames(artifact, expected) {
  // Compression output can vary across host zlib builds. Verify the entire
  // uncompressed JSON byte-for-byte, including all frame values and metadata.
  const actual = gunzipSync(artifact, { maxOutputLength: expected.length + 1 });
  if (!actual.equals(expected)) {
    let at = 0;
    while (at < Math.min(actual.length, expected.length) && actual[at] === expected[at]) at++;
    throw new Error(`Bloub animation drift at JSON byte ${at}; run node scripts/generate-native-bloub.mjs`);
  }
}

export async function generateBloub({ root = fileURLToPath(new URL("../", import.meta.url)), check = false } = {}) {
  const output = path.join(root, "apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz");
  const sample = sampleBloub(await loadBloub(root));
  const payload = encodeBloubFrames(sample);
  let bytes;
  if (check) {
    bytes = await readFile(output);
    assertBloubFrames(bytes, payload);
  } else {
    bytes = gzipSync(payload, { level: 9 });
    // RFC 1952's OS byte describes the compressor host, not the frame payload.
    bytes[9] = 255;
    await writeFile(output, bytes);
  }
  console.log(`Native bloub frames match pinned upstream ${sample.source}: ${bytes.length} compressed bytes.`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await generateBloub({ check: process.argv.includes("--check") });
}
