import { constants } from "node:fs";
import { createHash } from "node:crypto";
import { lstat, mkdir, open, readFile, readdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileDigest, nativeTargets, packageTarget } from "./native-package.mjs";

export const securityRuntimeProfile = "security-candidate";
export const funasrUpgradeProfile = "funasr-upgrade";
export const productionRuntimeProfile = "production";
export const vendorRuntimeFiles = Object.freeze(["funasr-1.3.9+liberty.local2-py3-none-any.whl", "METADATA.patch", "recipe.json", "provenance.json", "source.cdx.json"]);
export function vendorRuntimeFilesForProfile(profileID) {
  return profileID === funasrUpgradeProfile ? ["funasr-1.4.29+liberty.local1-py3-none-any.whl", ...vendorRuntimeFiles.slice(1)] : vendorRuntimeFiles;
}

export function runtimeInputProfile(manifest, profileID = securityRuntimeProfile) {
  if (profileID === productionRuntimeProfile) {
    const inputs = manifest.productionInputs;
    if (!inputs || inputs.builder?.fileName !== "build_vendor_wheel.py" || !Number.isSafeInteger(inputs.builder.bytes) || inputs.builder.bytes < 1 || inputs.builder.bytes > 128 * 1024 || !/^[a-f0-9]{64}$/.test(inputs.builder.sha256)) throw new Error("Unknown or incomplete production runtime build inputs");
    return { runtimeVersion: manifest.runtimeVersion, pythonVersion: manifest.pythonVersion, platforms: manifest.platforms?.filter((platform) => platform.asrBackend === "funasr" && !platform.unsupportedReason), files: inputs.files };
  }
  if (profileID !== securityRuntimeProfile && profileID !== funasrUpgradeProfile) throw new Error("Unknown runtime profile");
  return manifest.candidateProfiles?.[profileID];
}

export function runtimeInputPins(manifest, profileID = securityRuntimeProfile) {
  const profile = runtimeInputProfile(manifest, profileID);
  if (!profile || !Array.isArray(profile.platforms) || profile.platforms.length !== 3 || !Array.isArray(profile.files)) throw new Error("Unknown or incomplete runtime profile");
  if (new Set(profile.platforms.map((platform) => platform.platformId)).size !== 3 || profile.platforms.some((platform) => !Object.hasOwn(nativeTargets, platform.platformId))) throw new Error("Invalid candidate runtime platforms");
  const names = new Set([...vendorRuntimeFilesForProfile(profileID), ...profile.platforms.map((platform) => `requirements-lock-${platform.platformId}.txt`)]);
  const seen = new Set();
  let size = 0;
  for (const pin of profile.files) {
    if (!names.has(pin.fileName) || seen.has(pin.fileName) || !Number.isSafeInteger(pin.bytes) || pin.bytes < 1 || pin.bytes > 2 * 1024 * 1024 || !/^[a-f0-9]{64}$/.test(pin.sha256)) throw new Error("Invalid candidate runtime input pin");
    seen.add(pin.fileName);
    size += pin.bytes;
  }
  if (seen.size !== 8 || seen.size !== names.size || size > 8 * 1024 * 1024) throw new Error("Incomplete candidate runtime inputs");
  return profile.files;
}

export function runtimeSecurityOptions(args) {
  let profileID = securityRuntimeProfile;
  let inputDirectory;
  let inventoryFile;
  let production = false;
  for (let index = 0; index < args.length; index++) {
    const name = args[index];
    if (name === "--production" && !production && profileID === securityRuntimeProfile) {
      production = true;
      profileID = productionRuntimeProfile;
    } else if (name === "--profile" && !production && profileID === securityRuntimeProfile && args[index + 1] === funasrUpgradeProfile) {
      profileID = args[++index];
    } else if (["--runtime-input-directory", "--installed-inventory"].includes(name) && args[index + 1] && path.isAbsolute(args[index + 1])) {
      const value = args[++index];
      if (name === "--runtime-input-directory" && inputDirectory === undefined) inputDirectory = value;
      else if (name === "--installed-inventory" && inventoryFile === undefined) inventoryFile = value;
      else throw new Error("Duplicate runtime security input");
    } else throw new Error("Expected optional --production, --runtime-input-directory <absolute-directory> and --installed-inventory <absolute-file>");
  }
  if ((!production || inventoryFile) && !inputDirectory) throw new Error("Candidate and installed scans require verified runtime inputs");
  return { profileID, inputDirectory, inventoryFile };
}

