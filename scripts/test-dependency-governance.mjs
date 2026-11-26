import assert from "node:assert/strict";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  CARGO_DENY_SECURITY_CHECKS,
  OSV_EXCEPTION_CONFIG,
  OSV_LOCK_FILES,
  PYTHON_LOCK_FILES,
  assertGoLockHasIntegrity,
  parseGoModules,
  assertNodeLockHasIntegrity,
  assertOsvExceptionsAreGoverned,
  assertPythonLockHasHashes,
  evaluateDependencyFindings,
  dependencyGovernanceOptions,
  nativeNodeDependencies,
  readPolicy,
} from "./lib/dependency-governance.mjs";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const policy = await readPolicy(path.join(repositoryRoot, "config/dependency-governance.json"));
const osvExceptions = await readFile(path.join(repositoryRoot, OSV_EXCEPTION_CONFIG), "utf8");
const violations = evaluateDependencyFindings(policy, [
  { kind: "license", package: "fixture-license", license: "GPL-3.0-only" },
  { kind: "vulnerability", package: "fixture-vulnerable", id: "OSV-TEST-1", severity: "HIGH" },
  { kind: "source", package: "fixture-source", sourceKind: "git-unpinned" },
]);
assert.equal(violations.length, 3);
assert.throws(() => assertPythonLockHasHashes("demo==1.0.0\n", "fixture-lock"), /unhashed/);
assert.throws(
  () => assertNodeLockHasIntegrity("packages:\n\n  demo@1.0.0:\n    resolution: {}\n\nsnapshots:\n"),
  /without integrity/,
);
assert.ok(CARGO_DENY_SECURITY_CHECKS.includes("advisories"));
assert.ok(OSV_LOCK_FILES.includes("pnpm-lock.yaml"));
assert.ok(OSV_LOCK_FILES.includes("apps/native/go.mod"));
const fixtureGoModule = "module fixture\nrequire (\n example.test/module v1.2.3 // indirect\n)\n";
const fixtureHash = `h1:${"a".repeat(43)}=`;
const fixtureGoSum = `example.test/module v1.2.3 ${fixtureHash}\nexample.test/module v1.2.3/go.mod ${fixtureHash}\n`;
assert.doesNotThrow(() => assertGoLockHasIntegrity(fixtureGoModule, fixtureGoSum));
assert.throws(() => assertGoLockHasIntegrity(fixtureGoModule, ""), /lacks checksum/);
assert.throws(() => parseGoModules(`${fixtureGoModule}replace example.test/module => ../unlocked\n`), /locked module source/);
assert.throws(() => parseGoModules("require example.test/module latest"), /Unpinned/);
assert.ok(OSV_LOCK_FILES.some((lockFile) => lockFile.includes("requirements-lock-")));
assert.ok(!OSV_LOCK_FILES.includes("Cargo.lock"));
assert.doesNotThrow(() => assertOsvExceptionsAreGoverned(osvExceptions, new Date("2026-08-13T00:00:00Z")));
assert.throws(
  () => assertOsvExceptionsAreGoverned('[[PackageOverrides]]\nname = "torch"\nignore = true\n'),
  /entire package/,
);
assert.throws(
  () => assertOsvExceptionsAreGoverned('[[IgnoredVulns]]\nid = "PYSEC-1"\nreason = "Temporary compatibility exception."\n'),
  /ignoreUntil/,
);
assert.throws(
  () => assertOsvExceptionsAreGoverned('[[IgnoredVulns]]\nid = "PYSEC-1"\nignoreUntil = 2026-01-01\nreason = "Temporary compatibility exception."\n', new Date("2026-08-13T00:00:00Z")),
  /expired/,
);

assert.deepEqual(dependencyGovernanceOptions([]), { mode: "security", native: false });
assert.deepEqual(dependencyGovernanceOptions(["--native", "licenses"]), { mode: "licenses", native: true });
for (const args of [["--native", "--native"], ["unknown"], ["security", "locks"], ["security", "--skip"]]) assert.throws(() => dependencyGovernanceOptions(args));

