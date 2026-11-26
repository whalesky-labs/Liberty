import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import vm from "node:vm";
import ts from "typescript";

export const bloubDirectory = "packages/desktop-resources/presentation/bloub";

// Execute only the pinned framework-free engine at build time. Imports resolve
// from its verified source set, with no host require, process, DOM or network.
export async function loadBloub(root) {
  const directory = path.join(root, bloubDirectory);
  const source = JSON.parse(await readFile(path.join(directory, "source.json"), "utf8"));
  const files = new Map();
  for (const [name, hash] of Object.entries(source.files)) {
    if (!/^(LICENSE|upstream\/[a-z]+\.ts)$/.test(name)) throw new Error(`Invalid bloub source path: ${name}`);
    const bytes = await readFile(path.join(directory, name));
    if (createHash("sha256").update(bytes).digest("hex") !== hash) throw new Error(`Bloub source drift: ${name}`);
    files.set(name, bytes.toString("utf8"));
  }
  const context = vm.createContext({}, { codeGeneration: { strings: false, wasm: false } });
  const cache = new Map();
  function load(name) {
    if (cache.has(name)) return cache.get(name).exports;
    const input = files.get(`upstream/${name}.ts`);
    if (!input) throw new Error(`Unexpected bloub import: ${name}`);
    const code = ts.transpileModule(input, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
    const module = { exports: {} };
    cache.set(name, module);
    const require = (specifier) => {
      if (!/^\.\/[a-z]+$/.test(specifier)) throw new Error(`External bloub import: ${specifier}`);
      return load(specifier.slice(2));
    };
    new vm.Script(`(function(exports, require, module) { ${code}\n})`, { filename: `${name}.ts` }).runInContext(context, { timeout: 5000 })(module.exports, require, module);
    return module.exports;
  }
  return { source, ...load("engine"), ...load("repere"), ...load("expressions"), ...load("states"), ...load("decor") };
}

export function sampleBloub({ source, BotEngine, RAYON, DEMI_VIEWBOX, EXPRESSION_BY_ID, SEQUENCE, STATE_BY_ID, NOTIF_BLUE }) {
  const clips = {};
  function clip(name, state, seconds, expression, at) {
    const engine = new BotEngine(RAYON, state, null, expression ? EXPRESSION_BY_ID.get(expression) : null);
    const count = seconds ? Math.round(seconds * 30) : 1;
    const frames = Array.from({ length: count }, (_, index) => engine.sample(at ?? index / 30));
    clips[name] = { fps: seconds ? 30 : 0, frames };
  }
  clip("idle", "idle", 12);
  clip("comet", "comet", 3);
  clip("thinking", "thinking", 3);
  clip("paused", "idle", 0, "somnolent", 1);
  clip("failed", "idle", 0, "triste", 1);

  // Use the same ordered catalogue and durations as the original player. One
  // engine carries transitions across state boundaries, including comet -> idle
  // at the loop seam; independently sampled clips would visibly jump between poses.
  const sequence = [];
  const frames = [];
  const last = SEQUENCE.at(-1);
  const engine = new BotEngine(RAYON, last);
  engine.reset(last, -STATE_BY_ID.get(last).duration);
  for (const state of SEQUENCE) {
    const firstFrame = frames.length;
    const count = Math.round(STATE_BY_ID.get(state).duration * 30);
    sequence.push({ state, firstFrame, frames: count });
    engine.setState(state, firstFrame / 30);
    for (let i = 0; i < count; i++) frames.push(engine.sample((firstFrame + i) / 30));
  }
  clips.showcase = { fps: 30, sequence, frames };
  return { version: 2, source: source.commit, viewBox: DEMI_VIEWBOX * 2, radius: RAYON, notificationColor: NOTIF_BLUE, clips };
}