// Production scans use repository sources so the default security gate does not
// need a prebuilt wheel or a caller-supplied sidecar. Verify every source used to
// identify the locks and the vendor's upstream package against manifest pins.
export async function readProductionRuntimeScanInputs(root, manifest) {
  const pins = runtimeInputPins(manifest, productionRuntimeProfile);
  const directory = path.join(root, "python/funasr-runner");
  await assertPlainDirectory(directory);
  await assertPlainDirectory(path.join(directory, "vendor"));
  await assertPlainDirectory(path.join(directory, "tools"));
  const files = new Map();
  for (const pin of [...pins.filter(({ fileName }) => fileName.startsWith("requirements-lock-") || ["recipe.json", "source.cdx.json"].includes(fileName)), manifest.productionInputs.builder]) {
    const sourceDirectory = pin.fileName === "build_vendor_wheel.py" ? path.join(directory, "tools") : ["recipe.json", "source.cdx.json"].includes(pin.fileName) ? path.join(directory, "vendor") : directory;
    const sourceName = pin.fileName === "recipe.json" ? "funasr-1.3.9.json" : pin.fileName;
    const bytes = await readPlainFile(sourceDirectory, sourceName);
    if (bytes.length !== pin.bytes || fileDigest(bytes) !== pin.sha256) throw new Error("Production runtime source digest differs from the manifest");
    files.set(pin.fileName, bytes);
  }
  const recipe = JSON.parse(files.get("recipe.json").toString("utf8"));
  const source = JSON.parse(files.get("source.cdx.json").toString("utf8"));
  const upstream = source.components?.find((component) => component.purl === recipe.upstream?.purl);
  const output = pins.find((pin) => pin.fileName === recipe.output?.filename);
  if (recipe.schemaVersion !== 1 || recipe.upstream?.name !== "funasr" || recipe.upstream.version !== "1.3.9" || recipe.upstream.purl !== "pkg:pypi/funasr@1.3.9" || !upstream || upstream.name !== recipe.upstream.name || upstream.version !== recipe.upstream.version || !upstream.hashes?.some((hash) => hash.alg === "SHA-256" && hash.content === recipe.upstream.sha256) || !output || output.sha256 !== recipe.output.sha256 || output.bytes !== recipe.output.bytes) throw new Error("Production vendor source identity differs from the fixed recipe");
  for (const [name, bytes] of files) {
    if (!name.startsWith("requirements-lock-")) continue;
    const lock = parseRuntimeLock(bytes.toString("utf8"));
    const funasr = lock.packages.find((component) => component.name === "funasr");
    if (!lock.bytes.equals(bytes) || funasr?.version !== recipe.output.version || funasr.hashes.length !== 1 || funasr.hashes[0] !== recipe.output.sha256) throw new Error("Production FunASR lock differs from the fixed vendor recipe");
  }
  return files;
}

