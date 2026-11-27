import { execFileSync, spawn } from "node:child_process";
import { copyFile, mkdir, mkdtemp, readFile, readdir, rename, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { assertCandidateManifest, assertNativeBinary, assertNativeVersion, assertRunnableTarget, candidateIdentity, candidateName, candidatePlist, fileDigest, nativeTargets } from "./lib/native-package.mjs";
import { loadRuntimeInputManifest, publishRuntimeInputs, readRuntimeInputs, runtimeInputPins, runtimePackageOptions, securityRuntimeProfile } from "./lib/native-runtime-inputs.mjs";
import { assertWindowsApplicationIcon, windowsIconObject } from "./lib/native-windows-resources.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const native = path.join(root, "apps/native");
const env = { ...process.env, GOTOOLCHAIN: "go1.27.4", GOWORK: "off", GOFLAGS: "-mod=readonly", CGO_ENABLED: "0" };

async function run(command, args, options = {}) {
  await new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd: root, env, stdio: "inherit", ...options });
    child.once("error", reject);
    child.once("exit", (code, signal) => code === 0 ? resolve() : reject(new Error(`Native packaging command failed (${signal ?? code})`)));
  });
}

async function inventory(directory, prefix = "") {
  const files = [];
  for (const entry of (await readdir(path.join(directory, prefix), { withFileTypes: true })).sort((left, right) => left.name.localeCompare(right.name))) {
    const name = path.posix.join(prefix, entry.name);
    if (entry.isDirectory()) files.push(...await inventory(directory, name));
    else if (entry.isFile()) {
      const bytes = await readFile(path.join(directory, name));
      files.push({ path: name, bytes: bytes.length, sha256: fileDigest(bytes) });
    } else throw new Error("Candidate package contains a non-regular file");
  }
  return files;
}

