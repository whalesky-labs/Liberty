import assert from "node:assert/strict";
import test from "node:test";
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { assertCandidateManifest, assertNativeBinary, assertNativeVersion, assertRunnableTarget, candidateIdentity, candidatePlist, fileDigest, packageTarget } from "./lib/native-package.mjs";
import { productionRuntimeAcceptanceDirectory } from "./run-native.mjs";
import { assertWindowsApplicationIcon, windowsApplicationPNG, windowsIconFrames, windowsIconObject } from "./lib/native-windows-resources.mjs";
import { windowsExecutableFixture, windowsIconFixture } from "./test-fixtures/native-windows.mjs";

test("Windows resources retain every shared icon size and reject malformed input", () => {
  assert.deepEqual(windowsIconFrames(windowsIconFixture).map(frame => frame.entry[0] || 256).sort((a, b) => a - b), [16, 24, 32, 48, 64, 256]);
  assert.equal(windowsApplicationPNG(windowsIconFixture).readUInt32BE(16), 256);
  for (const icon of [Buffer.alloc(0), Buffer.alloc(6), windowsIconFixture.subarray(0, 20), windowsIconFixture.subarray(0, -1)]) assert.throws(() => windowsIconObject(icon));
  const executable = windowsExecutableFixture();
  assertWindowsApplicationIcon(executable, windowsIconFixture);
  for (const mutate of [
    bytes => bytes.writeUInt32LE(0, 128 + 24 + 128),
    bytes => bytes.writeUInt32LE(0xffffffff, 128 + 24 + 128),
    bytes => bytes.writeUInt32LE(0xffffffff, 512 + 20),
    bytes => { bytes[bytes.length - 4] ^= 1; },
  ]) {
    const bytes = Buffer.from(executable);
    mutate(bytes);
    assert.throws(() => assertWindowsApplicationIcon(bytes, windowsIconFixture));
  }
});

test("Go's Windows linker embeds a readable application icon resource", { timeout: 120_000 }, async context => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "liberty-windows-icon-"));
  context.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(path.join(directory, "go.mod"), "module liberty-icon-fixture\n\ngo 1.27.3\n");
  await writeFile(path.join(directory, "main.go"), "package main\nfunc main() {}\n");
  await writeFile(path.join(directory, "application_windows_amd64.syso"), windowsIconObject(windowsIconFixture));
  const executable = path.join(directory, "fixture.exe");
  const environment = { ...process.env, GOTOOLCHAIN: "go1.27.4", GOFLAGS: "-mod=readonly", GOWORK: "off", CGO_ENABLED: "0" };
  execFileSync("go", ["build", "-trimpath", "-ldflags=-H windowsgui", "-o", executable, "."], { cwd: directory, env: { ...environment, GOOS: "windows", GOARCH: "amd64" }, timeout: 90_000, stdio: "pipe" });
  const bytes = await readFile(executable);
  assertNativeBinary(bytes, "windows-x64");
  assertWindowsApplicationIcon(bytes, windowsIconFixture);
  // An independent standard-library PE reader must also accept the linked
  // resource section. This is a cross-build check, not Windows execution.
  await writeFile(path.join(directory, "inspect.go"), 'package main\nimport ("debug/pe"; "os")\nfunc main(){f,e:=pe.Open(os.Args[1]);if e!=nil{panic(e)};defer f.Close();s:=f.Section(".rsrc");if s==nil{panic("missing .rsrc")};if _,e=s.Data();e!=nil{panic(e)}}\n');
  const { GOOS: _, GOARCH: __, ...hostEnvironment } = environment;
  execFileSync("go", ["run", "inspect.go", executable], { cwd: directory, env: hostEnvironment, timeout: 90_000, stdio: "pipe" });
});

test("package selection accepts three existing platform identities and rejects production overrides", () => {
  for (const target of ["darwin-aarch64", "darwin-x64", "windows-x64"]) assert.equal(packageTarget(["--target", target]), target);
  assert.equal(packageTarget(["--target", "darwin-arm64"]), packageTarget([], "darwin", "arm64"));
  assert.equal(packageTarget([], "darwin", "arm64"), "darwin-aarch64");
  assert.throws(() => packageTarget([], "win32", "ia32"));
  for (const args of [["--production"], ["--target", "linux-x64"], ["--target", "windows-x64", "--production"]]) assert.throws(() => packageTarget(args));
  assert.throws(() => packageTarget([], "linux", "x64"));
});

