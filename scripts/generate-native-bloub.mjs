import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync, gunzipSync } from "node:zlib";
import { loadBloub, sampleBloub } from "./lib/bloub-animation.mjs";

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

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = fileURLToPath(new URL("../", import.meta.url));
  const output = path.join(root, "apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz");
  const sample = sampleBloub(await loadBloub(root));
  const payload = Buffer.from(JSON.stringify(sample));
  let bytes;
  if (process.argv.includes("--check")) {
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
