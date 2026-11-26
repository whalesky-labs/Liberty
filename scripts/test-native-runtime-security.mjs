import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import vm from "node:vm";
import * as governance from "./lib/dependency-governance.mjs";
import * as inputs from "./lib/native-runtime-inputs.mjs";
import { fileDigest, nativeTargets } from "./lib/native-package.mjs";
import * as translationInputs from "./lib/live-translation-inputs.mjs";

const checkerURL = new URL("./check-native-runtime-security.mjs", import.meta.url);
const checkerSource = await readFile(checkerURL, "utf8");
const root = fileURLToPath(new URL("../", import.meta.url));
const pinnedTranslation = (await inputs.loadRuntimeInputManifest(root)).liveTranslation;
const exceptionFile = path.join(root, governance.OSV_EXCEPTION_CONFIG);
const verifiedExceptions = Buffer.from('[[IgnoredVulns]]\nid = "GHSA-2222-3333-4444"\nignoreUntil = 2999-12-31\nreason = "Synthetic vulnerability-specific exception for scanner control-flow tests."\n');
const replacementExceptions = Buffer.from('[[IgnoredVulns]]\nid = "GHSA-5555-6666-7777"\nreason = "Synthetic replacement deliberately lacks the required expiry."\n');

function runtimeFixture() {
  const platforms = Object.keys(nativeTargets).map((platformId) => ({ platformId }));
  const locked = [{ name: "funasr", version: "1.3.9+liberty.local2" }, { name: "numpy", version: "2.3.1" }];
  const lock = Buffer.from(locked.map(({ name, version }) => `${name}==${version} \\\n    --hash=sha256:${"a".repeat(64)}\n`).join(""));
  const files = new Map([
    ...inputs.vendorRuntimeFiles.map((name) => [name, Buffer.from(`synthetic ${name}\n`)]),
    ...platforms.map(({ platformId }) => [`requirements-lock-${platformId}.txt`, lock]),
  ]);
  files.set("source.cdx.json", Buffer.from(JSON.stringify({ bomFormat: "CycloneDX", specVersion: "1.5", version: 1, components: [{ type: "library", name: "funasr", version: "1.3.9", purl: "pkg:pypi/funasr@1.3.9" }] })));
  const pins = [...files].map(([fileName, bytes]) => ({ fileName, bytes: bytes.length, sha256: fileDigest(bytes) }));
  const manifest = { liveTranslation: structuredClone(pinnedTranslation), candidateProfiles: { [inputs.securityRuntimeProfile]: { runtimeVersion: "synthetic-review-1", platforms, files: pins } } };
  const inventory = { runtimeProfile: inputs.securityRuntimeProfile, runtimeInputSha256: inputs.runtimeInputDigest(files), platform: platforms[0].platformId, components: [...locked, { name: "pip", version: "26.3" }] };
  return { files, manifest, inventory, platforms };
}

