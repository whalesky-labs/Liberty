import { access, readFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { loadRuntimeInputManifest, readProductionRuntimeScanInputs } from "./lib/native-runtime-inputs.mjs";
import {
  CARGO_DENY_SECURITY_CHECKS,
  OSV_EXCEPTION_CONFIG,
  OSV_LOCK_FILES,
  PYTHON_LOCK_FILES,
  assertGoLockHasIntegrity,
  assertNodeLockHasIntegrity,
  assertOsvExceptionsAreGoverned,
  assertPythonLockHasHashes,
  dependencyGovernanceOptions,
  readPolicy,
} from "./lib/dependency-governance.mjs";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const policy = await readPolicy(path.join(repositoryRoot, "config/dependency-governance.json"));
const { mode, native } = dependencyGovernanceOptions(process.argv.slice(2));

assertNodeLockHasIntegrity(await readFile(path.join(repositoryRoot, "pnpm-lock.yaml"), "utf8"));
assertGoLockHasIntegrity(
  await readFile(path.join(repositoryRoot, "apps/native/go.mod"), "utf8"),
  await readFile(path.join(repositoryRoot, "apps/native/go.sum"), "utf8"),
);
if (policy.requireHashesForPythonLocks) {
  for (const lock of PYTHON_LOCK_FILES) {
    assertPythonLockHasHashes(await readFile(path.join(repositoryRoot, lock), "utf8"), lock);
  }
}
assertOsvExceptionsAreGoverned(
  await readFile(path.join(repositoryRoot, OSV_EXCEPTION_CONFIG), "utf8"),
);
await readProductionRuntimeScanInputs(repositoryRoot, await loadRuntimeInputManifest(repositoryRoot));

if (mode === "locks") {
  console.log("Dependency lock integrity policy passed.");
  process.exit(0);
}

if (mode === "licenses") await import("./collect-native-licenses.mjs");

const requiredCommands = [...(native ? [] : ["cargo-deny"]), ...(mode === "security" ? ["osv-scanner"] : [])];
for (const command of requiredCommands) {
  const lookup = spawnSync("sh", ["-c", `command -v ${command}`], { encoding: "utf8" });
  if (lookup.status !== 0) {
    console.error(`${command} is required. Install the pinned CI version before running ${mode}:check.`);
    process.exit(2);
  }
}

if (!native) assertToolVersion("cargo-deny", policy.tools.cargoDeny);
if (mode === "security") assertToolVersion("osv-scanner", policy.tools.osvScanner);

if (!native) {
  const cargoDenyChecks = mode === "licenses" ? ["licenses"] : CARGO_DENY_SECURITY_CHECKS;
  const cargoDeny = spawnSync("cargo-deny", ["check", ...cargoDenyChecks], {
    cwd: repositoryRoot,
    stdio: "inherit",
  });
  if (cargoDeny.status !== 0) process.exit(cargoDeny.status ?? 1);
}

if (mode === "security") {
  // Local vendor versions are not published on PyPI. Scan the production locks
  // as pinned SBOMs together with the authenticated upstream identity instead.
  const locks = [...OSV_LOCK_FILES.filter((lock) => !lock.startsWith("python/")), ...(native ? [PYTHON_LOCK_FILES[0]] : [])];
  for (const lockFile of locks) {
    await access(path.join(repositoryRoot, lockFile));
    const scan = spawnSync("osv-scanner", ["scan", "-L", lockFile, ...(native && lockFile.startsWith("python/") ? ["--config", OSV_EXCEPTION_CONFIG] : [])], {
      cwd: repositoryRoot,
      stdio: "inherit",
    });
    if (scan.status !== 0) {
      process.exit(scan.status ?? 1);
    }
  }
  const runtime = spawnSync(process.execPath, [path.join(repositoryRoot, "scripts/check-native-runtime-security.mjs"), "--production"], {
    cwd: repositoryRoot,
    stdio: "inherit",
  });
  if (runtime.status !== 0) process.exit(runtime.status ?? 1);
}

console.log(`${native ? "Native " : ""}${mode} dependency governance passed.`);

function assertToolVersion(command, expectedVersion) {
  const result = spawnSync(command, ["--version"], { encoding: "utf8" });
  const output = `${result.stdout ?? ""}\n${result.stderr ?? ""}`;
  if (result.status !== 0 || !new RegExp(`(^|[^0-9])${escapeRegex(expectedVersion)}([^0-9]|$)`).test(output)) {
    console.error(`${command} must be pinned to ${expectedVersion}; received ${output.trim() || "no version"}.`);
    process.exit(2);
  }
}

function escapeRegex(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