async function packageNative() {
  const { targetID, inputDirectory, verifyHost, runtimeProfile = securityRuntimeProfile } = runtimePackageOptions(process.argv.slice(2));
  const target = nativeTargets[targetID];
  if (verifyHost) assertRunnableTarget(targetID);
  const runtimeManifest = inputDirectory ? await loadRuntimeInputManifest(root) : undefined;
  const runtimePins = runtimeManifest ? runtimeInputPins(runtimeManifest, runtimeProfile) : undefined;
  const runtimeFiles = inputDirectory ? await readRuntimeInputs(inputDirectory, runtimePins) : undefined;
  if (inputDirectory) await run(process.execPath, [path.join(root, "scripts/check-native-runtime-security.mjs"), ...(runtimeProfile === securityRuntimeProfile ? [] : ["--profile", runtimeProfile]), "--runtime-input-directory", inputDirectory]);
  await run(process.execPath, [path.join(root, "scripts/check-version-consistency.mjs"), "--native"]);
  const workspace = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
  const host = await readFile(path.join(native, "internal/host/profile.go"), "utf8");
  const version = workspace.version;
  assertNativeVersion(version, [host.match(/^const Version = "([^"]+)"$/m)?.[1]]);
  const location = await readFile(path.join(native, "internal/infrastructure/profile/location.go"), "utf8");
  const candidateProfile = location.match(/^\s*CandidateID\s*=\s*"([^"]+)"/m)?.[1];
  const productionProfile = location.match(/^\s*ProductionID\s*=\s*"([^"]+)"/m)?.[1];
  if (candidateProfile !== candidateIdentity || !productionProfile || productionProfile === candidateProfile) throw new Error("Native profile identity drift");
  await run(process.execPath, [path.join(root, "scripts/collect-native-licenses.mjs")]);
  const output = path.join(root, "target/native/packages");
  await mkdir(output, { recursive: true });
  const temporary = await mkdtemp(path.join(output, ".staging-"));
  const name = `liberty-native-candidate-${version}-${targetID}${inputDirectory ? (runtimeProfile === securityRuntimeProfile ? "-security-runtime" : `-${runtimeProfile}`) : ""}`;
  const content = path.join(temporary, name);
  const app = target.os === "darwin" ? path.join(content, `${candidateName}.app/Contents`) : content;
  const binary = target.os === "darwin" ? path.join(app, "MacOS/liberty-native") : path.join(content, "Liberty Native Candidate.exe");
  const resources = target.os === "darwin" ? path.join(app, "Resources") : content;
  let published = false;
  let windowsSource;
  try {
    await mkdir(path.dirname(binary), { recursive: true });
    await mkdir(resources, { recursive: true });
    const flags = `-s -w -X main.launchChannel=candidate${target.os === "windows" ? " -H windowsgui" : ""}`;
    let mainPackage = "./cmd/liberty-native";
    const windowsIcon = target.os === "windows" ? await readFile(path.join(root, "packages/desktop-resources/presentation/icon.ico")) : undefined;
    if (windowsIcon) {
      // Go links .syso files only from the real main-package directory; its
      // overlay does not cover them. Stage an isolated main package so parallel
      // packaging never mutates or leaves generated objects in the source tree.
      const staging = path.join(native, ".mygo");
      await mkdir(staging, { recursive: true });
      windowsSource = await mkdtemp(path.join(staging, "windows-package-"));
      for (const file of await readdir(path.join(native, "cmd/liberty-native"))) {
        if (file.endsWith(".go") && !file.endsWith("_test.go")) await copyFile(path.join(native, "cmd/liberty-native", file), path.join(windowsSource, file));
      }
      await writeFile(path.join(windowsSource, "application_windows_amd64.syso"), windowsIconObject(windowsIcon));
      mainPackage = windowsSource;
    }
    await run("go", ["build", "-trimpath", `-ldflags=${flags}`, "-o", binary, mainPackage], { cwd: native, env: { ...env, GOOS: target.os, GOARCH: target.arch } });
    assertNativeBinary(await readFile(binary), targetID);
    if (windowsIcon) assertWindowsApplicationIcon(await readFile(binary), windowsIcon);
    if (target.os === "darwin") await writeFile(path.join(app, "Info.plist"), candidatePlist(version));
    const iconName = target.os === "darwin" ? "icon.icns" : "icon.ico";
    await copyFile(path.join(root, "packages/desktop-resources/presentation", iconName), path.join(resources, iconName));
    await copyFile(path.join(root, "LICENSE"), path.join(resources, "LICENSE"));
    await copyFile(path.join(root, "artifacts/sbom/native-go-NOTICES.txt"), path.join(resources, "THIRD_PARTY_NOTICES.txt"));
    await copyFile(path.join(root, "packages/desktop-resources/runtime/live-translation-NOTICES.txt"), path.join(resources, "LOCAL_TRANSLATION_NOTICES.txt"));
    if (runtimeFiles) await publishRuntimeInputs(path.join(resources, "runtime-inputs"), runtimeFiles, runtimePins);
    await writeFile(path.join(content, "README.txt"), `Liberty Native Candidate ${version} — ${targetID}

本包为未签名、未公证的原生迁移候选版，尚未通过生产切换验收。
This unsigned, unnotarized migration candidate has not passed production cutover acceptance.

双击应用启动。首次启动自动创建独立候选资料目录 ${candidateIdentity}，后续启动继续使用，不会清除。
Launch the app normally. It creates and reuses the separate ${candidateIdentity} profile.

现有生产资料目录 ${productionProfile} 默认不访问。不要将真实业务资料用于候选验收。
The existing production profile ${productionProfile} is not selected by default. Use synthetic acceptance data.

命令行可使用 --profile-directory <absolute-directory> --writable --prepare-database 指定隔离资料目录。
Use --profile-directory <absolute-directory> --writable --prepare-database for an explicit isolated profile.
显式 --database <absolute-file> 默认只读；--version 不访问资料目录。
An explicit --database <absolute-file> is read only by default; --version does not access profiles.

同一资料目录只允许一个当前分支客户端写入。旧发行版未实现共同资料锁，必须先退出。
Only one current-branch client may write a profile. Previously released clients do not share this lock and must be closed first.

Windows 包为便携目录，不是安装器；macOS 包不代表已完成签名、公证或升级回退验收。
Windows output is portable, not an installer. macOS output does not establish signing, notarization or upgrade/rollback acceptance.
${runtimeFiles ? `
本包额外携带经过源码清单固定摘要核验的 ${runtimeProfile} 运行时输入，默认仍选择 production 运行时。
This package also includes pinned ${runtimeProfile} runtime inputs; the default runtime remains production.
仅使用显式隔离目录加 --writable --prepare-database --runtime-profile ${runtimeProfile} 验收候选运行时。
Select it with an explicit isolated --profile-directory plus --writable --prepare-database --runtime-profile ${runtimeProfile}.
安装仍需清单中的网络下载源；随包的 wheel 只覆盖 FunASR，不是完整离线环境。
Installation still uses the configured network source; the bundled FunASR wheel is not a complete offline runtime.
` : ""}
`);
    const manifest = { format: 1, channel: "candidate", identity: candidateIdentity, version, target: targetID, productionReady: false, signed: false, notarized: false, defaultRuntimeProfile: "production", availableRuntimeProfiles: runtimeFiles ? ["production", runtimeProfile] : ["production"], ...(runtimeFiles ? { candidateRuntimeVersion: runtimeManifest.candidateProfiles[runtimeProfile].runtimeVersion, runtimeInputs: runtimePins } : {}), files: await inventory(content) };
    assertCandidateManifest(manifest);
    await writeFile(path.join(content, "candidate-manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
    const verified = JSON.parse(await readFile(path.join(content, "candidate-manifest.json"), "utf8"));
    assertCandidateManifest(verified);
    for (const file of verified.files) {
      if (fileDigest(await readFile(path.join(content, file.path))) !== file.sha256 || (await stat(path.join(content, file.path))).size !== file.bytes) throw new Error("Candidate file verification failed");
    }
    const archiveName = `${name}.tar.gz`;
    await run("tar", ["-czf", archiveName, name], { cwd: temporary });
    const archive = await readFile(path.join(temporary, archiveName));
    if (archive.length < 1024) throw new Error("Candidate archive is incomplete");
    const extracted = path.join(temporary, "verify");
    await mkdir(extracted);
    await run("tar", ["-xzf", archiveName, "-C", "verify"], { cwd: temporary });
    const extractedManifest = await readFile(path.join(extracted, name, "candidate-manifest.json"), "utf8");
    if (extractedManifest !== await readFile(path.join(content, "candidate-manifest.json"), "utf8")) throw new Error("Candidate archive manifest differs");
    for (const file of verified.files) {
      if (fileDigest(await readFile(path.join(extracted, name, file.path))) !== file.sha256) throw new Error("Candidate archive verification failed");
    }
    if (verifyHost) {
      const program = path.join(extracted, name, path.relative(content, binary));
      const reported = execFileSync(program, ["--version"], { cwd: extracted, encoding: "utf8", timeout: 15_000, windowsHide: true }).trim();
      if (reported !== `Liberty Native ${version} (candidate)`) throw new Error("Extracted program reported an unexpected candidate version");
      console.log(`Verified extracted ${targetID} program version and candidate channel.`);
    }
    await rm(extracted, { recursive: true });
    await writeFile(path.join(temporary, "SHA256SUMS"), `${fileDigest(archive)}  ${archiveName}\n`);
    const destination = path.join(output, `${name}-${path.basename(temporary).replace(".staging-", "")}`);
    await rename(temporary, destination);
    published = true;
    console.log(`Verified unsigned candidate package: ${destination}`);
  } finally {
    if (windowsSource) await rm(windowsSource, { recursive: true, force: true });
    if (!published) await rm(temporary, { recursive: true, force: true });
  }
}

await packageNative();
