import { readFileSync } from "node:fs";
import { parseRuntimeModelPins, runtimeModelPinsPath } from "./lib/runtime-model-pins.mjs";
import { translationComponents } from "./lib/live-translation-inputs.mjs";

const requirementsPath = "python/funasr-runner/requirements.txt";
const runtimeManifest = JSON.parse(readFileSync("packages/desktop-resources/runtime/runtime-manifest.json", "utf8"));
const lockFiles = runtimeManifest.platforms
  .filter((platform) => platform.asrBackend === "funasr" && !platform.unsupportedReason)
  .map((platform) => `python/funasr-runner/requirements-lock-${platform.platformId}.txt`)
  .sort();
const installerSource = readFileSync("apps/native/internal/infrastructure/managed/installer.go", "utf8");
const errors = [];
const directPins = parseDirectPins(readFileSync(requirementsPath, "utf8"), errors);

if (lockFiles.length === 0 || new Set(lockFiles).size !== lockFiles.length) {
  errors.push("The shared runtime manifest must identify supported FunASR lock platforms exactly once");
}

for (const lockFile of lockFiles) {
  const locked = parseHashLock(lockFile, readFileSync(lockFile, "utf8"), errors);
  for (const [name, version] of directPins) {
    if (locked.get(name) !== version) {
      errors.push(`${lockFile} must contain ${name}==${version}`);
    }
  }
  for (const name of ["numpy", "pyyaml", "sentencepiece"]) {
    if (!locked.has(name)) errors.push(`${lockFile} must include the local translation dependency ${name}`);
  }
  const yaml = locked.get("pyyaml")?.split(".").map(Number);
  if (!yaml || yaml[0] < 5 || yaml[0] >= 7 || (yaml[0] === 5 && yaml[1] < 3)) errors.push(`${lockFile} is incompatible with the local translation PyYAML constraint`);
}
try { translationComponents(runtimeManifest); } catch (error) { errors.push(error.message); }

if (!installerSource.includes('"requirements-lock-" + i.Platform.ID + ".txt"')) {
  errors.push("Native runtime installer must select the lock matching the shared platform ID");
}
for (const requiredFlag of ["--require-hashes", "--no-build-isolation"]) {
  if (!installerSource.includes(`"${requiredFlag}"`)) {
    errors.push(`Native Python dependency installation must use ${requiredFlag}`);
  }
}
try {
  parseRuntimeModelPins(readFileSync(runtimeModelPinsPath, "utf8"));
} catch {
  errors.push("The shared model registry must list four ordered FunASR roles with distinct immutable 40-character commit hashes and valid model IDs");
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join("\n"));
  process.exit(1);
}

console.log(`Native runtime dependencies are hash-locked for ${lockFiles.length} manifest-supported FunASR platforms.`);

function parseDirectPins(contents, errors) {
  const pins = new Map();
  for (const rawLine of contents.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) {
      continue;
    }
    const match = line.match(/^([A-Za-z0-9_.-]+)==([A-Za-z0-9_.+!-]+)$/);
    if (!match) {
      errors.push(`${requirementsPath} contains a non-exact dependency: ${line}`);
      continue;
    }
    const name = normalizePackageName(match[1]);
    if (pins.has(name)) {
      errors.push(`${requirementsPath} contains duplicate dependency ${name}`);
    }
    pins.set(name, match[2]);
  }
  if (pins.size === 0) {
    errors.push(`${requirementsPath} contains no direct dependencies`);
  }
  return pins;
}

function parseHashLock(fileName, contents, errors) {
  const packages = new Map();
  let currentPackage = null;
  let currentHasHash = false;

  const finishPackage = () => {
    if (currentPackage && !currentHasHash) {
      errors.push(`${fileName} package ${currentPackage} has no SHA-256 hash`);
    }
  };

  for (const rawLine of contents.split(/\r?\n/)) {
    if (!rawLine.trim() || rawLine.trimStart().startsWith("#")) {
      continue;
    }
    if (/^\s/.test(rawLine)) {
      if (!currentPackage || !/^\s+--hash=sha256:[a-f0-9]{64}(?:\s+\\)?$/.test(rawLine)) {
        errors.push(`${fileName} contains an invalid hash continuation: ${rawLine.trim()}`);
      } else {
        currentHasHash = true;
      }
      continue;
    }

    finishPackage();
    const match = rawLine.match(/^([A-Za-z0-9_.-]+)==([^\s\\]+)\s+\\$/);
    if (!match) {
      errors.push(`${fileName} contains a non-exact or unhashed entry: ${rawLine}`);
      currentPackage = null;
      currentHasHash = false;
      continue;
    }
    const name = normalizePackageName(match[1]);
    if (packages.has(name)) {
      errors.push(`${fileName} contains duplicate package ${name}`);
    }
    packages.set(name, match[2]);
    currentPackage = name;
    currentHasHash = false;
  }
  finishPackage();
  if (packages.size === 0) {
    errors.push(`${fileName} contains no locked packages`);
  }
  return packages;
}

function normalizePackageName(value) {
  return value.toLowerCase().replaceAll(/[_.]+/g, "-");
}
