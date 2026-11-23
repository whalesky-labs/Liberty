import { createHash } from "node:crypto";

export const candidateIdentity = "com.westng.liberty.native-candidate";
export const candidateName = "Liberty Native Candidate";
export const nativeTargets = Object.freeze({
  "darwin-aarch64": { os: "darwin", arch: "arm64", machine: 0x100000c },
  "darwin-x64": { os: "darwin", arch: "amd64", machine: 0x1000007 },
  "windows-x64": { os: "windows", arch: "amd64", machine: 0x8664 },
  "windows-x86": { os: "windows", arch: "386", machine: 0x14c },
});

export function packageTarget(args, platform = process.platform, arch = process.arch) {
  const fallback = Object.entries(nativeTargets).find(([, target]) => target.os === (platform === "win32" ? "windows" : platform) && target.arch === (arch === "x64" ? "amd64" : arch === "ia32" ? "386" : arch))?.[0];
  if (args.length === 0 && fallback) return fallback;
  if (args.length === 2 && args[0] === "--target" && args[1] === "darwin-arm64") return packageTarget([], "darwin", "arm64");
  if (args.length === 2 && args[0] === "--target" && Object.hasOwn(nativeTargets, args[1])) return args[1];
  throw new Error("Expected --target darwin-aarch64, darwin-x64, windows-x64 or windows-x86; only isolated candidate packages are supported");
}

export function assertRunnableTarget(targetID, platform = process.platform, arch = process.arch) {
  const hostTarget = packageTarget([], platform, arch);
  if (targetID !== hostTarget && !(hostTarget === "windows-x64" && targetID === "windows-x86")) {
    throw new Error("Candidate execution requires a matching host, or Windows x64 for the x86 candidate");
  }
}

export function assertNativeBinary(bytes, targetID) {
  const target = nativeTargets[targetID];
  if (!target || bytes.length < 64) throw new Error("Invalid native binary");
  if (target.os === "darwin") {
    if (bytes.readUInt32LE(0) !== 0xfeedfacf || bytes.readUInt32LE(4) !== target.machine) throw new Error("Unexpected Mach-O architecture");
  } else {
    const offset = bytes.readUInt32LE(0x3c);
    if (bytes.toString("ascii", 0, 2) !== "MZ" || offset < 64 || offset > bytes.length - 6 || bytes.readUInt32LE(offset) !== 0x4550 || bytes.readUInt16LE(offset + 4) !== target.machine) throw new Error("Unexpected PE architecture");
  }
}

export function assertNativeVersion(version, sources) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(version) || sources.some((source) => source !== version)) throw new Error("Native package version differs from the workspace");
}

export function candidatePlist(version) {
  assertNativeVersion(version, []);
  const bundleVersion = version.split(/[+-]/)[0];
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>${candidateIdentity}</string>
<key>CFBundleName</key><string>${candidateName}</string>
<key>CFBundleDisplayName</key><string>${candidateName}</string>
<key>CFBundleExecutable</key><string>liberty-native</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>${bundleVersion}</string>
<key>CFBundleVersion</key><string>${bundleVersion}</string>
<key>CFBundleGetInfoString</key><string>Liberty Native ${version} (candidate)</string>
<key>CFBundleIconFile</key><string>icon.icns</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
`;
}

export function fileDigest(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

export function assertCandidateManifest(manifest) {
  if (manifest.format !== 1 || manifest.channel !== "candidate" || manifest.identity !== candidateIdentity || manifest.productionReady !== false || manifest.signed !== false || manifest.notarized !== false || !Object.hasOwn(nativeTargets, manifest.target)) throw new Error("Invalid candidate package policy");
  assertNativeVersion(manifest.version, []);
  if (!Array.isArray(manifest.files) || manifest.files.length < 4) throw new Error("Incomplete candidate package");
  const seen = new Set();
  for (const file of manifest.files) {
    if (typeof file.path !== "string" || !file.path || file.path.includes("\\") || file.path.startsWith("/") || file.path.split("/").some((part) => !part || part === "." || part === ".." || part.includes(":")) || !/^[0-9a-f]{64}$/.test(file.sha256) || !Number.isSafeInteger(file.bytes) || file.bytes <= 0 || seen.has(file.path)) throw new Error("Invalid candidate file inventory");
    seen.add(file.path);
  }
  if (manifest.availableRuntimeProfiles !== undefined) {
    const profiles = JSON.stringify(manifest.availableRuntimeProfiles);
    if (manifest.defaultRuntimeProfile !== "production" || !['["production"]', '["production","security-candidate"]'].includes(profiles)) throw new Error("Invalid candidate runtime selection policy");
    if (manifest.availableRuntimeProfiles.length === 2) {
      if (manifest.target === "windows-x86" || !manifest.candidateRuntimeVersion || !Array.isArray(manifest.runtimeInputs) || manifest.runtimeInputs.length !== 8) throw new Error("Incomplete candidate runtime metadata");
      const runtimeNames = new Set();
      for (const input of manifest.runtimeInputs) {
        const matches = manifest.files.filter((file) => file.path.endsWith(`/runtime-inputs/${input.fileName}`) || file.path === `runtime-inputs/${input.fileName}`);
        if (typeof input.fileName !== "string" || !input.fileName || /[/\\:\x00]/.test(input.fileName) || input.fileName === "." || input.fileName === ".." || runtimeNames.has(input.fileName) || matches.length !== 1 || matches[0].sha256 !== input.sha256 || matches[0].bytes !== input.bytes) throw new Error("Candidate runtime inputs differ from packaged bytes");
        runtimeNames.add(input.fileName);
      }
    } else if (manifest.runtimeInputs !== undefined || manifest.candidateRuntimeVersion !== undefined) throw new Error("Unexpected candidate runtime metadata");
  } else if (manifest.defaultRuntimeProfile !== undefined || manifest.runtimeInputs !== undefined || manifest.candidateRuntimeVersion !== undefined) throw new Error("Incomplete candidate runtime selection policy");
}
