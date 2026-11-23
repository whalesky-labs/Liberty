import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { nativeTargets } from "./lib/native-package.mjs";
import { checkNativePlatformMatrix, validateNativePlatformMatrix } from "./check-native-platform-matrix.mjs";

async function fixture() {
  const read = (name) => readFile(new URL(`../${name}`, import.meta.url), "utf8");
  return {
    targets: structuredClone(nativeTargets),
    manifest: JSON.parse(await read("packages/desktop-resources/runtime/runtime-manifest.json")),
    module: await read("apps/native/go.mod"),
    workspace: JSON.parse(await read("package.json")),
  };
}

test("Native target policy validates all four shared targets without reading the retired UI", async () => {
  const matrix = await checkNativePlatformMatrix();
  assert.equal(matrix.length, 4);
  assert.deepEqual(matrix.find((entry) => entry.platformId === "windows-x86"), { platformId: "windows-x86", goos: "windows", goarch: "386" });
});

test("Native policy rejects missing, duplicated, remapped and unpinned platforms", async () => {
  const original = await fixture();
  for (const [name, change] of [
    ["missing target", (value) => { delete value.targets["windows-x86"]; }],
    ["wrong machine", (value) => { value.targets["windows-x64"].machine = 0x14c; }],
    ["wrong architecture", (value) => { value.targets["windows-x86"].arch = "amd64"; }],
    ["duplicate runtime platform", (value) => { value.manifest.platforms[1] = value.manifest.platforms[0]; }],
    ["missing hash", (value) => { value.manifest.platforms[0].pythonBundle.sha256 = ""; }],
    ["mutable URL", (value) => { value.manifest.platforms[0].pythonBundle.urls.official = "https://github.com/project/releases/latest/python.zip"; }],
    ["insecure URL", (value) => { value.manifest.platforms[0].ffmpegBundle.urls.official = "http://example.com/asset.zip"; }],
    ["escaping executable", (value) => { value.manifest.platforms[0].pythonExecutableCandidates = ["../python"]; }],
    ["x86 installation enabled", (value) => { value.manifest.platforms.find((p) => p.platformId === "windows-x86").unsupportedReason = ""; }],
    ["x86 candidate enabled", (value) => { value.manifest.candidateProfiles["security-candidate"].platforms[0].platformId = "windows-x86"; }],
    ["toolchain drift", (value) => { value.module = value.module.replace("toolchain go1.27.4", "toolchain go1.27.5"); }],
    ["package manager drift", (value) => { value.workspace.packageManager = "pnpm@latest"; }],
  ]) {
    const input = structuredClone(original);
    change(input);
    assert.throws(() => validateNativePlatformMatrix(input), undefined, name);
  }
});
