import assert from "node:assert/strict";
import test from "node:test";
import { assertCandidateManifest, assertNativeBinary, assertNativeVersion, assertRunnableTarget, candidateIdentity, candidatePlist, fileDigest, packageTarget } from "./lib/native-package.mjs";
import { productionRuntimeAcceptanceDirectory } from "./run-native.mjs";

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
