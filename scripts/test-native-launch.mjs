import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, realpath, rm, symlink, unlink, utimes, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { candidateIdentity, candidateName, fileDigest } from "./lib/native-package.mjs";
import { activateWindowsNativeCandidate, findLatestNativeCandidate, launchNativeCandidate, nativeLaunchSpec, nativeLaunchTarget, runningNativeCandidatePaths, windowsNativeActivationSpec } from "./run-native.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const macTarget = nativeLaunchTarget("darwin", "arm64");
const macExecutable = `${candidateName}.app/Contents/MacOS/liberty-native`;

function manifest(target, files, version = "1.1.24") {
  return {
    format: 1,
    channel: "candidate",
    identity: candidateIdentity,
    version,
    target,
    productionReady: false,
    signed: false,
    notarized: false,
    files,
  };
}

test("uses Native for startup without old-client compatibility aliases", async () => {
  const packageJSON = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
  const nativeLauncher = await readFile(path.join(root, "scripts/run-native.mjs"), "utf8");
  assert.equal(packageJSON.scripts["native:launch"], "node scripts/run-native.mjs launch");
  assert.equal(packageJSON.scripts["native:start"], "pnpm native:launch");
  assert.equal(packageJSON.scripts.start, "pnpm native:launch");
  assert.equal(packageJSON.scripts["native:preview"], "node scripts/run-native.mjs preview");
  for (const entrypoint of ["start", "native:start", "native:launch"]) {
    assert.equal(resolveRootScript(packageJSON.scripts, entrypoint), "node scripts/run-native.mjs launch");
  }
  assert.equal(packageJSON.scripts["desktop:dev"], undefined);
  assert.equal(packageJSON.scripts["desktop:dev:web"], undefined);
  assert.equal(packageJSON.scripts["desktop:tauri"], undefined);
  for (const [name, command] of Object.entries(packageJSON.scripts)) {
    assert.doesNotMatch(name, /^desktop:compat:/, `${name} retains a compatibility alias`);
    assert.doesNotMatch(command, /--dir apps\/desktop (?:dev|dev:web|tauri)(?:\s|$)/, `${name} launches the old UI`);
  }
  assert.doesNotMatch(nativeLauncher, /apps\/desktop|run-tauri\.mjs|tauri dev/);
});

function resolveRootScript(scripts, entrypoint, visited = new Set()) {
  assert.ok(!visited.has(entrypoint), `cyclic root script alias: ${entrypoint}`);
  visited.add(entrypoint);
  const command = scripts[entrypoint];
  assert.equal(typeof command, "string", `missing root script: ${entrypoint}`);
  const alias = command.match(/^pnpm ([\w:-]+)$/);
  return alias ? resolveRootScript(scripts, alias[1], visited) : command;
}

async function createCandidate(root, name, target, executablePath, bytes, version = "1.1.24", nested = false) {
  const packageRoot = path.join(root, name);
  const directory = nested ? path.join(packageRoot, `${name}-content`) : packageRoot;
  await mkdir(path.dirname(path.join(directory, executablePath)), { recursive: true });
  await writeFile(path.join(directory, executablePath), bytes);
  await writeFile(path.join(directory, "LICENSE"), "license\n");
  await writeFile(path.join(directory, "README.txt"), "candidate\n");
  await writeFile(path.join(directory, "THIRD_PARTY_NOTICES.txt"), "notices\n");
  const files = [executablePath, "LICENSE", "README.txt", "THIRD_PARTY_NOTICES.txt"].map((file) => ({
    path: file,
    bytes: Buffer.byteLength(file === executablePath ? bytes : file === "LICENSE" ? "license\n" : file === "README.txt" ? "candidate\n" : "notices\n"),
    sha256: fileDigest(file === executablePath ? bytes : file === "LICENSE" ? "license\n" : file === "README.txt" ? "candidate\n" : "notices\n"),
  }));
  await writeFile(path.join(directory, "candidate-manifest.json"), JSON.stringify(manifest(target, files, version)));
  return { packageRoot, content: directory };
}

