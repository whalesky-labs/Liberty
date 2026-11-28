import { execFile, spawn } from "node:child_process";
import { lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { loadRuntimeInputManifest, publishRuntimeInputs, readRuntimeInputs, runtimeInputDigest, runtimeInputPins, securityRuntimeProfile } from "./lib/native-runtime-inputs.mjs";
import { assertCandidateManifest, candidateName, fileDigest, nativeTargets } from "./lib/native-package.mjs";
import { unpackNativeFixtures } from "./lib/native-fixtures.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const native = path.join(root, "apps/native");
const env = { ...process.env, GOTOOLCHAIN: "go1.27.4", GOFLAGS: "-mod=readonly" };
const execFileAsync = promisify(execFile);
const candidatePackagePrefix = "liberty-native-candidate-";

function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd: native, env, stdio: "inherit", ...options });
    child.once("error", reject);
    child.once("exit", (code, signal) => code === 0 ? resolve() : reject(new Error(`${command} failed (${signal ?? code})`)));
  });
}

async function goFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  return (await Promise.all(entries.map((e) => e.isDirectory()
    ? goFiles(path.join(directory, e.name))
    : e.name.endsWith(".go") ? [path.join(directory, e.name)] : []))).flat();
}

async function check() {
  await run(process.execPath, ["--test", path.join(root, "scripts/test-native-source-isolation.mjs")], { cwd: root });
  await run(process.execPath, ["--test", path.join(root, "scripts/test-native-fixtures.mjs")], { cwd: root });
  await run(process.execPath, ["--test", path.join(root, "scripts/test-native-launch.mjs")], { cwd: root });
  await run(process.execPath, ["--test", path.join(root, "scripts/test-native-package.mjs")], { cwd: root });
  await run(process.execPath, ["--test", path.join(root, "scripts/test-native-bloub.mjs")], { cwd: root });
  await run(process.execPath, ["--test", path.join(root, "scripts/test-native-runtime-inputs.mjs")], { cwd: root });
  await run(process.execPath, ["--experimental-vm-modules", "--test", path.join(root, "scripts/test-native-runtime-security.mjs")], { cwd: root });
  await run(process.execPath, ["--test", path.join(root, "packages/desktop-resources/pet/validate.test.mjs")], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-contracts.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-runtime.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-migrations.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-export.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-presentation.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-work-art.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-pet-art.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-pet.mjs"), "--check"], { cwd: root });
  await run(process.execPath, [path.join(root, "scripts/generate-native-redemption.mjs"), "--check"], { cwd: root });
  // gofmt -l exits successfully on unformatted input; explicitly reject output.
  const files = await goFiles(native);
  await new Promise((resolve, reject) => {
    const child = spawn("gofmt", ["-l", ...files], { cwd: native, env, stdio: ["ignore", "pipe", "inherit"] });
    let output = "";
    child.stdout.on("data", (data) => { output += data; });
    child.once("error", reject);
    child.once("exit", (code) => code === 0 && !output.trim() ? resolve() : reject(new Error("Go files need formatting; run gofmt in apps/native.")));
  });
  await run("go", ["mod", "verify"]);
  await run("go", ["vet", "./..."]);
  await run("go", ["test", "./..."]);
}

export function nativeLaunchTarget(platform, arch) {
  const osName = platform === "win32" ? "windows" : platform;
  const architecture = arch === "x64" ? "amd64" : arch;
  const target = Object.entries(nativeTargets).find(([, value]) => value.os === osName && value.arch === architecture)?.[0];
  if (!target) throw new Error(`Native candidate launch is unsupported on ${platform}/${arch}`);
  return target;
}

export function nativeLaunchSpec(platform, packageDirectory, targetID) {
  const target = nativeTargets[targetID];
  if (!target) throw new Error(`Unknown native candidate target: ${targetID}`);
  if (platform === "darwin" && target.os === "darwin") {
    return { command: "open", args: [path.join(packageDirectory, `${candidateName}.app`)] };
  }
  if (platform === "win32" && target.os === "windows") {
    return { command: path.join(packageDirectory, candidateExecutable(targetID)), args: [], detached: true };
  }
  throw new Error(`Native candidate package ${targetID} does not match host ${platform}`);
}

function candidateExecutable(targetID) {
  return nativeTargets[targetID].os === "darwin"
    ? `${candidateName}.app/Contents/MacOS/liberty-native`
    : `${candidateName}.exe`;
}

export async function runningNativeCandidatePaths(platform, inspect = execFileAsync) {
  if (platform === "darwin") {
    let processIDs;
    try {
      ({ stdout: processIDs } = await inspect("pgrep", ["-x", "liberty-native"], { encoding: "utf8" }));
    } catch (error) {
      if (error.code === 1) return [];
      throw new Error("Could not inspect running Liberty native processes safely.", { cause: error });
    }
    const ids = processIDs.trim().split(/\s+/);
    if (ids.some((id) => !/^[1-9]\d*$/.test(id))) throw new Error("Could not identify running Liberty native processes safely.");
    const executablePaths = [];
    for (const processID of ids) {
      try {
        const { stdout } = await inspect("lsof", ["-a", "-p", processID, "-d", "txt", "-F0fn"], { encoding: "utf8" });
        const paths = stdout.split("\0").filter((field) => field.startsWith("n")).map((field) => field.slice(1));
        const executables = paths.filter((executable) => path.basename(executable) === "liberty-native");
        if (executables.length !== 1) throw new Error("Expected exactly one executable path for the process.");
        executablePaths.push(executables[0]);
      } catch (error) {
        throw new Error("Could not verify a running Liberty native executable safely.", { cause: error });
      }
    }
    return executablePaths;
  }
  if (platform === "win32") {
    try {
      const { stdout } = await inspect("powershell.exe", [
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        `$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = [Text.Encoding]::UTF8; $processes = @(Get-CimInstance Win32_Process | Where-Object { $_.Name -in @('${candidateName}.exe', 'liberty-native.exe') } | Select-Object -Property ExecutablePath); ConvertTo-Json -InputObject $processes -Compress`,
      ], { encoding: "utf8" });
      const processes = JSON.parse(stdout);
      if (!Array.isArray(processes) || processes.some((record) => typeof record?.ExecutablePath !== "string" || !record.ExecutablePath.trim())) {
        throw new Error("Missing native executable path.");
      }
      return processes.map((record) => record.ExecutablePath);
    } catch (error) {
      throw new Error("Could not inspect running Liberty native processes safely.", { cause: error });
    }
  }
  throw new Error(`Native candidate process inspection is unsupported on ${platform}`);
}

export function windowsNativeActivationSpec(executable) {
  if (typeof executable !== "string" || !executable || executable.includes("\0")) throw new Error("Invalid native activation executable");
  // Encode the path as data; no path text is interpreted as PowerShell code.
  const encodedPath = Buffer.from(executable, "utf8").toString("base64");
  const script = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$expected = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('${encodedPath}'))
$nativeProcesses = @(Get-CimInstance Win32_Process | Where-Object { $_.Name -in @('${candidateName}.exe', 'liberty-native.exe') })
if ($nativeProcesses.Count -ne 1 -or $nativeProcesses[0].ExecutablePath -ine $expected) { throw 'Native process identity changed' }
$nativeProcessId = [uint32]$nativeProcesses[0].ProcessId
$created = $nativeProcesses[0].CreationDate
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class LibertyNativeActivation {
  public delegate bool EnumWindow(IntPtr window, IntPtr data);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindow callback, IntPtr data);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint process);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window, StringBuilder title, int length);
  [DllImport("user32.dll")] public static extern IntPtr GetWindow(IntPtr window, uint command);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
  [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr window);
  [DllImport("user32.dll")] public static extern bool ShowWindowAsync(IntPtr window, int command);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  public static IntPtr Find(uint process, string expectedTitle) {
    var windows = new List<IntPtr>();
    EnumWindows((window, data) => {
      uint owner; GetWindowThreadProcessId(window, out owner);
      if (owner == process && IsWindowVisible(window) && GetWindow(window, 4) == IntPtr.Zero) {
        var title = new StringBuilder(512); GetWindowText(window, title, title.Capacity);
        if (title.ToString() == expectedTitle) windows.Add(window);
      }
      return true;
    }, IntPtr.Zero);
    if (windows.Count != 1) throw new InvalidOperationException("Expected one Native main window");
    return windows[0];
  }
}
'@
$window = [LibertyNativeActivation]::Find($nativeProcessId, '${candidateName}')
$ownedProcess = [Diagnostics.Process]::GetProcessById([int]$nativeProcessId)
$heldProcessHandle = $ownedProcess.Handle
$current = Get-CimInstance Win32_Process -Filter "ProcessId = $nativeProcessId"
if ($heldProcessHandle -eq [IntPtr]::Zero -or $ownedProcess.HasExited -or $null -eq $current -or $current.ExecutablePath -ine $expected -or $current.CreationDate -ne $created) { throw 'Native process identity changed' }
if ([LibertyNativeActivation]::IsIconic($window)) { [void][LibertyNativeActivation]::ShowWindowAsync($window, 9) }
[void][LibertyNativeActivation]::SetForegroundWindow($window)
for ($attempt = 0; $attempt -lt 20; $attempt++) {
  if ($ownedProcess.HasExited) { throw 'Native process exited during activation' }
  [uint32]$owner = 0
  [void][LibertyNativeActivation]::GetWindowThreadProcessId($window, [ref]$owner)
  if ($owner -eq $nativeProcessId -and [LibertyNativeActivation]::GetForegroundWindow() -eq $window -and -not [LibertyNativeActivation]::IsIconic($window)) {
    @{ status = 'activated'; processId = $nativeProcessId; windowHandle = $window.ToInt64().ToString(); foreground = $true; minimized = $false } | ConvertTo-Json -Compress
    exit 0
  }
  Start-Sleep -Milliseconds 100
}
throw 'Windows did not activate the verified Native main window'
`;
  return { command: "powershell.exe", args: ["-NoProfile", "-NonInteractive", "-EncodedCommand", Buffer.from(script, "utf16le").toString("base64")] };
}

export async function activateWindowsNativeCandidate(executable, inspect = execFileAsync) {
  const spec = windowsNativeActivationSpec(executable);
  try {
    const { stdout } = await inspect(spec.command, spec.args, { encoding: "utf8", timeout: 15_000, maxBuffer: 1024 * 1024, windowsHide: true });
    const result = JSON.parse(stdout.trim());
    if (result?.status !== "activated" || !Number.isSafeInteger(result.processId) || result.processId <= 0 ||
        !/^[1-9]\d*$/.test(result.windowHandle) || result.foreground !== true || result.minimized !== false) {
      throw new Error("Missing confirmed foreground Native window");
    }
    return result;
  } catch (error) {
    throw new Error("Could not activate the verified Liberty Native main window. No duplicate process was started.", { cause: error });
  }
}

async function readCandidateFile(packageDirectory, relativePath) {
  const parts = relativePath.split("/");
  let current = packageDirectory;
  for (const [index, part] of parts.entries()) {
    current = path.join(current, part);
    const info = await lstat(current);
    if (info.isSymbolicLink() || (index < parts.length - 1 ? !info.isDirectory() : !info.isFile())) {
      throw new Error(`Native candidate contains a non-regular package path: ${relativePath}`);
    }
  }
  return readFile(current);
}

function compareCandidateVersions(left, right) {
  const parse = (version) => {
    const withoutBuild = version.split("+", 1)[0];
    const separator = withoutBuild.indexOf("-");
    const core = separator === -1 ? withoutBuild : withoutBuild.slice(0, separator);
    const prerelease = separator === -1 ? undefined : withoutBuild.slice(separator + 1).split(".");
    return { core: core.split("."), prerelease };
  };
  const compareNumericIdentifiers = (leftIdentifier, rightIdentifier) => {
    const leftNormalized = leftIdentifier.replace(/^0+(?=\d)/, "");
    const rightNormalized = rightIdentifier.replace(/^0+(?=\d)/, "");
    if (leftNormalized.length !== rightNormalized.length) return leftNormalized.length - rightNormalized.length;
    return leftNormalized < rightNormalized ? -1 : leftNormalized > rightNormalized ? 1 : 0;
  };
  const leftVersion = parse(left);
  const rightVersion = parse(right);
  for (let index = 0; index < 3; index += 1) {
    if (leftVersion.core[index] !== rightVersion.core[index]) return compareNumericIdentifiers(leftVersion.core[index], rightVersion.core[index]);
  }
  if (!leftVersion.prerelease || !rightVersion.prerelease) {
    return leftVersion.prerelease ? -1 : rightVersion.prerelease ? 1 : 0;
  }
  for (let index = 0; index < Math.max(leftVersion.prerelease.length, rightVersion.prerelease.length); index += 1) {
    const leftPart = leftVersion.prerelease[index];
    const rightPart = rightVersion.prerelease[index];
    if (leftPart === undefined || rightPart === undefined) return leftPart === undefined ? -1 : 1;
    if (leftPart === rightPart) continue;
    const leftNumeric = /^\d+$/.test(leftPart);
    const rightNumeric = /^\d+$/.test(rightPart);
    if (leftNumeric && rightNumeric) return compareNumericIdentifiers(leftPart, rightPart);
    if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
    return leftPart < rightPart ? -1 : 1;
  }
  return 0;
}

async function candidateContentDirectories(directory) {
  if (!(await lstat(directory)).isDirectory()) throw new Error("Native candidate package is not a regular directory");
  const directManifest = path.join(directory, "candidate-manifest.json");
  const directInfo = await lstat(directManifest).catch((error) => {
    if (error.code === "ENOENT") return null;
    throw error;
  });
  if (directInfo?.isSymbolicLink()) throw new Error("Native candidate manifest is a symbolic link");
  if (directInfo?.isFile()) return [directory];

  const entries = await readdir(directory, { withFileTypes: true });
  const nested = [];
  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const content = path.join(directory, entry.name);
    const manifest = path.join(content, "candidate-manifest.json");
    const info = await lstat(manifest).catch((error) => {
      if (error.code === "ENOENT") return null;
      throw error;
    });
    if (info?.isSymbolicLink()) throw new Error("Native candidate manifest is a symbolic link");
    if (info?.isFile()) nested.push(content);
  }
  if (nested.length > 1) throw new Error("Native candidate has multiple content directories");
  return nested;
}

async function readCandidateManifest(directory, packageName) {
  try {
    return JSON.parse(await readCandidateFile(directory, "candidate-manifest.json"));
  } catch (error) {
    throw new Error(`Invalid native candidate package: ${packageName}`, { cause: error });
  }
}

async function verifyCandidateFiles(directory, manifest, packageName) {
  assertCandidateManifest(manifest);
  if (!manifest.files.some((file) => file.path === candidateExecutable(manifest.target))) {
    throw new Error(`Native candidate executable is missing from inventory: ${packageName}`);
  }
  for (const file of manifest.files) {
    const bytes = await readCandidateFile(directory, file.path).catch((error) => {
      if (error.message.startsWith("Native candidate contains a non-regular package path:")) throw error;
      throw new Error(`Native candidate file is missing: ${packageName}/${file.path}`, { cause: error });
    });
    if (bytes.length !== file.bytes || fileDigest(bytes) !== file.sha256) {
      throw new Error(`Native candidate file failed integrity verification: ${packageName}/${file.path}`);
    }
  }
}

export async function findLatestNativeCandidate(packagesDirectory, targetID) {
  if (!nativeTargets[targetID]) throw new Error(`Unknown native candidate target: ${targetID}`);
  const entries = await readdir(packagesDirectory, { withFileTypes: true }).catch((error) => {
    if (error.code === "ENOENT") return [];
    throw error;
  });
  const candidates = [];
  for (const entry of entries) {
    if (!entry.isDirectory() || !entry.name.startsWith(candidatePackagePrefix)) continue;
    const packageRoot = path.join(packagesDirectory, entry.name);
    let contentDirectories;
    try {
      contentDirectories = await candidateContentDirectories(packageRoot);
    } catch (error) {
      throw new Error(`Invalid native candidate package: ${entry.name}`, { cause: error });
    }
    if (contentDirectories.length === 0) throw new Error(`Invalid native candidate package: ${entry.name}`);
    for (const directory of contentDirectories) {
      const manifest = await readCandidateManifest(directory, entry.name);
      if (manifest.target !== targetID) continue;
      await verifyCandidateFiles(directory, manifest, entry.name);
      candidates.push({ directory, version: manifest.version, modifiedAt: (await stat(packageRoot)).mtimeMs });
    }
  }
  candidates.sort((left, right) => compareCandidateVersions(right.version, left.version) || right.modifiedAt - left.modifiedAt);
  return candidates[0]?.directory ?? null;
}

async function findRunningNativeCandidate(packagesDirectory, targetID, binaryPath) {
  if (typeof binaryPath !== "string" || !path.isAbsolute(binaryPath) || path.resolve(binaryPath) !== binaryPath) {
    throw new Error("Invalid running native executable path");
  }
  if (!(await lstat(packagesDirectory)).isDirectory()) throw new Error("Native packages root is not a regular directory");
  const canonicalPackages = await realpath(packagesDirectory);
  const relativeBinary = [packagesDirectory, canonicalPackages]
    .map((directory) => path.relative(directory, binaryPath))
    .find((relative) => relative.startsWith(candidatePackagePrefix));
  if (!relativeBinary) throw new Error("Running executable is outside the native candidate packages");

  const [packageName] = relativeBinary.split(path.sep);
  const [directory] = await candidateContentDirectories(path.join(canonicalPackages, packageName));
  const expectedBinary = directory && path.join(directory, candidateExecutable(targetID));
  if (!expectedBinary || path.relative(expectedBinary, path.join(canonicalPackages, relativeBinary)) !== "") {
    throw new Error("Running executable does not match the exact package executable");
  }
  const manifest = await readCandidateManifest(directory, packageName);
  if (manifest.target !== targetID) throw new Error("Running native candidate target does not match this host");
  await verifyCandidateFiles(directory, manifest, packageName);
  if (await realpath(binaryPath) !== await realpath(expectedBinary)) {
    throw new Error("Running executable does not resolve to the verified package executable");
  }
  return directory;
}

async function executeNativeLaunch(spec, packageDirectory) {
  if (spec.detached) {
    await new Promise((resolve, reject) => {
      const child = spawn(spec.command, spec.args, { cwd: packageDirectory, env, detached: true, stdio: "ignore" });
      child.once("error", reject);
      child.once("spawn", () => {
        child.unref();
        resolve();
      });
    });
    return;
  }
  await run(spec.command, spec.args, { cwd: root });
}

export async function launchNativeCandidate({
  platform = process.platform,
  arch = process.arch,
  packagesDirectory = path.join(root, "target/native/packages"),
  inspect = runningNativeCandidatePaths,
  execute = executeNativeLaunch,
  activateWindows = activateWindowsNativeCandidate,
  log = console.log,
} = {}) {
  const targetID = nativeLaunchTarget(platform, arch);
  const runningPaths = await inspect(platform);
  if (runningPaths.length > 1) {
    throw new Error("Multiple Liberty native instances are already running. Close them normally before launching a candidate; no running process was stopped.");
  }
  if (runningPaths.length === 1) {
    let directory;
    try {
      directory = await findRunningNativeCandidate(packagesDirectory, targetID, runningPaths[0]);
    } catch (error) {
      throw new Error("Could not verify the running Liberty native candidate. Close it normally before launching a packaged candidate; no running process was stopped.", { cause: error });
    }
    if (platform === "win32") {
      const result = await activateWindows(path.join(directory, candidateExecutable(targetID)));
      log(`Activated verified Native main window (process ${result.processId}, foreground confirmed). No duplicate process was started.`);
    } else {
      log(`Activating verified native candidate: ${directory}`);
      await execute(nativeLaunchSpec(platform, directory, targetID), directory);
    }
    log("To use a newer package, save your work and quit the running candidate normally, then run pnpm native:launch again.");
    return;
  }

  const directory = await findLatestNativeCandidate(packagesDirectory, targetID);
  if (!directory) throw new Error(`No packaged native candidate for ${targetID}; run pnpm native:package first.`);
  log(`Launching verified native candidate: ${directory}`);
  await execute(nativeLaunchSpec(platform, directory, targetID), directory);
}

async function compatibility(preview) {
  const temporaryRoot = os.tmpdir();
  const directory = await mkdtemp(path.join(temporaryRoot, "native-compat-"));
  const fixtureEnv = { ...env, LIBERTY_NATIVE_FIXTURE_DIR: directory };
  try {
    await unpackNativeFixtures(directory);
    await run("go", ["test", "-tags", "compatibility", "./internal/domain", "-run", "^TestRust(TextExports|Minutes|AI)$", "-count=1", "-v"], { env: fixtureEnv });
    await run("go", ["test", "-tags", "compatibility", "./internal/infrastructure/repositories/legacydb", "-run", "^TestRustDatabaseCompatibility$", "-count=1", "-v"], { env: fixtureEnv });
    const raceFlags = ["arm64", "x64"].includes(process.arch) ? ["-race"] : [];
    await run("go", ["test", ...raceFlags, "-tags", "compatibility", "./internal/infrastructure/repositories/schema", "-count=1", "-v"], { env: fixtureEnv });
    await run("go", ["test", ...raceFlags, "-tags", "compatibility", "./internal/infrastructure/repositories/jobdb", "./internal/infrastructure/profile", "./internal/infrastructure/execution", "./internal/host", "./internal/infrastructure/memberxlsx", "./internal/infrastructure/minutesdocx", "-run", "^(TestWrite|TestLock|TestRustEscapedWorkbookInteroperability$)", "-count=1", "-timeout=30m", "-v"], { env: fixtureEnv });
    if (preview) {
      const binary = process.platform === "darwin"
        ? path.join(directory, "Liberty Native Preview.app/Contents/MacOS/liberty-native")
        : path.join(directory, process.platform === "win32" ? "liberty-native.exe" : "liberty-native");
      await mkdir(path.dirname(binary), { recursive: true });
      if (process.platform === "darwin") {
        // Isolated preview identity: never register the production bundle ID.
        await writeFile(path.join(path.dirname(binary), "../Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.westng.liberty.native-preview</string>
<key>CFBundleName</key><string>Liberty Native Preview</string>
<key>CFBundleExecutable</key><string>liberty-native</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>\n`);
      }
      await run("go", ["build", "-o", binary, "./cmd/liberty-native"]);
      const previewArgs = process.argv.slice(3);
      const writable = previewArgs.includes("--writable");
      if (writable) previewArgs.splice(previewArgs.indexOf("--writable"), 1);
      await run(binary, [writable ? "--profile-directory" : "--database", writable ? directory : path.join(directory, "liberty.sqlite3"), ...(writable ? ["--writable"] : []), ...previewArgs], { env: fixtureEnv });
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