export function parseRuntimeLock(text) {
  const normalized = text.replaceAll("\r\n", "\n").split("\n").filter((line) => line.trim() && !line.trimStart().startsWith("#")).join("\n") + "\n";
  const packages = [];
  const seen = new Set();
  let current;
  const finish = () => {
    if (current && current.hashes.length === 0) throw new Error("Candidate runtime lock contains an unhashed package");
  };
  for (const line of normalized.trimEnd().split("\n")) {
    const entry = line.match(/^([A-Za-z0-9_.-]+)==([A-Za-z0-9_.+!-]+)\s+\\$/);
    if (entry) {
      finish();
      const name = entry[1].toLowerCase().replace(/[_.]+/g, "-");
      if (seen.has(name)) throw new Error("Candidate runtime lock contains duplicate packages");
      seen.add(name);
      current = { name, version: entry[2], hashes: [] };
      packages.push(current);
    } else {
      const hash = line.match(/^\s+--hash=sha256:([a-f0-9]{64})(?:\s+\\)?$/);
      if (!current || !hash) throw new Error("Candidate runtime lock contains an unpinned requirement or option");
      current.hashes.push(hash[1]);
    }
  }
  finish();
  if (!packages.length) throw new Error("Candidate runtime lock is empty");
  return { bytes: Buffer.from(normalized), packages };
}

export async function assertPlainDirectory(directory) {
  if (!path.isAbsolute(directory)) throw new Error("Candidate runtime input directory must be absolute");
  const parsed = path.parse(directory);
  let current = parsed.root;
  for (const part of directory.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    if (!(await lstat(current)).isDirectory()) throw new Error("Candidate runtime directory contains a link or non-directory");
  }
}