async function runSecurityCheck({ production = false, installed = false, omitUpstream = false, omitTranslation = false, installedExitCode, governedExitCode, replaceExceptions = false } = {}) {
  assert.equal(typeof vm.SourceTextModule, "function", "Run with node --experimental-vm-modules --test scripts/test-native-runtime-security.mjs");
  const fixture = runtimeFixture();
  const profileID = production ? inputs.productionRuntimeProfile : inputs.securityRuntimeProfile;
  if (production) {
    fixture.manifest.runtimeVersion = "synthetic-production-1";
    fixture.manifest.platforms = fixture.platforms.map((platform) => ({ ...platform, asrBackend: "funasr" }));
    fixture.manifest.productionInputs = { builder: { fileName: "build_vendor_wheel.py", bytes: 1, sha256: "a".repeat(64) }, files: fixture.manifest.candidateProfiles[inputs.securityRuntimeProfile].files };
    fixture.inventory.runtimeProfile = inputs.productionRuntimeProfile;
  }
  const memory = new Map([[exceptionFile, Buffer.from(verifiedExceptions)]]);
  const writes = new Map();
  const validations = [];
  const scans = [];
  const output = path.join(root, "target/native/runtime-security/scan-synthetic");
  const inputDirectory = path.join(root, "target/native/synthetic-runtime-inputs");
  const inventoryFile = path.join(root, "target/native/synthetic-installed/installed-inventory.json");
  const scannerVersion = "2.5.0";
  let versionChecks = 0;
  const mockProcess = { argv: [process.execPath, fileURLToPath(checkerURL), ...(production ? ["--production"] : []), ...(!production || installed ? ["--runtime-input-directory", inputDirectory] : []), ...(installed ? ["--installed-inventory", inventoryFile] : [])] };

  function memoryFile(filename) {
    assert.ok(memory.has(filename), `Missing in-memory file: ${filename}`);
    return Buffer.from(memory.get(filename));
  }

  function spawn(command, args) {
    assert.equal(command, "osv-scanner");
    const child = new EventEmitter();
    child.stdout = new EventEmitter();
    child.stderr = new EventEmitter();
    queueMicrotask(() => {
      try {
        if (args[0] === "--version") {
          versionChecks += 1;
          if (replaceExceptions) memory.set(exceptionFile, Buffer.from(replacementExceptions));
          child.stdout.emit("data", `osv-scanner version: ${scannerVersion}\n`);
          child.emit("exit", 0, null);
          child.emit("close", 0, null);
          return;
        }
        assert.deepEqual(Array.from(args).slice(0, 2), ["scan", "source"]);
        assert.ok(args.includes("--all-packages"));
        assert.equal(args[args.indexOf("--format") + 1], "json");
        const sbomFiles = Array.from(args).flatMap((argument, index) => argument === "-L" ? [args[index + 1]] : []);
        assert.equal(sbomFiles.length, 2);
        const configFile = args[args.indexOf("--config") + 1];
        const mode = path.basename(configFile) === "no-exceptions.toml" ? "unfiltered" : "current-policy";
        const inventory = path.basename(sbomFiles[0], ".cdx.json");
        const results = sbomFiles.map((filename) => ({ source: { path: filename, type: "sbom" }, packages: JSON.parse(memoryFile(filename)).components.map(({ name, version }) => ({ package: { ecosystem: "PyPI", name, version } })).filter((entry) => (!omitUpstream || entry.package.name !== "funasr" || entry.package.version !== "1.3.9") && (!omitTranslation || entry.package.name !== "ctranslate2")) }));
        scans.push({ inventory, mode, configFile, config: memoryFile(configFile), sbomFiles, results });
        const code = inventory === "installed" && installedExitCode !== undefined ? installedExitCode : mode === "current-policy" ? governedExitCode ?? 0 : 1;
        child.stdout.emit("data", JSON.stringify({ results }));
        child.emit("exit", code, null);
        child.emit("close", code, null);
      } catch (error) {
        child.emit("error", error);
      }
    });
    return child;
  }

  const dependencies = new Map([
    ["node:child_process", { spawn }],
    ["node:fs/promises", {
      mkdir: async () => {},
      mkdtemp: async (prefix) => { assert.equal(prefix, path.join(root, "target/native/runtime-security/scan-")); return output; },
      readFile: async (filename) => { assert.equal(filename, exceptionFile); return memoryFile(filename); },
      writeFile: async (filename, bytes, options) => {
        if (options?.flag === "wx") assert.ok(!memory.has(filename));
        memory.set(filename, Buffer.from(bytes));
        writes.set(filename, options === undefined ? undefined : { ...options });
      },
    }],
    ["node:path", { default: path }],
    ["node:url", { fileURLToPath }],
    ["./lib/dependency-governance.mjs", {
      ...governance,
      assertOsvExceptionsAreGoverned: (text) => { governance.assertOsvExceptionsAreGoverned(text); validations.push(Buffer.from(text)); },
      readPolicy: async () => ({ tools: { osvScanner: scannerVersion } }),
    }],
    ["./lib/native-runtime-inputs.mjs", {
      ...inputs,
      loadRuntimeInputManifest: async () => fixture.manifest,
      readRuntimeInputs: async (directory, pins) => { assert.equal(directory, inputDirectory); assert.deepEqual(pins, fixture.manifest.candidateProfiles[inputs.securityRuntimeProfile].files); return fixture.files; },
      readProductionRuntimeScanInputs: async (directory, manifest) => { assert.equal(directory, path.resolve(root)); assert.equal(manifest, fixture.manifest); return fixture.files; },
      assertPlainDirectory: async (directory) => { assert.equal(directory, path.dirname(inventoryFile)); },
      readPlainFile: async (directory, name) => { assert.equal(path.join(directory, name), inventoryFile); return Buffer.from(JSON.stringify(fixture.inventory)); },
    }],
    ["./lib/native-package.mjs", { fileDigest }],
    ["./lib/live-translation-inputs.mjs", translationInputs],
  ]);
  const context = vm.createContext({ process: mockProcess, Buffer, console: { log() {} } });
  const checker = new vm.SourceTextModule(checkerSource, { context, identifier: checkerURL.href, initializeImportMeta(meta) { meta.url = checkerURL.href; } });
  await checker.link((specifier) => {
    const exports = dependencies.get(specifier);
    assert.ok(exports, `Unexpected checker dependency: ${specifier}`);
    return new vm.SyntheticModule(Object.keys(exports), function () {
      for (const [name, value] of Object.entries(exports)) this.setExport(name, value);
    }, { context });
  });
  await checker.evaluate();
  assert.equal(versionChecks, 1);
  assert.deepEqual(validations, [verifiedExceptions]);
  const summary = JSON.parse(memoryFile(path.join(output, "summary.json")));
  assert.equal(summary.productionClearance, false);
  assert.equal(summary.profile, profileID);
  assert.equal(summary.runtimeVersion, inputs.runtimeInputProfile(fixture.manifest, profileID).runtimeVersion);
  assert.deepEqual(summary.inputs, fixture.manifest.candidateProfiles[inputs.securityRuntimeProfile].files);
  assert.equal(summary.exceptionSHA256, fileDigest(verifiedExceptions));
  return { summary, scans, exitCode: mockProcess.exitCode ?? 0, output, writes, memory, platforms: fixture.platforms };
}