test("maps supported hosts to the exact candidate target", () => {
  assert.equal(nativeLaunchTarget("darwin", "arm64"), "darwin-aarch64");
  assert.equal(nativeLaunchTarget("darwin", "x64"), "darwin-x64");
  assert.equal(nativeLaunchTarget("win32", "x64"), "windows-x64");
  assert.equal(nativeLaunchTarget("win32", "ia32"), "windows-x86");
  assert.throws(() => nativeLaunchTarget("linux", "x64"), /unsupported/);
});

test("builds platform-specific launch specifications without resolving generic app names", () => {
  const packageDirectory = "/tmp/liberty-native-package";
  assert.deepEqual(nativeLaunchSpec("darwin", packageDirectory, "darwin-aarch64"), {
    command: "open",
    args: [path.join(packageDirectory, `${candidateName}.app`)],
  });
  assert.deepEqual(nativeLaunchSpec("win32", packageDirectory, "windows-x64"), {
    command: path.join(packageDirectory, `${candidateName}.exe`),
    args: [],
    detached: true,
  });
  assert.throws(() => nativeLaunchSpec("darwin", packageDirectory, "windows-x64"), /does not match/);
});

async function launchFixture(context) {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const calls = [];
  const activations = [];
  const messages = [];
  return {
    directory,
    calls,
    activations,
    messages,
    launch: (runningPaths, platform = "darwin", arch = "arm64", inspect = async () => runningPaths) => launchNativeCandidate({
      platform,
      arch,
      packagesDirectory: directory,
      inspect,
      execute: async (spec, cwd) => calls.push({ spec, cwd }),
      activateWindows: async (executable) => { activations.push(executable); return { processId: 123 }; },
      log: (message) => messages.push(message),
    }),
  };
}

test("activates the exact running macOS candidate even after a newer package is built", async (context) => {
  const { directory, calls, messages, launch } = await launchFixture(context);
  const running = await createCandidate(directory, "liberty-native-candidate-running", macTarget, macExecutable, "running", "1.1.24", true);
  const newer = await createCandidate(directory, "liberty-native-candidate-newer", macTarget, macExecutable, "newer", "1.1.25", true);
  const runningContent = await realpath(running.content);
  const runningPath = path.join(runningContent, macExecutable);

  await launch([runningPath]);
  assert.deepEqual(calls, [{ spec: { command: "open", args: [path.join(runningContent, `${candidateName}.app`)] }, cwd: runningContent }]);
  assert.match(messages[0], /Activating verified native candidate/);
  assert.match(messages[1], /save your work and quit the running candidate normally.*pnpm native:launch/);

  await launch([]);
  assert.deepEqual(calls[1], { spec: nativeLaunchSpec("darwin", newer.content, macTarget), cwd: newer.content });
});

test("reuses a verified running package without relying on another package's integrity", async (context) => {
  const { directory, calls, launch } = await launchFixture(context);
  const running = await createCandidate(directory, "liberty-native-candidate-running", macTarget, macExecutable, "running");
  const newer = await createCandidate(directory, "liberty-native-candidate-newer", macTarget, macExecutable, "newer", "1.1.25");
  await writeFile(path.join(newer.content, "LICENSE"), "tampered");

  await launch([path.join(running.content, macExecutable)]);
  assert.equal(calls[0].cwd, await realpath(running.content));
  await assert.rejects(() => launch([]), /integrity verification/);
  assert.equal(calls.length, 1);
});

test("activates the exact running Windows main window without spawning a newer package", async (context) => {
  const { directory, calls, activations, messages, launch } = await launchFixture(context);
  const executable = `${candidateName}.exe`;
  const running = await createCandidate(directory, "liberty-native-candidate-running", "windows-x64", executable, "running");
  await createCandidate(directory, "liberty-native-candidate-newer", "windows-x64", executable, "newer", "1.1.25");

  await launch([path.join(running.content, executable)], "win32", "x64");
  assert.deepEqual(calls, []);
  assert.deepEqual(activations, [path.join(await realpath(running.content), executable)]);
  assert.match(messages[0], /process 123, foreground confirmed.*No duplicate process/);
  assert.match(messages[1], /quit the running candidate normally/);
});

