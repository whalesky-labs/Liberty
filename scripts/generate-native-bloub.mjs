import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { loadBloub, sampleBloub } from "./lib/bloub-animation.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const output = path.join(root, "apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz");
const sample = sampleBloub(await loadBloub(root));
const bytes = gzipSync(Buffer.from(JSON.stringify(sample)), { level: 9 });
if (process.argv.includes("--check")) {
  if (!(await readFile(output)).equals(bytes)) throw new Error("Bloub animation drift; run node scripts/generate-native-bloub.mjs");
} else await writeFile(output, bytes);
console.log(`Native bloub frames match pinned upstream ${sample.source}: ${bytes.length} compressed bytes.`);