test("candidate lock inventories require six complete scans and allow unfiltered findings", async () => {
  const result = await runSecurityCheck();
  assert.equal(result.scans.length, 6);
  assert.equal(result.summary.runs.length, 6);
  assert.equal(result.summary.passed, true);
  assert.equal(result.exitCode, 0);
  assert.deepEqual(result.scans.map(({ inventory, mode }) => [inventory, mode]), result.platforms.flatMap(({ platformId }) => [[platformId, "current-policy"], [platformId, "unfiltered"]]));
  assert.ok(result.summary.runs.every((run) => run.coverageComplete && run.exitCode === (run.mode === "current-policy" ? 0 : 1)));
  assert.ok(result.scans.every((scan) => scan.sbomFiles[1] === path.join(result.output, "source.cdx.json")));
  assert.ok(result.scans.every((scan) => scan.results.some(source => source.packages.some(entry => entry.package.name === "ctranslate2" && entry.package.version === "4.9.1"))));
});

test("omitting optional local translation fails runtime scan coverage", async () => {
  const result = await runSecurityCheck({ omitTranslation: true });
  assert.equal(result.exitCode, 1);
  assert.equal(result.summary.passed, false);
  assert.ok(result.summary.runs.every(run => !run.coverageComplete));
});

test("production scans use actual production identity and preserve upstream coverage and failure gates", async () => {
  for (const installed of [false, true]) {
    const result = await runSecurityCheck({ production: true, installed });
    assert.equal(result.summary.profile, "production");
    assert.equal(result.summary.productionPolicyPassed, true);
    assert.equal(result.summary.productionClearance, false);
    assert.equal(result.scans.length, installed ? 8 : 6);
    assert.equal(result.summary.inputValidation, installed ? "complete-runtime-inputs" : "repository-production-locks-builder-recipe-and-upstream-sbom");
  }
  for (const options of [{ omitUpstream: true }, { governedExitCode: 1 }, { installed: true, installedExitCode: 2 }]) {
    const result = await runSecurityCheck({ production: true, ...options });
    assert.equal(result.summary.productionPolicyPassed, false);
    assert.equal(result.exitCode, 1);
  }
});