test("native package validates Mach-O and PE architecture", () => {
  const mach = Buffer.alloc(128);
  mach.writeUInt32LE(0xfeedfacf);
  mach.writeUInt32LE(0x100000c, 4);
  assertNativeBinary(mach, "darwin-aarch64");
  assert.throws(() => assertNativeBinary(mach, "darwin-x64"));
  const pe = Buffer.alloc(256);
  pe.write("MZ");
  pe.writeUInt32LE(128, 0x3c);
  pe.writeUInt32LE(0x4550, 128);
  pe.writeUInt16LE(0x8664, 132);
  assertNativeBinary(pe, "windows-x64");
  pe.writeUInt16LE(0x14c, 132);
  assert.throws(() => assertNativeBinary(pe, "windows-x64"));
  pe.writeUInt32LE(0xffffffff, 0x3c);
  assert.throws(() => assertNativeBinary(pe, "windows-x64"), /PE architecture/);
  assert.throws(() => assertNativeBinary(Buffer.alloc(2), "darwin-aarch64"));
});

test("execution verification requires a matching supported host", () => {
  assertRunnableTarget("darwin-aarch64", "darwin", "arm64");
  assertRunnableTarget("darwin-x64", "darwin", "x64");
  assertRunnableTarget("windows-x64", "win32", "x64");
  assert.throws(() => assertRunnableTarget("darwin-x64", "darwin", "arm64"), /matching host/);
  assert.throws(() => assertRunnableTarget("windows-x64", "win32", "ia32"), /Expected --target/);
  assert.throws(() => assertRunnableTarget("windows-x64", "darwin", "x64"), /matching host/);
});

test("runtime and meeting acceptance cannot reuse a different released generation", () => {
  const manifest = { runtimeVersion: "runtime-1", modelSetVersion: "models-1", modelProfile: "profile-1" };
  const current = productionRuntimeAcceptanceDirectory(manifest, "darwin", "arm64");
  assert.equal(productionRuntimeAcceptanceDirectory({ ...manifest }, "darwin", "arm64"), current);
  for (const field of Object.keys(manifest)) {
    assert.notEqual(productionRuntimeAcceptanceDirectory({ ...manifest, [field]: `${manifest[field]}-next` }, "darwin", "arm64"), current);
  }
  assert.notEqual(productionRuntimeAcceptanceDirectory(manifest, "darwin", "x64"), current);
  assert.throws(() => productionRuntimeAcceptanceDirectory({ ...manifest, runtimeVersion: "" }, "darwin", "arm64"), /Missing runtime or model identity/);
});

test("candidate bundle version and identity cannot silently drift", () => {
  assertNativeVersion("1.1.24", ["1.1.24", "1.1.24"]);
  assert.throws(() => assertNativeVersion("1.1.24", ["1.1.23"]));
  assert.throws(() => candidatePlist("<invalid>"));
  const plist = candidatePlist("1.1.24");
  assert.ok(plist.includes(`<string>${candidateIdentity}</string>`));
  assert.ok(!plist.includes("<string>com.westng.liberty</string>"));
  assert.ok(plist.includes("<key>CFBundleIconFile</key>"));
  const prerelease = candidatePlist("1.1.24-beta.1+build.2");
  assert.ok(prerelease.includes("<key>CFBundleVersion</key><string>1.1.24</string>"));
  assert.ok(prerelease.includes("1.1.24-beta.1+build.2 (candidate)"));
});

test("candidate manifests reject production claims, traversal, duplicates and invalid checksums", () => {
  const files = ["app", "icon", "LICENSE", "NOTICES"].map((name) => ({ path: `bundle/${name}`, bytes: 4, sha256: fileDigest(Buffer.from("test")) }));
  const manifest = { format: 1, channel: "candidate", identity: candidateIdentity, version: "1.1.24", target: "windows-x64", productionReady: false, signed: false, notarized: false, files };
  assertCandidateManifest(manifest);
  for (const change of [{ productionReady: true }, { signed: true }, { notarized: true }, { identity: "com.westng.liberty" }, { channel: "production" }, { target: "unknown" }, { files: [] }]) assert.throws(() => assertCandidateManifest({ ...manifest, ...change }));
  for (const name of ["../outside", "/absolute", "C:/absolute", "bundle\\file", "bundle/./file", "bundle//file"]) assert.throws(() => assertCandidateManifest({ ...manifest, files: [...files, { ...files[0], path: name }] }));
  assert.throws(() => assertCandidateManifest({ ...manifest, files: [...files, files[0]] }));
  assert.throws(() => assertCandidateManifest({ ...manifest, files: files.map((file) => ({ ...file, sha256: "invalid" })) }));
});