async function buildMatrix() {
  const output = path.join(root, "target/native");
  await mkdir(output, { recursive: true });
  const targets = [["darwin", "arm64"], ["darwin", "amd64"], ["windows", "amd64"]];
  const results = await Promise.allSettled(targets.map(async ([os, arch]) => {
    const binary = path.join(output, `liberty-${os}-${arch}${os === "windows" ? ".exe" : ""}`);
    const flags = os === "windows" ? "-s -w -H windowsgui" : "-s -w";
    const targetEnv = { ...env, CGO_ENABLED: "0", GOOS: os, GOARCH: arch };
    await run("go", ["build", "-trimpath", `-ldflags=${flags}`, "-o", binary, "./cmd/liberty-native"], { env: targetEnv });
    const tests = path.join(output, `tests-${os}-${arch}`);
    await mkdir(tests, { recursive: true });
    // Compile packages not yet reachable from the preview entry (e.g. Runner).
    // These are target-format checks, not claims of cross-platform test execution.
    await run("go", ["test", "-tags", "compatibility", "-c", "-o", tests + path.sep, "./..."], { env: targetEnv });
    const bytes = await readFile(binary);
    if (os === "windows") {
      const pe = bytes.readUInt32LE(0x3c);
      if (bytes.toString("ascii", 0, 2) !== "MZ" || bytes.readUInt32LE(pe) !== 0x4550 || bytes.readUInt16LE(pe+4) !== 0x8664) throw new Error(`Unexpected PE architecture: ${arch}`);
    } else if (bytes.readUInt32LE(0) !== 0xfeedfacf || bytes.readUInt32LE(4) !== (arch === "arm64" ? 0x100000c : 0x1000007)) {
      throw new Error(`Unexpected Mach-O architecture: ${arch}`);
    }
    console.log(`Verified binary format: ${os}/${arch}`);
  }));
  const failures = results.filter((r) => r.status === "rejected");
  if (failures.length) throw new AggregateError(failures.map((r) => r.reason), "Native target builds failed");
}