test("candidate installed inventory adds two scans including bootstrap pip and upstream FunASR", async () => {
  const result = await runSecurityCheck({ installed: true });
  assert.equal(result.scans.length, 8);
  assert.equal(result.summary.runs.length, 8);
  assert.equal(result.summary.passed, true);
  assert.equal(result.exitCode, 0);
  const installed = result.scans.filter((scan) => scan.inventory === "installed");
  assert.deepEqual(installed.map((scan) => scan.mode), ["current-policy", "unfiltered"]);
  for (const scan of installed) {
    const packages = scan.results.flatMap((source) => source.packages.map((entry) => `${entry.package.name}@${entry.package.version}`));
    assert.deepEqual(packages.sort(), ["funasr@1.3.9", "funasr@1.3.9+liberty.local2", "numpy@2.3.1", "pip@26.3"].sort());
  }
});

test("omitting upstream FunASR fails coverage with and without an installed inventory", async () => {
  for (const installed of [false, true]) {
    const result = await runSecurityCheck({ installed, omitUpstream: true });
    assert.equal(result.scans.length, installed ? 8 : 6);
    assert.equal(result.summary.passed, false);
    assert.equal(result.exitCode, 1);
    assert.ok(result.summary.runs.every((run) => !run.coverageComplete && !run.passed));
  }
});

test("an installed scanner exit 2 blocks acceptance even when all lock scans pass", async () => {
  const result = await runSecurityCheck({ installed: true, installedExitCode: 2 });
  assert.equal(result.scans.length, 8);
  assert.equal(result.summary.passed, false);
  assert.equal(result.exitCode, 1);
  assert.ok(result.summary.runs.filter((run) => run.inventory !== "installed" && run.mode === "current-policy").every((run) => run.passed));
  const installed = result.summary.runs.filter((run) => run.inventory === "installed");
  assert.equal(installed.length, 2);
  assert.ok(installed.every((run) => run.exitCode === 2 && run.coverageComplete && !run.passed));
});

test("governed scanner exit 1 blocks acceptance despite complete package coverage", async () => {
  const result = await runSecurityCheck({ installed: true, governedExitCode: 1 });
  assert.equal(result.scans.length, 8);
  assert.equal(result.summary.passed, false);
  assert.equal(result.exitCode, 1);
  assert.ok(result.summary.runs.every((run) => run.exitCode === 1 && run.coverageComplete && !run.passed));
});

test("replacing source exceptions after validation cannot change scanned policy or its reported digest", async () => {
  assert.throws(() => governance.assertOsvExceptionsAreGoverned(replacementExceptions.toString()), /ignoreUntil/);
  const result = await runSecurityCheck({ installed: true, replaceExceptions: true });
  assert.equal(result.summary.passed, true);
  assert.equal(result.exitCode, 0);
  assert.deepEqual(result.memory.get(exceptionFile), replacementExceptions);
  const governed = result.scans.filter((scan) => scan.mode === "current-policy");
  assert.equal(governed.length, 4);
  for (const scan of governed) {
    assert.equal(scan.configFile, path.join(result.output, "current-policy.toml"));
    assert.notEqual(scan.configFile, exceptionFile);
    assert.deepEqual(scan.config, verifiedExceptions);
    assert.equal(fileDigest(scan.config), result.summary.exceptionSHA256);
  }
  assert.deepEqual(result.writes.get(path.join(result.output, "current-policy.toml")), { flag: "wx", mode: 0o600 });
  assert.ok(result.scans.filter((scan) => scan.mode === "unfiltered").every((scan) => scan.config.length === 0));
});
