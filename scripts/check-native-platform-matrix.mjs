import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { nativeTargets } from "./lib/native-package.mjs";
import { runtimeInputPins } from "./lib/native-runtime-inputs.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const expectedTargets = {
  "darwin-aarch64": ["darwin", "arm64", 0x100000c],
  "darwin-x64": ["darwin", "amd64", 0x1000007],
  "windows-x64": ["windows", "amd64", 0x8664],
  "windows-x86": ["windows", "386", 0x14c],
};

function asset(value, label) {
  assert.ok(value && /^[a-f0-9]{64}$/i.test(value.sha256), `${label}: missing SHA-256`);
  assert.ok(typeof value.fileName === "string" && value.fileName && !/[/\\:\x00]/.test(value.fileName), `${label}: invalid filename`);
  assert.ok(value.urls && Object.keys(value.urls).length, `${label}: missing download sources`);
  for (const address of Object.values(value.urls)) {
    const url = new URL(address);
    assert.ok(url.protocol === "https:" && !url.username && !url.password
      && !/(?:^|\/)latest(?:\/|$)/i.test(url.pathname)
      && !/\.(?:invalid|test|example)$/.test(url.hostname), `${label}: mutable or invalid URL`);
  }
}

export function validateNativePlatformMatrix({ targets = nativeTargets, manifest, module, workspace }) {
  assert.deepEqual(Object.keys(targets).sort(), Object.keys(expectedTargets).sort(), "Native must retain all four target identities");
  for (const [id, [os, arch, machine]] of Object.entries(expectedTargets)) {
    assert.deepEqual(targets[id], { os, arch, machine }, `Native target drift: ${id}`);
  }
  assert.equal(workspace.packageManager, "pnpm@10.30.3", "Native tooling requires pinned pnpm");
  assert.match(module, /^toolchain go1\.27\.4\s*$/m, "Native Go toolchain must remain fixed");
  assert.match(module, /^go 1\.27\.3\s*$/m, "Native module language version drift");
  assert.ok(Array.isArray(manifest.platforms), "Missing shared runtime platforms");
  assert.deepEqual(manifest.platforms.map((p) => p.platformId).sort(), Object.keys(expectedTargets).sort(), "Shared runtime and Native targets differ");
  for (const platform of manifest.platforms) {
    asset(platform.pythonBundle, `${platform.platformId} Python`);
    asset(platform.ffmpegBundle, `${platform.platformId} FFmpeg`);
    for (const field of ["pythonExecutableCandidates", "ffmpegExecutableCandidates"]) {
      assert.ok(Array.isArray(platform[field]) && platform[field].length, `${platform.platformId}: missing executable paths`);
      for (const value of platform[field]) {
        assert.ok(typeof value === "string" && value.split("/").every((part) => part && part !== "." && part !== "..")
          && !/[\\:\x00]/.test(value), `${platform.platformId}: escaping executable path`);
      }
    }
    if (platform.platformId === "windows-x86") {
      assert.equal(platform.asrBackend, "sherpa-onnx", "Windows x86 backend must remain distinct");
      assert.ok(typeof platform.unsupportedReason === "string" && platform.unsupportedReason.trim(), "Windows x86 local installation remains blocked pending acceptance");
    } else {
      assert.equal(platform.asrBackend, "funasr", `${platform.platformId}: unexpected backend`);
    }
  }
  runtimeInputPins(manifest);
  const candidate = manifest.candidateProfiles["security-candidate"];
  assert.deepEqual(candidate.platforms.map((p) => p.platformId).sort(), Object.keys(expectedTargets).filter((id) => id !== "windows-x86").sort(), "Candidate runtime must not imply x86 support");
  for (const platform of candidate.platforms) asset(platform.pythonBundle, `${platform.platformId} candidate Python`);
  return Object.entries(targets).map(([platformId, { os, arch }]) => ({ platformId, goos: os, goarch: arch }));
}

export async function checkNativePlatformMatrix() {
  const [manifest, module, workspace] = await Promise.all([
    readFile(path.join(root, "packages/desktop-resources/runtime/runtime-manifest.json"), "utf8").then(JSON.parse),
    readFile(path.join(root, "apps/native/go.mod"), "utf8"),
    readFile(path.join(root, "package.json"), "utf8").then(JSON.parse),
  ]);
  return validateNativePlatformMatrix({ manifest, module, workspace });
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  assert.ok(process.argv.slice(2).every((arg) => arg === "--print-matrix") && process.argv.length <= 3, "Expected optional --print-matrix");
  const include = await checkNativePlatformMatrix();
  if (process.argv.includes("--print-matrix")) console.log(JSON.stringify({ include }));
  else console.log("Native four-target static policy passed. Windows x86 local runtime remains unsupported; target-device execution, signing and production acceptance are separate requirements.");
}
