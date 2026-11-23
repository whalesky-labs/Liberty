import assert from "node:assert/strict";
import { copyFile, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { assertCandidateManifest, candidateIdentity, fileDigest } from "./lib/native-package.mjs";
import { assertRuntimeScanCoverage, loadRuntimeInputManifest, parseRuntimeLock, productionRuntimeProfile, publishRuntimeInputs, readProductionRuntimeScanInputs, readRuntimeInputs, runtimeInputDigest, runtimeInputPins, runtimeInventorySBOM, runtimePackageOptions, runtimeSecurityOptions, securityRuntimeProfile, validateInstalledRuntimeInventory, vendorRuntimeFiles } from "./lib/native-runtime-inputs.mjs";
import { parseRuntimeModelPins, runtimeModelPinsPath } from "./lib/runtime-model-pins.mjs";
import { candidateRuntimeAcceptanceDirectory } from "./run-native.mjs";

const platforms = ["darwin-aarch64", "darwin-x64", "windows-x64"].map((platformId) => ({ platformId }));
const fixtureFiles = () => new Map([...vendorRuntimeFiles, ...platforms.map(({ platformId }) => `requirements-lock-${platformId}.txt`)].map((name) => [name, Buffer.from(`synthetic ${name}\n`)]));
const manifestFor = (files) => ({ candidateProfiles: { "security-candidate": { platforms, files: [...files].map(([fileName, bytes]) => ({ fileName, bytes: bytes.length, sha256: fileDigest(bytes) })) } } });

test("checked-in candidate locks reproduce their pinned sidecar bytes", async () => {
  const root = await realpath(fileURLToPath(new URL("..", import.meta.url)));
  const directory = path.join(root, "packages", "desktop-resources", "runtime", "candidates", securityRuntimeProfile);
  const pins = runtimeInputPins(await loadRuntimeInputManifest(root))
    .filter(({ fileName }) => !vendorRuntimeFiles.includes(fileName));
  const locks = await readRuntimeInputs(directory, pins);
  for (const [name, bytes] of locks) {
    assert.deepEqual(parseRuntimeLock(bytes.toString("utf8")).bytes, bytes, name);
  }
});

test("production scan sources bind the actual locks, builder and upstream source identity", async () => {
  const root = await realpath(fileURLToPath(new URL("..", import.meta.url)));
  const manifest = await loadRuntimeInputManifest(root);
  const pins = runtimeInputPins(manifest, productionRuntimeProfile);
  assert.equal(pins.length, 8);
  const files = await readProductionRuntimeScanInputs(root, manifest);
  assert.equal(files.size, 6);
  const source = JSON.parse(files.get("source.cdx.json"));
  assert.ok(source.components.some(({ purl }) => purl === "pkg:pypi/funasr@1.3.9"));
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), "liberty-production-runtime-sources-")));
  try {
    const runtime = path.join(directory, "python/funasr-runner");
    await mkdir(path.join(runtime, "tools"), { recursive: true });
    await mkdir(path.join(runtime, "vendor"));
    for (const name of files.keys()) {
      const relative = name === "build_vendor_wheel.py" ? `tools/${name}` : name === "recipe.json" ? "vendor/funasr-1.3.9.json" : name === "source.cdx.json" ? `vendor/${name}` : name;
      await copyFile(path.join(root, "python/funasr-runner", relative), path.join(runtime, relative));
    }
    for (const relative of ["tools/build_vendor_wheel.py", "vendor/funasr-1.3.9.json", "vendor/source.cdx.json", "requirements-lock-darwin-x64.txt"]) {
      const original = await readFile(path.join(runtime, relative));
      await writeFile(path.join(runtime, relative), Buffer.concat([original, Buffer.from("\n")]));
      await assert.rejects(readProductionRuntimeScanInputs(directory, manifest), /digest/);
      await writeFile(path.join(runtime, relative), original);
    }
    assert.equal((await readProductionRuntimeScanInputs(directory, manifest)).size, 6);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("production security CLI is explicit and installed inventories require complete inputs", () => {
  const directory = path.resolve("synthetic-runtime");
  const inventoryFile = path.join(directory, "installed.json");
  assert.deepEqual(runtimeSecurityOptions(["--production"]), { profileID: "production", inputDirectory: undefined, inventoryFile: undefined });
  assert.deepEqual(runtimeSecurityOptions(["--production", "--installed-inventory", inventoryFile, "--runtime-input-directory", directory]), { profileID: "production", inputDirectory: directory, inventoryFile });
  assert.equal(runtimeSecurityOptions(["--runtime-input-directory", directory]).profileID, securityRuntimeProfile);
  for (const args of [[], ["--production", "--production"], ["--production", "--installed-inventory", inventoryFile], ["--production", "--runtime-input-directory", "relative"], ["--production", "--skip"], ["--runtime-input-directory", directory, "--runtime-input-directory", directory]]) assert.throws(() => runtimeSecurityOptions(args));
});

test("candidate acceptance separates existing runtime generations when their identity changes", () => {
  const files = fixtureFiles();
  const digest = runtimeInputDigest(files);
  const manifest = { ...manifestFor(files), modelSetVersion: "models.1", modelProfile: "paraformer" };
  manifest.candidateProfiles[securityRuntimeProfile].runtimeVersion = "candidate.1";
  const previous = candidateRuntimeAcceptanceDirectory(manifest, digest, "darwin", "arm64");
  assert.equal(candidateRuntimeAcceptanceDirectory(structuredClone(manifest), digest, "darwin", "arm64"), previous);
  assert.notEqual(candidateRuntimeAcceptanceDirectory(manifest, digest, "darwin", "x64"), previous);
  assert.notEqual(candidateRuntimeAcceptanceDirectory({ ...manifest, modelSetVersion: "models.2" }, digest, "darwin", "arm64"), previous);
  files.set("provenance.json", Buffer.from("changed vendor evidence"));
  assert.notEqual(candidateRuntimeAcceptanceDirectory(manifest, runtimeInputDigest(files), "darwin", "arm64"), previous);
  manifest.candidateProfiles[securityRuntimeProfile].runtimeVersion = "candidate.2";
  assert.notEqual(candidateRuntimeAcceptanceDirectory(manifest, digest, "darwin", "arm64"), previous);
});

test("the shared model registry requires all roles and distinct immutable model commits", async () => {
  const pins = parseRuntimeModelPins(await readFile(runtimeModelPinsPath, "utf8"));
  assert.deepEqual(pins.map(({ role }) => role), ["model", "vad", "punc", "spk"]);
  for (const mutate of [
    (changed) => changed.pop(),
    (changed) => changed.reverse(),
    (changed) => { changed[0].role = "vad"; },
    (changed) => { changed[0].revision = changed[1].revision; },
    (changed) => { changed[0].revision = "master"; },
    (changed) => { changed[0].revision += "\n"; },
    (changed) => { changed[0].modelId = "../model"; },
    (changed) => { changed[0].modelId += "\n"; },
    (changed) => { changed[0].unexpected = 1; },
  ]) {
    const changed = structuredClone(pins);
    mutate(changed);
    assert.throws(() => parseRuntimeModelPins(JSON.stringify(changed)), /immutable 40-character commits/);
  }
});

test("runtime package flags preserve explicit target and reject ambiguous or unsupported profiles", () => {
  const absolute = path.resolve("synthetic-runtime-inputs");
  assert.deepEqual(runtimePackageOptions(["--runtime-input-directory", absolute, "--target", "windows-x64"]), { targetID: "windows-x64", inputDirectory: absolute, verifyHost: false });
  assert.deepEqual(runtimePackageOptions(["--verify-host", "--target", "windows-x86"]), { targetID: "windows-x86", inputDirectory: undefined, verifyHost: true });
  assert.equal(runtimePackageOptions(["--target", "darwin-arm64"]).targetID, "darwin-aarch64");
  for (const args of [["--runtime-input-directory", "relative"], ["--runtime-input-directory", absolute, "--runtime-input-directory", absolute], ["--target", "windows-x86", "--runtime-input-directory", absolute], ["--target", "windows-x64", "--target", "darwin-x64"], ["--runtime-profile", "security-candidate"], ["--verify-host", "--verify-host"], ["--verify-host", "true"]]) assert.throws(() => runtimePackageOptions(args));
});

test("candidate locks discard local comments without changing exact versions and hashes", () => {
  const hash = "a".repeat(64);
  const text = `# uv compile /private/synthetic-path\r\n\r\nFunASR==1.3.9+liberty.local2 \\\r\n    --hash=sha256:${hash}\r\n`;
  const parsed = parseRuntimeLock(text);
  assert.equal(parsed.bytes.toString(), `FunASR==1.3.9+liberty.local2 \\\n    --hash=sha256:${hash}\n`);
  assert.deepEqual(parsed.packages, [{ name: "funasr", version: "1.3.9+liberty.local2", hashes: [hash] }]);
  for (const bad of ["", "# empty", "funasr==1.3.9", "-r /tmp/other.txt", `funasr==1.3.9 \\\n    --hash=sha256:${hash}\n--extra-index-url https://example.invalid`, `a_b==1 \\\n    --hash=sha256:${hash}\na-b==1 \\\n    --hash=sha256:${hash}`]) assert.throws(() => parseRuntimeLock(bad));
});

test("runtime inventory keeps local and upstream identities and rejects scanner omission", () => {
  const sbom = runtimeInventorySBOM(`funasr==1.3.9+liberty.local2 \\\n    --hash=sha256:${"a".repeat(64)}\n`, "security-candidate", "darwin-x64");
  assert.equal(sbom.components[0].purl, "pkg:pypi/funasr@1.3.9%2Bliberty.local2");
  const expected = [...sbom.components, { name: "funasr", version: "1.3.9" }];
  const result = { results: [{ packages: expected.map(({ name, version }) => ({ package: { ecosystem: "PyPI", name, version } })) }] };
  assert.doesNotThrow(() => assertRuntimeScanCoverage(result, expected));
  result.results[0].packages.pop();
  assert.throws(() => assertRuntimeScanCoverage(result, expected));
  assert.throws(() => assertRuntimeScanCoverage({}, expected));
});

test("installed runtime inventory requires exact locked versions, input digest and explicit bootstrap pip", () => {
  const files = fixtureFiles();
  const platform = "darwin-x64";
  files.set(`requirements-lock-${platform}.txt`, Buffer.from(`funasr==1.3.9+liberty.local2 \\\n    --hash=sha256:${"a".repeat(64)}\n`));
  const manifest = manifestFor(files);
  const inventory = { runtimeProfile: "security-candidate", runtimeInputSha256: runtimeInputDigest(files), platform, components: [{ name: "funasr", version: "1.3.9+liberty.local2" }, { name: "pip", version: "26.3" }] };
  assert.equal(validateInstalledRuntimeInventory(inventory, files, manifest).components.length, 2);
  for (const mutate of [(changed) => { changed.runtimeProfile = "production"; }, (changed) => { changed.runtimeInputSha256 = "0".repeat(64); }, (changed) => { changed.platform = "windows-x86"; }, (changed) => { changed.components[0].version = "1.3.9"; }, (changed) => { changed.components.push({ name: "oss2", version: "1" }); }, (changed) => { changed.components.push(changed.components[0]); }, (changed) => { changed.components.pop(); }]) {
    const changed = structuredClone(inventory);
    mutate(changed);
    assert.throws(() => validateInstalledRuntimeInventory(changed, files, manifest));
  }
});

test("runtime pins require all eight fixed inputs and three supported unique platforms", () => {
  const manifest = manifestFor(fixtureFiles());
  assert.equal(runtimeInputPins(manifest).length, 8);
  assert.throws(() => runtimeInputPins(manifest, "production"));
  for (const mutate of [(profile) => profile.files.pop(), (profile) => profile.files.push(profile.files[0]), (profile) => { profile.files[0].fileName = "../escape"; }, (profile) => { profile.files[0].sha256 = "invalid"; }, (profile) => { profile.platforms[0].platformId = "windows-x86"; }, (profile) => { profile.platforms[1].platformId = profile.platforms[0].platformId; }]) {
    const changed = structuredClone(manifest);
    mutate(changed.candidateProfiles["security-candidate"]);
    assert.throws(() => runtimeInputPins(changed));
  }
});

test("candidate package metadata binds the selected runtime to the packaged files", () => {
  const inputs = runtimeInputPins(manifestFor(fixtureFiles()));
  const manifest = { format: 1, channel: "candidate", identity: candidateIdentity, version: "1.1.24", target: "darwin-x64", productionReady: false, signed: false, notarized: false, defaultRuntimeProfile: "production", availableRuntimeProfiles: ["production", "security-candidate"], candidateRuntimeVersion: "synthetic-candidate-1", runtimeInputs: inputs, files: inputs.map(({ fileName, bytes, sha256 }) => ({ path: `App/Contents/Resources/runtime-inputs/${fileName}`, bytes, sha256 })) };
  assert.doesNotThrow(() => assertCandidateManifest(manifest));
  for (const mutate of [(changed) => { changed.defaultRuntimeProfile = "security-candidate"; }, (changed) => { changed.target = "windows-x86"; }, (changed) => { changed.files[0].sha256 = "a".repeat(64); }, (changed) => { changed.runtimeInputs[0] = changed.runtimeInputs[1]; }, (changed) => { changed.availableRuntimeProfiles = ["production"]; }]) {
    const changed = structuredClone(manifest);
    mutate(changed);
    assert.throws(() => assertCandidateManifest(changed));
  }
});

test("runtime sidecar copy verifies exact bytes and rejects mutation, extras, links and overwrite", async (context) => {
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), "liberty-runtime-inputs-")));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const files = fixtureFiles();
  const pins = runtimeInputPins(manifestFor(files));
  const target = path.join(directory, "inputs");
  await publishRuntimeInputs(target, files, pins);
  const read = await readRuntimeInputs(target, pins);
  assert.deepEqual(read, files);
  await assert.rejects(publishRuntimeInputs(target, files, pins));
  await writeFile(path.join(target, "extra.txt"), "extra");
  await assert.rejects(readRuntimeInputs(target, pins));
  await rm(path.join(target, "extra.txt"));
  const selected = pins[0].fileName;
  await writeFile(path.join(target, selected), "mutated");
  await assert.rejects(readRuntimeInputs(target, pins));
  await rm(path.join(target, selected));
  const external = path.join(directory, "external.txt");
  await writeFile(external, files.get(selected));
  await symlink(external, path.join(target, selected));
  await assert.rejects(readRuntimeInputs(target, pins));
  await symlink(target, path.join(directory, "linked"), "dir");
  await assert.rejects(readRuntimeInputs(path.join(directory, "linked"), pins));
  assert.deepEqual(await readFile(external), files.get(selected));
});