async function benchmarkReads() {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-reads-"));
  const fixtureEnv = { ...env, LIBERTY_NATIVE_FIXTURE_DIR: directory };
  try {
    await unpackNativeFixtures(directory);
    await run("go", ["test", "-tags", "compatibility", "./internal/infrastructure/repositories/legacydb", "-run", "^TestCompareReleaseReadLatency$", "-count=1", "-v"], { env: fixtureEnv });
    await run("go", ["test", "-tags", "compatibility", "./internal/infrastructure/repositories/legacydb", "-run", "^$", "-bench", "^BenchmarkReadProjection$", "-benchmem", "-count=3"], { env: fixtureEnv });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

export function productionRuntimeAcceptanceDirectory(manifest, platform = process.platform, arch = process.arch) {
  const { runtimeVersion, modelSetVersion, modelProfile } = manifest;
  const versions = [runtimeVersion, modelSetVersion, modelProfile];
  if (versions.some((value) => typeof value !== "string" || !value.trim())) {
    throw new Error("Missing runtime or model identity for acceptance");
  }
  // Ready markers are version-bound and carry absolute paths. Keep old
  // generations intact and share only the current cache between both flows.
  const key = fileDigest(JSON.stringify([nativeLaunchTarget(platform, arch), ...versions])).slice(0, 16);
  return path.join(root, "target/native/runtime-acceptance", key);
}

async function runtimeAcceptance() {
  const directory = productionRuntimeAcceptanceDirectory(await loadRuntimeInputManifest(root));
  await mkdir(directory, { recursive: true });
  await writeFile(path.join(directory, ".native-runtime-acceptance"), "isolated-runtime-acceptance-v1\n");
  await run("go", ["test", "-tags", "runtime_acceptance", "./internal/infrastructure/managed", "-run", "^(TestPinnedRuntimeAcceptance|TestInstalledRuntimeInventoryAcceptance)$", "-count=1", "-timeout=95m", "-v"], {
    env: { ...env, LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR: directory },
  });
  if (["darwin", "win32"].includes(process.platform)) {
    await run("go", ["test", "-tags", "runtime_acceptance", "./internal/host", "-run", "^TestPinnedHostRuntimeAcceptance$", "-count=1", "-timeout=15m", "-v"], {
      env: { ...env, LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR: directory },
    });
  }
  const inventory = JSON.parse(await readFile(path.join(directory, "installed-inventory.json"), "utf8"));
  if (inventory.runtimeProfile !== "production" || !/^[a-f0-9]{64}$/.test(inventory.productionSourceSha256) || inventory.platform !== nativeLaunchTarget(process.platform, process.arch)) throw new Error("Installed production runtime inventory is not bound to this acceptance");
  const temporary = await mkdtemp(path.join(directory, "scan-inputs-"));
  try {
    const inputs = path.join(temporary, "inputs");
    const vendor = path.join(directory, "runtime", inventory.platform, "vendor-inputs", inventory.productionSourceSha256);
    await run(process.execPath, [path.join(root, "scripts/prepare-native-runtime.mjs"), "--profile", "production", "--locks-directory", path.join(root, "python/funasr-runner"), "--vendor-directory", vendor, "--output-directory", inputs], { cwd: root });
    await run(process.execPath, [path.join(root, "scripts/check-native-runtime-security.mjs"), "--production", "--runtime-input-directory", inputs, "--installed-inventory", path.join(directory, "installed-inventory.json")], { cwd: root });
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

export function candidateRuntimeAcceptanceDirectory(manifest, inputDigest, platform = process.platform, arch = process.arch) {
  const { runtimeVersion } = manifest.candidateProfiles[securityRuntimeProfile];
  const { modelSetVersion, modelProfile } = manifest;
  // The installer binds ready markers to these versions and the verified inputs.
  const key = fileDigest(JSON.stringify([
    nativeLaunchTarget(platform, arch), runtimeVersion, modelSetVersion, modelProfile, inputDigest,
  ])).slice(0, 16);
  return path.join(root, "target/native/runtime-candidate-acceptance", key);
}

async function candidateRuntimeAcceptance() {
  const args = process.argv.slice(3);
  if (args.length !== 2 || args[0] !== "--runtime-input-directory" || !path.isAbsolute(args[1])) throw new Error("Expected --runtime-input-directory <absolute-directory>");
  const manifest = await loadRuntimeInputManifest(root);
  const pins = runtimeInputPins(manifest);
  const files = await readRuntimeInputs(args[1], pins);
  const directory = candidateRuntimeAcceptanceDirectory(manifest, runtimeInputDigest(files));
  await mkdir(directory, { recursive: true });
  await writeFile(path.join(directory, ".native-runtime-acceptance"), "isolated-runtime-acceptance-v1\n");
  const temporary = await mkdtemp(path.join(directory, "host-"));
  try {
    const app = process.platform === "darwin" ? path.join(temporary, "Liberty Runtime Acceptance.app/Contents") : temporary;
    const binaryDirectory = process.platform === "darwin" ? path.join(app, "MacOS") : app;
    const resources = process.platform === "darwin" ? path.join(app, "Resources") : app;
    await mkdir(binaryDirectory, { recursive: true });
    await mkdir(resources, { recursive: true });
    await publishRuntimeInputs(path.join(resources, "runtime-inputs"), files, pins);
    const acceptanceEnv = { ...env, LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR: directory, LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_PROFILE: securityRuntimeProfile };
    for (const [name, packageName, testName, timeout] of [["managed", "./internal/infrastructure/managed", "TestPinnedRuntimeAcceptance|TestInstalledRuntimeInventoryAcceptance", "95m"], ...(["darwin", "win32"].includes(process.platform) ? [["host", "./internal/host", "TestPinnedHostRuntimeAcceptance", "15m"]] : [])]) {
      const binary = path.join(binaryDirectory, `${name}-acceptance${process.platform === "win32" ? ".exe" : ""}`);
      await run("go", ["test", "-tags", "runtime_acceptance", "-c", "-o", binary, packageName]);
      await run(binary, ["-test.run", `^(${testName})$`, "-test.v", "-test.timeout", timeout], { env: acceptanceEnv });
    }
    await run(process.execPath, [path.join(root, "scripts/check-native-runtime-security.mjs"), "--runtime-input-directory", args[1], "--installed-inventory", path.join(directory, "installed-inventory.json")], { cwd: root });
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

async function meetingAcceptance() {
  if (process.platform !== "darwin") throw new Error("The synthetic speech GUI fixture requires macOS.");
  const runtimeDirectory = productionRuntimeAcceptanceDirectory(await loadRuntimeInputManifest(root));
  const temporary = await mkdtemp(path.join(os.tmpdir(), "liberty-gui-"));
  try {
    const contents = path.join(temporary, "Liberty Meeting Acceptance.app", "Contents");
    const binary = path.join(contents, "MacOS", "liberty-native");
    await mkdir(path.dirname(binary), { recursive: true });
    await writeFile(path.join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.westng.liberty.meeting-acceptance</string><key>CFBundleName</key><string>Liberty Meeting Acceptance</string><key>CFBundleExecutable</key><string>liberty-native</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`);
    await run("go", ["build", "-o", binary, "./cmd/liberty-native"]);
    await run("go", ["test", "-tags", "meeting_acceptance", "./internal/host", "-run", "^TestMeetingWindowAcceptance$", "-count=1", "-timeout=40m", "-v"], {
      env: { ...env, LIBERTY_NATIVE_GUI_BINARY: binary, LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR: runtimeDirectory },
    });
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  switch (process.argv[2]) {
    case "check": await check(); break;
    case "compat": await compatibility(false); break;
    case "preview": await compatibility(true); break;
    case "launch": await launchNativeCandidate(); break;
    case "build-matrix": await buildMatrix(); break;
    case "bench-reads": await benchmarkReads(); break;
    case "runtime-acceptance": await runtimeAcceptance(); break;
    case "runtime-candidate-acceptance": await candidateRuntimeAcceptance(); break;
    case "meeting-acceptance": await meetingAcceptance(); break;
    case "remote-acceptance": await run("go", ["test", "-tags", "remote_acceptance", "./internal/infrastructure/remote", "-run", "^TestReadOnlyServiceAcceptance$", "-count=1", "-timeout=2m", "-v"]); break;
    case "credentials-acceptance": await run("go", ["test", "-tags", "credentials_acceptance", "./internal/infrastructure/credentials", "-count=1", "-timeout=60s", "-v"]); break;
    default: throw new Error("Expected check, compat, preview, launch, build-matrix or runtime-acceptance");
  }
}
