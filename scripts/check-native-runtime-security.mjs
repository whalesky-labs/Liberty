import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { assertOsvExceptionsAreGoverned, OSV_EXCEPTION_CONFIG, readPolicy } from "./lib/dependency-governance.mjs";
import { assertPlainDirectory, assertRuntimeScanCoverage, loadRuntimeInputManifest, productionRuntimeProfile, readPlainFile, readProductionRuntimeScanInputs, readRuntimeInputs, runtimeInputPins, runtimeInputProfile, runtimeInventorySBOM, runtimeSecurityOptions, validateInstalledRuntimeInventory } from "./lib/native-runtime-inputs.mjs";
import { fileDigest } from "./lib/native-package.mjs";
import { translationComponents } from "./lib/live-translation-inputs.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const { profileID, inputDirectory, inventoryFile } = runtimeSecurityOptions(process.argv.slice(2));
const manifest = await loadRuntimeInputManifest(root);
const profile = runtimeInputProfile(manifest, profileID);
const pins = runtimeInputPins(manifest, profileID);
const files = inputDirectory ? await readRuntimeInputs(inputDirectory, pins) : await readProductionRuntimeScanInputs(root, manifest);
let installedSBOM;
if (inventoryFile) {
  await assertPlainDirectory(path.dirname(inventoryFile));
  const inventory = JSON.parse((await readPlainFile(path.dirname(inventoryFile), path.basename(inventoryFile))).toString("utf8"));
  installedSBOM = validateInstalledRuntimeInventory(inventory, files, manifest, profileID);
}
const policy = await readPolicy(path.join(root, "config/dependency-governance.json"));
const exceptionFile = path.join(root, OSV_EXCEPTION_CONFIG);
const exceptions = await readFile(exceptionFile);
assertOsvExceptionsAreGoverned(exceptions.toString("utf8"));

async function runScanner(args) {
  return await new Promise((resolve, reject) => {
    const child = spawn("osv-scanner", args, { cwd: root, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (data) => { stdout += data; });
    child.stderr.on("data", (data) => { stderr += data; });
    child.once("error", reject);
    child.once("exit", (code, signal) => resolve({ code: code ?? 2, signal, stdout, stderr }));
  });
}
const version = await runScanner(["--version"]);
if (version.code !== 0 || !version.stdout.includes(`osv-scanner version: ${policy.tools.osvScanner}\n`)) throw new Error("Runtime scanning requires the pinned OSV Scanner version");
const outputRoot = path.join(root, "target/native/runtime-security");
await mkdir(outputRoot, { recursive: true });
const output = await mkdtemp(path.join(outputRoot, "scan-"));
const governedConfig = path.join(output, "current-policy.toml");
await writeFile(governedConfig, exceptions, { flag: "wx", mode: 0o600 });
await writeFile(path.join(output, "source.cdx.json"), files.get("source.cdx.json"));
const emptyConfig = path.join(output, "no-exceptions.toml");
await writeFile(emptyConfig, "");
const runs = [];
const inventories = profile.platforms.map((platform) => ({ name: platform.platformId, sbom: runtimeInventorySBOM(files.get(`requirements-lock-${platform.platformId}.txt`).toString("utf8"), profileID, platform.platformId) }));
for (const inventory of inventories) inventory.sbom.components.push(...translationComponents(manifest, inventory.name));
if (installedSBOM) inventories.push({ name: "installed", sbom: installedSBOM });
for (const { name, sbom } of inventories) {
  const sbomFile = path.join(output, `${name}.cdx.json`);
  await writeFile(sbomFile, JSON.stringify(sbom, null, 2) + "\n");
  const source = JSON.parse(files.get("source.cdx.json").toString("utf8"));
  const upstreamVersion = profileID === "funasr-upgrade" ? "1.4.29" : "1.3.9";
  if (!source.components?.some((component) => component.name === "funasr" && component.version === upstreamVersion && component.purl === `pkg:pypi/funasr@${upstreamVersion}`)) throw new Error("Pinned upstream FunASR identity missing");
  const expected = [...sbom.components, { name: "funasr", version: upstreamVersion }];
  for (const [mode, config] of [["current-policy", governedConfig], ["unfiltered", emptyConfig]]) {
    const scan = await runScanner(["scan", "source", "-L", sbomFile, "-L", path.join(output, "source.cdx.json"), "--config", config, "--format", "json", "--all-packages"]);
    const prefix = `${name}-${mode}`;
    await writeFile(path.join(output, `${prefix}.json`), scan.stdout);
    await writeFile(path.join(output, `${prefix}.log`), scan.stderr);
    let complete = false;
    try {
      assertRuntimeScanCoverage(JSON.parse(scan.stdout), expected);
      complete = true;
    } catch {}
    const passed = scan.code === 0 && complete;
    runs.push({ inventory: name, mode, exitCode: scan.code, signal: scan.signal, coverageComplete: complete, passed });
    console.log(`${profileID} runtime ${name} ${mode}: exit=${scan.code}, complete=${complete}`);
  }
}
const governed = runs.filter((run) => run.mode === "current-policy");
const passed = governed.length === inventories.length && governed.every((run) => run.passed) && runs.every((run) => run.coverageComplete && (run.exitCode === 0 || run.exitCode === 1));
await writeFile(path.join(output, "summary.json"), JSON.stringify({ profile: profileID, runtimeVersion: profile.runtimeVersion, checkedAt: new Date().toISOString(), scanner: policy.tools.osvScanner, exceptionSHA256: fileDigest(exceptions), inputs: pins, verifiedInputs: [...files].map(([fileName, bytes]) => ({ fileName, bytes: bytes.length, sha256: fileDigest(bytes) })), inputValidation: inputDirectory ? "complete-runtime-inputs" : "repository-production-locks-builder-recipe-and-upstream-sbom", passed, productionPolicyPassed: profileID === productionRuntimeProfile && passed, productionClearance: false, runs }, null, 2) + "\n");
console.log(`${profileID} runtime security evidence: ${output}`);
if (!passed) process.exitCode = 1;