test("Windows activation requires positive window state evidence beyond a successful command exit", async () => {
  const executable = `C:\\packages\\O'Brien & $value\\${candidateName}.exe`;
  const result = { status: "activated", processId: 123, windowHandle: "456", foreground: true, minimized: false };
  const spec = windowsNativeActivationSpec(executable);
  const script = Buffer.from(spec.args.at(-1), "base64").toString("utf16le");
  assert.ok(!script.includes(executable));
  assert.ok(script.includes(Buffer.from(executable).toString("base64")));
  assert.match(script, /ExecutablePath -ine \$expected/);
  assert.match(script, /CreationDate -ne \$created/);
  assert.match(script, /owner == process/);
  assert.match(script, /title\.ToString\(\) == expectedTitle/);
  assert.match(script, /GetForegroundWindow\(\) -eq \$window/);
  assert.doesNotMatch(script, /SendKeys|AppActivate|Start-Process/);
  assert.deepEqual(await activateWindowsNativeCandidate(executable, async (command, args, options) => {
    assert.equal(command, "powershell.exe");
    assert.deepEqual(args, spec.args);
    assert.equal(options.windowsHide, true);
    return { stdout: JSON.stringify(result) };
  }), result);
  for (const evidence of ["", "{}", JSON.stringify({ ...result, foreground: false }), JSON.stringify({ ...result, minimized: true }), JSON.stringify({ ...result, processId: 0 }), JSON.stringify({ ...result, windowHandle: "0" })]) {
    await assert.rejects(activateWindowsNativeCandidate(executable, async () => ({ stdout: evidence })), /Could not activate.*No duplicate process/);
  }
});

test("failed Windows activation never falls back to spawning another candidate", async context => {
  const { directory, calls } = await launchFixture(context);
  const executable = `${candidateName}.exe`;
  const running = await createCandidate(directory, "liberty-native-candidate-running", "windows-x64", executable, "running");
  let attempts = 0;
  await assert.rejects(launchNativeCandidate({
    platform: "win32", arch: "x64", packagesDirectory: directory,
    inspect: async () => [path.join(running.content, executable)],
    execute: async (...args) => calls.push(args),
    activateWindows: async () => { attempts++; throw new Error("foreground permission denied"); },
    log: () => {},
  }), /foreground permission denied/);
  assert.equal(attempts, 1);
  assert.deepEqual(calls, []);
});

test("does not pick an instance when multiple native processes share one executable", async (context) => {
  const { directory, calls, launch } = await launchFixture(context);
  const running = await createCandidate(directory, "liberty-native-candidate-running", macTarget, macExecutable, "running");
  const binary = path.join(running.content, macExecutable);

  await assert.rejects(() => launch([binary, binary]), /Multiple Liberty native instances.*Close them normally/);
  assert.deepEqual(calls, []);
});

test("does not launch the latest package when the native process query fails", async (context) => {
  const { directory, calls, launch } = await launchFixture(context);
  await createCandidate(directory, "liberty-native-candidate-newer", macTarget, macExecutable, "newer", "1.1.25");
  const inspect = (platform) => runningNativeCandidatePaths(platform, async () => { throw new Error("process query unavailable"); });

  await assert.rejects(() => launch(undefined, "darwin", "arm64", inspect), /Could not inspect running Liberty native processes safely/);
  assert.deepEqual(calls, []);
});