export async function readPlainFile(directory, name, maximum = 2 * 1024 * 1024) {
  const filename = path.join(directory, name);
  const before = await lstat(filename);
  if (!before.isFile() || before.size < 1 || before.size > maximum) throw new Error("Invalid candidate runtime input file");
  const handle = await open(filename, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
  try {
    const actual = await handle.stat();
    if (!actual.isFile() || actual.dev !== before.dev || actual.ino !== before.ino || actual.size !== before.size) throw new Error("Candidate runtime input changed during verification");
    const bytes = await handle.readFile();
    if (bytes.length !== actual.size) throw new Error("Candidate runtime input changed during verification");
    return bytes;
  } finally {
    await handle.close();
  }
}

export async function readRuntimeInputs(directory, pins) {
  await assertPlainDirectory(directory);
  const names = (await readdir(directory)).sort();
  if (JSON.stringify(names) !== JSON.stringify(pins.map((pin) => pin.fileName).sort())) throw new Error("Candidate runtime input file set differs from the manifest");
  const files = new Map();
  for (const pin of pins) {
    const bytes = await readPlainFile(directory, pin.fileName);
    if (bytes.length !== pin.bytes || fileDigest(bytes) !== pin.sha256) throw new Error("Candidate runtime input digest differs from the manifest");
    files.set(pin.fileName, bytes);
  }
  return files;
}

export async function publishRuntimeInputs(directory, files, pins) {
  if (files.size !== pins.length || pins.some((pin) => !files.has(pin.fileName) || files.get(pin.fileName).length !== pin.bytes || fileDigest(files.get(pin.fileName)) !== pin.sha256)) throw new Error("Candidate runtime inputs differ from the pinned file set");
  await assertPlainDirectory(path.dirname(directory));
  await mkdir(directory, { mode: 0o700 });
  let complete = false;
  try {
    for (const [name, bytes] of files) await writeFile(path.join(directory, name), bytes, { flag: "wx", mode: 0o600 });
    await readRuntimeInputs(directory, pins);
    complete = true;
  } finally {
    if (!complete) await rm(directory, { recursive: true, force: true });
  }
}

export function runtimePackageOptions(args) {
  const targetArgs = [];
  let inputDirectory;
  let verifyHost = false;
  let runtimeProfile;
  for (let index = 0; index < args.length;) {
    const name = args[index++];
    if (name === "--verify-host" && !verifyHost) {
      verifyHost = true;
      continue;
    }
    const value = args[index++];
    if (name === "--runtime-input-directory" && inputDirectory === undefined && value && path.isAbsolute(value)) inputDirectory = value;
    else if (name === "--runtime-profile" && runtimeProfile === undefined && [securityRuntimeProfile, funasrUpgradeProfile].includes(value)) runtimeProfile = value;
    else if (name === "--target" && targetArgs.length === 0 && value) targetArgs.push(name, value);
    else throw new Error("Expected --target, optional --verify-host and optional --runtime-input-directory <absolute-directory>");
  }
  const targetID = packageTarget(targetArgs);
  if (runtimeProfile && !inputDirectory) throw new Error("Explicit candidate runtime requires verified runtime inputs");
  return { targetID, inputDirectory, verifyHost, ...(runtimeProfile ? {runtimeProfile} : {}) };
}

export function runtimeInventorySBOM(text, profileID, platformID) {
  const { packages } = parseRuntimeLock(text);
  return {
    bomFormat: "CycloneDX", specVersion: "1.5", version: 1,
    metadata: { component: { type: "application", name: `liberty-native-runtime-${profileID}-${platformID}` } },
    components: packages.map(({ name, version, hashes }) => ({ type: "library", name, version, purl: `pkg:pypi/${name}@${encodeURIComponent(version)}`, hashes: hashes.map((content) => ({ alg: "SHA-256", content })) })),
  };
}

export function runtimeInputDigest(files) {
  const hash = createHash("sha256");
  for (const name of [...files.keys()].sort()) {
    hash.update(name);
    hash.update(Buffer.from([0]));
    hash.update(files.get(name));
  }
  return hash.digest("hex");
}

export function validateInstalledRuntimeInventory(inventory, files, manifest, profileID = securityRuntimeProfile) {
  const profile = runtimeInputProfile(manifest, profileID);
  if (inventory.runtimeProfile !== profileID || inventory.runtimeInputSha256 !== runtimeInputDigest(files) || !profile?.platforms?.some((platform) => platform.platformId === inventory.platform) || !Array.isArray(inventory.components)) throw new Error("Runtime installed inventory is not bound to the verified inputs");
  const locked = parseRuntimeLock(files.get(`requirements-lock-${inventory.platform}.txt`).toString("utf8")).packages;
  const installed = new Map();
  for (const component of inventory.components) {
    if (typeof component.name !== "string" || !/^[A-Za-z0-9_.-]+$/.test(component.name) || typeof component.version !== "string" || !/^[A-Za-z0-9_.+!-]+$/.test(component.version)) throw new Error("Invalid candidate installed package identity");
    const name = component.name.toLowerCase().replace(/[_.]+/g, "-");
    if (installed.has(name)) throw new Error("Duplicate candidate installed package");
    installed.set(name, component.version);
  }
  for (const component of locked) if (installed.get(component.name) !== component.version) throw new Error("Candidate installed package differs from the hash lock");
  const allowed = new Set([...locked.map((component) => component.name), "pip"]);
  if ([...installed.keys()].some((name) => !allowed.has(name)) || !installed.has("pip")) throw new Error("Candidate installed inventory contains unexpected bootstrap packages");
  return {
    bomFormat: "CycloneDX", specVersion: "1.5", version: 1,
    metadata: { component: { type: "application", name: `liberty-native-runtime-installed-${inventory.platform}` }, properties: [{ name: "liberty:runtime-input-sha256", value: inventory.runtimeInputSha256 }] },
    components: [...installed].sort(([left], [right]) => left.localeCompare(right)).map(([name, version]) => ({ type: "library", name, version, purl: `pkg:pypi/${name}@${encodeURIComponent(version)}` })),
  };
}

export function assertRuntimeScanCoverage(result, expected) {
  const actual = new Set((result.results ?? []).flatMap((source) => (source.packages ?? []).map((entry) => `${entry.package?.ecosystem}:${entry.package?.name?.toLowerCase().replace(/[_.]+/g, "-")}@${entry.package?.version}`)));
  for (const component of expected) {
    if (!actual.has(`PyPI:${component.name}@${component.version}`)) throw new Error("Candidate runtime scanner omitted a pinned package or upstream identity");
  }
}

export async function loadRuntimeInputManifest(root) {
  return JSON.parse(await readFile(path.join(root, "packages/desktop-resources/runtime/runtime-manifest.json"), "utf8"));
}