// Invoke the actual entrypoints in a fixture repository with no Cargo sources.
// Tools are local protocol fixtures: these tests verify dispatch and fail-closed
// behavior, and do not claim to perform vulnerability or license scans.
const fixtureRoot = await mkdtemp(path.join(os.tmpdir(), "liberty-native-governance-"));
try {
  const put = async (name, content) => {
    await mkdir(path.dirname(path.join(fixtureRoot, name)), { recursive: true });
    await writeFile(path.join(fixtureRoot, name), content);
  };
  for (const name of ["scripts/check-dependency-governance.mjs", "scripts/check-native-runtime-security.mjs", "scripts/generate-sbom.mjs", "scripts/collect-native-licenses.mjs", "scripts/lib/dependency-governance.mjs", "scripts/lib/native-runtime-inputs.mjs", "scripts/lib/native-package.mjs", "scripts/lib/live-translation-inputs.mjs", "packages/desktop-resources/presentation/bloub/source.json", "packages/desktop-resources/presentation/bloub/LICENSE", "packages/desktop-resources/presentation/bloub/NOTICE", "packages/desktop-resources/runtime/runtime-manifest.json", "python/funasr-runner/tools/build_vendor_wheel.py", "python/funasr-runner/vendor/funasr-1.3.9.json", "python/funasr-runner/vendor/source.cdx.json", ...PYTHON_LOCK_FILES.slice(1)]) {
    await mkdir(path.dirname(path.join(fixtureRoot, name)), { recursive: true });
    await copyFile(path.join(repositoryRoot, name), path.join(fixtureRoot, name));
  }
  await put("package.json", JSON.stringify({ type: "module", version: "1.2.3", devDependencies: { builder: "1.0.0" } }));
  await put("config/dependency-governance.json", JSON.stringify(policy));
  await put("config/native-module-licenses.json", JSON.stringify({ "example.test/module@v1.2.3": "MIT" }));
  await put("apps/native/go.mod", fixtureGoModule);
  await put("apps/native/go.sum", fixtureGoSum);
  await put(OSV_EXCEPTION_CONFIG, '[[IgnoredVulns]]\nid = "PYSEC-1"\nignoreUntil = 2099-01-01\nreason = "Synthetic exception with a fixed test scope."\n');
  const pythonLock = `runtime-demo==1.0.0 \\\n    --hash=sha256:${"a".repeat(64)}\n`;
  await put(PYTHON_LOCK_FILES[0], pythonLock.replace("runtime-demo", "dev-demo"));
  const nodeLock = `lockfileVersion: '9.0'
importers:
  .:
    devDependencies:
      builder:
        specifier: 1.0.0
        version: 1.0.0
  apps/desktop:
    dependencies:
      desktop-only:
        specifier: 2.0.0
        version: 2.0.0
packages:
  builder@1.0.0:
    resolution: {integrity: sha512-synthetic}
  transitive@1.1.0:
    resolution: {integrity: sha512-synthetic}
  optional@1.2.0:
    resolution: {integrity: sha512-synthetic}
  desktop-only@2.0.0:
    resolution: {integrity: sha512-synthetic}
snapshots:
  builder@1.0.0:
    dependencies:
      transitive: 1.1.0
    optionalDependencies:
      optional: 1.2.0
  transitive@1.1.0: {}
  optional@1.2.0: {}
  desktop-only@2.0.0: {}
`;
  await put("pnpm-lock.yaml", nodeLock);
  const buildPackage = { devDependencies: { builder: "1.0.0" } };
  assert.deepEqual(nativeNodeDependencies(nodeLock, buildPackage).map(({ name }) => name).sort(), ["builder", "optional", "transitive"]);
  assert.throws(() => nativeNodeDependencies(nodeLock.replace("      transitive: 1.1.0", "      transitive: link:../unlocked"), buildPackage), /Unresolved/);
  assert.throws(() => nativeNodeDependencies(nodeLock, { devDependencies: { builder: "2.0.0" } }), /differs/);
  assert.throws(() => nativeNodeDependencies(nodeLock.replace("  optional@1.2.0: {}", ""), buildPackage), /Unresolved/);

  await put("tool-module/LICENSE", "Synthetic MIT license notice\n");
  await put("toolchain/LICENSE", "Synthetic Go license notice\n");
  await put("toolchain/PATENTS", "Synthetic Go patents notice\n");
  const tool = `#!${process.execPath}
const fs = require('node:fs');
const path = require('node:path');
const name = path.basename(process.argv[1]);
const args = process.argv.slice(2);
if (process.env.TOOL_LOG) fs.appendFileSync(process.env.TOOL_LOG, JSON.stringify({name,args})+'\\n');
if(name==='osv-scanner') {
  if(args[0]==='--version') console.log('osv-scanner version: '+(process.env.BAD_OSV_VERSION||${JSON.stringify(policy.tools.osvScanner)}));
  else {
    const sboms = args.flatMap((argument,index) => argument==='-L'&&args[index+1].endsWith('.cdx.json')?[args[index+1]]:[]);
    if(sboms.length) {
      const results = sboms.map(filename=>({packages:JSON.parse(fs.readFileSync(filename)).components.filter(component=>!(process.env.OMIT_RUNTIME_UPSTREAM&&component.version==='1.3.9')).map(({name,version})=>({package:{ecosystem:'PyPI',name,version}}))}));
      console.log(JSON.stringify({results}));
    }
    process.exit(Number((sboms.length?process.env.RUNTIME_OSV_EXIT:undefined)||process.env.OSV_EXIT||0));
  }
} else if(name==='cargo-deny') {
  if(args[0]==='--version') console.log('cargo-deny ${policy.tools.cargoDeny}');
  else process.exit(Number(process.env.CARGO_EXIT||0));
} else if(name==='go') {
  if(args[0]==='mod'&&args[1]==='download') console.log(JSON.stringify({Path:'example.test/module',Version:'v1.2.3',Sum:${JSON.stringify(fixtureHash)},Dir:path.join(process.env.FIXTURE_ROOT,'tool-module')}));
  else if(args[0]==='env'&&args[1]==='GOROOT') console.log(path.join(process.env.FIXTURE_ROOT,'toolchain'));
  else if(args[0]==='env') console.log('go1.27.4');
  else if(args.join(' ')!=='mod verify') process.exit(3);
}
`;
  for (const name of ["osv-scanner", "cargo-deny", "go"]) {
    await put(`bin/${name}`, tool);
    await chmod(path.join(fixtureRoot, "bin", name), 0o700);
  }
  await put("bin/package.json", '{"type":"commonjs"}\n');
  const toolLog = path.join(fixtureRoot, "tools.jsonl");
  const invoke = (script, args, extraEnv = {}) => spawnSync(process.execPath, [path.join(fixtureRoot, "scripts", script), ...args], {
    cwd: fixtureRoot, encoding: "utf8", env: { ...process.env, PATH: `${path.join(fixtureRoot, "bin")}${path.delimiter}${process.env.PATH}`, FIXTURE_ROOT: fixtureRoot, TOOL_LOG: toolLog, ...extraEnv },
  });
  const succeeds = (result) => assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  succeeds(invoke("check-dependency-governance.mjs", ["locks", "--native"]));
  succeeds(invoke("check-dependency-governance.mjs", ["licenses", "--native"]));
  succeeds(invoke("check-dependency-governance.mjs", ["security", "--native"]));
  const calls = (await readFile(toolLog, "utf8")).trim().split("\n").map(JSON.parse);
  assert(!calls.some(({ name }) => name === "cargo-deny"));
  const scans = calls.filter(({ name, args }) => name === "osv-scanner" && args[0] === "scan");
  assert.equal(scans.length, 9);
  const directScans = scans.filter(({ args }) => args[1] !== "source");
  assert.deepEqual(new Set(directScans.map(({ args }) => args[2])), new Set(["pnpm-lock.yaml", "apps/native/go.mod", PYTHON_LOCK_FILES[0]]));
  assert(directScans.every(({ args }) => args[2].startsWith("python/") ? args.includes("--config") && args.at(-1) === OSV_EXCEPTION_CONFIG : !args.includes("--config")));
  const runtimeScans = scans.filter(({ args }) => args[1] === "source");
  assert.equal(runtimeScans.length, 6);
  assert(runtimeScans.every(({ args }) => args.filter(argument => argument === "-L").length === 2 && args.includes("--all-packages")));
  await writeFile(toolLog, "");
  succeeds(invoke("check-dependency-governance.mjs", ["security"]));
  const combinedCalls = (await readFile(toolLog, "utf8")).trim().split("\n").map(JSON.parse);
  assert(combinedCalls.some(({ name, args }) => name === "cargo-deny" && args[0] === "check"));
  const combinedScans = combinedCalls.filter(({ name, args }) => name === "osv-scanner" && args[0] === "scan");
  assert.equal(combinedScans.length, 9, "combined CI must scan shared inputs once without omitting development tools");
  assert.deepEqual(new Set(combinedScans.filter(({ args }) => args[1] !== "source").map(({ args }) => args[2])), new Set(["pnpm-lock.yaml", "apps/native/go.mod", PYTHON_LOCK_FILES[0]]));
  assert.equal(invoke("check-dependency-governance.mjs", ["security", "--native"], { OSV_EXIT: "1" }).status, 1);
  assert.equal(invoke("check-dependency-governance.mjs", ["security", "--native"], { OSV_EXIT: "2" }).status, 2);
  assert.equal(invoke("check-dependency-governance.mjs", ["security", "--native"], { BAD_OSV_VERSION: "0.0.1" }).status, 2);
  assert.equal(invoke("check-dependency-governance.mjs", ["security", "--native"], { RUNTIME_OSV_EXIT: "1" }).status, 1);
  assert.equal(invoke("check-dependency-governance.mjs", ["security", "--native"], { RUNTIME_OSV_EXIT: "2" }).status, 1);
  assert.equal(invoke("check-dependency-governance.mjs", ["security", "--native"], { OMIT_RUNTIME_UPSTREAM: "1" }).status, 1);
  assert.equal(invoke("check-dependency-governance.mjs", ["security"], { CARGO_EXIT: "7" }).status, 7);

  succeeds(invoke("generate-sbom.mjs", ["--native"]));
  const sbom = JSON.parse(await readFile(path.join(fixtureRoot, "artifacts/sbom/liberty-native.cdx.json")));
  assert.equal(sbom.metadata.component.name, "Liberty Native");
  assert.deepEqual(sbom.components.filter(({ purl }) => purl.startsWith("pkg:npm/")).map(({ name }) => name).sort(), ["builder", "optional", "transitive"]);
  assert(sbom.components.some(({ name, purl }) => name === "example.test/module" && purl.startsWith("pkg:golang/")));
  assert(sbom.components.some(({ name }) => name === "dev-demo"));
  assert(sbom.components.some(({ purl, hashes }) => purl === "pkg:pypi/ctranslate2@4.9.1" && hashes.length === 3));
  assert(sbom.components.some(({ purl }) => purl === "pkg:pypi/funasr@1.3.9%2Bliberty.local2"));
  assert(sbom.components.some(({ purl }) => purl === "pkg:pypi/funasr@1.3.9"));
  assert.deepEqual(sbom.dependencies, [{ ref: "pkg:pypi/funasr@1.3.9%2Bliberty.local2", dependsOn: ["pkg:pypi/funasr@1.3.9"] }]);
  assert(!sbom.components.some(({ purl }) => purl.startsWith("pkg:cargo/")));
  assert(sbom.components.every(({ properties }) => properties.some(({ name }) => name === "liberty:source-lock")));
  assert(sbom.metadata.properties.some(({ name, value }) => name === "liberty:sbom:scope" && value.includes("Not an installed")));
  assert.notEqual(invoke("generate-sbom.mjs", []).status, 0);
  await put("Cargo.lock", 'version = 4\n[[package]]\nname = "rust-demo"\nversion = "1.0.0"\n');
  succeeds(invoke("generate-sbom.mjs", []));
  const defaultSBOM = JSON.parse(await readFile(path.join(fixtureRoot, "artifacts/sbom/liberty.cdx.json")));
  assert(defaultSBOM.components.some(({ purl }) => purl === "pkg:cargo/rust-demo@1.0.0"));
  assert(defaultSBOM.components.some(({ name }) => name === "desktop-only"));
  assert(defaultSBOM.components.some(({ purl }) => purl === "pkg:pypi/funasr@1.3.9"));
  const sourceSBOMPath = path.join(fixtureRoot, "python/funasr-runner/vendor/source.cdx.json");
  const sourceSBOM = await readFile(sourceSBOMPath);
  await writeFile(sourceSBOMPath, Buffer.concat([sourceSBOM, Buffer.from("\n")]));
  assert.notEqual(invoke("check-dependency-governance.mjs", ["locks", "--native"]).status, 0);
  assert.notEqual(invoke("generate-sbom.mjs", ["--native"]).status, 0);
  await writeFile(sourceSBOMPath, sourceSBOM);
  await put("apps/native/go.sum", "");
  assert.notEqual(invoke("check-dependency-governance.mjs", ["locks", "--native"]).status, 0);
  assert.notEqual(invoke("generate-sbom.mjs", ["--native"]).status, 0);
  await put("apps/native/go.sum", fixtureGoSum);
  await put("config/native-module-licenses.json", JSON.stringify({ "example.test/module@v1.2.3": "GPL-3.0-only" }));
  assert.notEqual(invoke("check-dependency-governance.mjs", ["licenses", "--native"]).status, 0);
  await put("pnpm-lock.yaml", nodeLock.replaceAll("resolution: {integrity: sha512-synthetic}", "resolution: {}"));
  assert.notEqual(invoke("check-dependency-governance.mjs", ["locks", "--native"]).status, 0);
  assert.notEqual(invoke("generate-sbom.mjs", ["--native"]).status, 0);
  await put("pnpm-lock.yaml", nodeLock);
  await put(PYTHON_LOCK_FILES[0], "unhashed==1.0.0\n");
  assert.notEqual(invoke("check-dependency-governance.mjs", ["locks", "--native"]).status, 0);
  assert.notEqual(invoke("generate-sbom.mjs", ["--native"]).status, 0);
} finally {
  await rm(fixtureRoot, { recursive: true, force: true });
}
console.log("Dependency governance blocking fixtures passed.");