test("does not fall back to the latest package when the running process cannot be verified", async (context) => {
  for (const [name, change, cause] of [
    ["preview bundle", async (candidate) => path.join(candidate.content, "Liberty Native Preview.app", "Contents", "MacOS", "liberty-native"), /exact package executable/],
    ["outside packages", async (candidate) => path.join(candidate.packageRoot, "..", "..", macExecutable), /outside the native candidate packages/],
    ["relative executable", async () => macExecutable, /Invalid running native executable path/],
    ["missing package", async (candidate) => path.join(candidate.packageRoot, "..", "liberty-native-candidate-missing", macExecutable), /ENOENT/],
    ["wrong architecture", async (candidate, binary) => {
      const file = path.join(candidate.content, "candidate-manifest.json");
      const previous = JSON.parse(await readFile(file, "utf8"));
      await writeFile(file, JSON.stringify({ ...previous, target: "darwin-x64" }));
      return binary;
    }, /target does not match this host/],
    ["preview channel", async (candidate, binary) => {
      const file = path.join(candidate.content, "candidate-manifest.json");
      const previous = JSON.parse(await readFile(file, "utf8"));
      await writeFile(file, JSON.stringify({ ...previous, channel: "preview" }));
      return binary;
    }, /Invalid candidate package policy/],
    ["tampered package file", async (candidate, binary) => {
      await writeFile(path.join(candidate.content, "LICENSE"), "tampered");
      return binary;
    }, /integrity verification/],
    ["symlinked package directory", async (candidate) => {
      const linked = path.join(candidate.packageRoot, "..", "liberty-native-candidate-linked");
      await symlink(candidate.packageRoot, linked, "junction");
      return path.join(linked, macExecutable);
    }, /not a regular directory/],
  ]) {
    await context.test(name, async (scenario) => {
      const { directory, calls, launch } = await launchFixture(scenario);
      const candidate = await createCandidate(directory, "liberty-native-candidate-running", macTarget, macExecutable, "running");
      await createCandidate(directory, "liberty-native-candidate-newer", macTarget, macExecutable, "newer", "1.1.25");
      const binary = await change(candidate, path.join(candidate.content, macExecutable));

      await assert.rejects(() => launch([binary]), (error) => {
        assert.match(error.message, /Could not verify the running Liberty native candidate.*Close it normally/);
        assert.match(error.cause?.message ?? "", cause);
        return true;
      });
      assert.deepEqual(calls, []);
    });
  }
});

test("macOS process inspection fails if any matched PID has no verifiable executable", async () => {
  const noProcesses = Object.assign(new Error("no matches"), { code: 1 });
  assert.deepEqual(await runningNativeCandidatePaths("darwin", async () => { throw noProcesses; }), []);

  const executable = `/packages/${macExecutable}`;
  const inspect = async (command, args) => {
    if (command === "pgrep") return { stdout: "123\n456\n" };
    assert.equal(command, "lsof");
    assert.ok(args.includes("-F0fn"));
    const processID = args[args.indexOf("-p") + 1];
    return { stdout: `p${processID}\0\nftxt\0n${processID === "123" ? executable : "/unidentified"}\0\n` };
  };
  await assert.rejects(() => runningNativeCandidatePaths("darwin", inspect), /Could not verify a running Liberty native executable/);
  assert.deepEqual(await runningNativeCandidatePaths("darwin", async (command, args) => command === "pgrep" ? { stdout: "123\n" } : inspect(command, args)), [executable]);
});

test("Windows process inspection includes preview processes and rejects unavailable paths", async () => {
  const preview = "C:\\preview\\liberty-native.exe";
  const candidate = `C:\\packages\\${candidateName}.exe`;
  const inspect = (processes) => async (command, args) => {
    assert.equal(command, "powershell.exe");
    assert.match(args.at(-1), /'liberty-native\.exe'/);
    return { stdout: JSON.stringify(processes) };
  };
  assert.deepEqual(await runningNativeCandidatePaths("win32", inspect([])), []);
  assert.deepEqual(await runningNativeCandidatePaths("win32", inspect([{ ExecutablePath: preview }])), [preview]);
  await assert.rejects(
    () => runningNativeCandidatePaths("win32", inspect([{ ExecutablePath: candidate }, { ExecutablePath: null }])),
    /Could not inspect running Liberty native processes safely/,
  );
});

test("selects the highest intact candidate version for the current target", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
    const older = await createCandidate(directory, "liberty-native-candidate-1.1.23-darwin-aarch64", "darwin-aarch64", executable, Buffer.from("older"), "1.1.23");
    const newer = await createCandidate(directory, "liberty-native-candidate-1.1.24-darwin-aarch64", "darwin-aarch64", executable, Buffer.from("newer"), "1.1.24", true);
    await utimes(older.packageRoot, new Date(1000), new Date(1000));
    await utimes(newer.packageRoot, new Date(2000), new Date(2000));
    assert.equal(await findLatestNativeCandidate(directory, "darwin-aarch64"), newer.content);
    assert.equal(await findLatestNativeCandidate(directory, "windows-x64"), null);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("does not confuse package rename timestamps with candidate version order", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
    const previous = await createCandidate(directory, "liberty-native-candidate-1.1.24-darwin-aarch64", "darwin-aarch64", executable, Buffer.from("previous"), "1.1.24", true);
    const current = await createCandidate(directory, "liberty-native-candidate-1.1.25-darwin-aarch64", "darwin-aarch64", executable, Buffer.from("current"), "1.1.25", true);
    await utimes(previous.packageRoot, new Date(3000), new Date(3000));
    await utimes(current.packageRoot, new Date(1000), new Date(1000));
    assert.equal(await findLatestNativeCandidate(directory, "darwin-aarch64"), current.content);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("compares candidate versions with build metadata by SemVer precedence", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
    const older = await createCandidate(directory, "liberty-native-candidate-1.1.24-build", "darwin-aarch64", executable, Buffer.from("older"), "1.1.24+build.9", true);
    const newer = await createCandidate(directory, "liberty-native-candidate-1.1.25-build", "darwin-aarch64", executable, Buffer.from("newer"), "1.1.25+build.1", true);
    await utimes(older.packageRoot, new Date(3000), new Date(3000));
    await utimes(newer.packageRoot, new Date(1000), new Date(1000));
    assert.equal(await findLatestNativeCandidate(directory, "darwin-aarch64"), newer.content);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("compares large numeric SemVer identifiers without floating-point rounding", async () => {
  for (const [lower, higher] of [
    ["1.1.24-rc.9007199254740992", "1.1.24-rc.9007199254740993"],
    ["1.9007199254740992.0", "1.9007199254740993.0"],
  ]) {
    const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
    try {
      const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
      const older = await createCandidate(directory, `liberty-native-candidate-${lower}`, "darwin-aarch64", executable, Buffer.from("older"), lower, true);
      const newer = await createCandidate(directory, `liberty-native-candidate-${higher}`, "darwin-aarch64", executable, Buffer.from("newer"), higher, true);
      await utimes(older.packageRoot, new Date(3000), new Date(3000));
      await utimes(newer.packageRoot, new Date(1000), new Date(1000));
      assert.equal(await findLatestNativeCandidate(directory, "darwin-aarch64"), newer.content);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }
});

test("rejects a tampered executable before launch", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
    const candidate = await createCandidate(directory, "liberty-native-candidate-tampered", "darwin-aarch64", executable, Buffer.from("original"));
    await writeFile(path.join(candidate.content, executable), "tampered");
    await assert.rejects(() => findLatestNativeCandidate(directory, "darwin-aarch64"), /integrity verification/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("ignores an intact-manifest package for another platform when its files are corrupt", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.exe";
    const unrelated = await createCandidate(directory, "liberty-native-candidate-windows", "windows-x64", executable, Buffer.from("original"));
    await writeFile(path.join(unrelated.content, executable), "tampered");
    assert.equal(await findLatestNativeCandidate(directory, "darwin-aarch64"), null);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("rejects package symlinks even when their target matches the manifest", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
    const bytes = Buffer.from("matching digest outside the package");
    const candidate = await createCandidate(directory, "liberty-native-candidate-linked", "darwin-aarch64", executable, bytes);
    const externalFile = path.join(directory, "external-binary");
    await writeFile(externalFile, bytes);
    await unlink(path.join(candidate.content, executable));
    await symlink(externalFile, path.join(candidate.content, executable));
    await assert.rejects(() => findLatestNativeCandidate(directory, "darwin-aarch64"), /non-regular package path/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("rejects a symlinked candidate manifest", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-native-launch-"));
  try {
    const executable = "Liberty Native Candidate.app/Contents/MacOS/liberty-native";
    const candidate = await createCandidate(directory, "liberty-native-candidate-linked-manifest", "darwin-aarch64", executable, Buffer.from("candidate"));
    const externalManifest = path.join(directory, "external-manifest.json");
    await writeFile(externalManifest, await readFile(path.join(candidate.content, "candidate-manifest.json")));
    await unlink(path.join(candidate.content, "candidate-manifest.json"));
    await symlink(externalManifest, path.join(candidate.content, "candidate-manifest.json"));
    await assert.rejects(() => findLatestNativeCandidate(directory, "darwin-aarch64"), /Invalid native candidate package/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
