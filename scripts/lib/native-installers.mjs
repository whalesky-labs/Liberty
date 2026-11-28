import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, readlink, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { assertNativeCandidatePayload, readNativeArchive } from '../check-native-release-policy.mjs';
import { assertMsiMetadata, compileMsi, installerProductVersion, msiQueryScript, renderWix } from './native-distribution.mjs';
import { assertNativeBinary, assertNativeVersion, assertRunnableTarget, candidateIdentity, candidateName, fileDigest, nativeTargets } from './native-package.mjs';

export const updatesURL = 'https://github.com/whalesky-labs/Liberty/releases';
const describe = (name, bytes) => ({ name, bytes: bytes.length, sha256: fileDigest(bytes) });
const json = value => Buffer.from(JSON.stringify(value, null, 2) + '\n');
const sameNames = (a, b) => assert.deepEqual([...a].sort(), [...b].sort(), 'Installer file set differs');
const ps = value => `'${String(value).replaceAll("'", "''")}'`;
const run = (command, args, options = {}) => {
  try { return execFileSync(command, args, { encoding: 'utf8', timeout: 120_000, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], ...options }).trim(); }
  catch { throw new Error(`Native installer tool failed: ${path.basename(command)} (output withheld)`); }
};
const powershell = source => {
  if (process.platform !== 'win32' || !process.env.SystemRoot) throw new Error('Windows installer verification requires Windows');
  return run(path.join(process.env.SystemRoot, 'System32/WindowsPowerShell/v1.0/powershell.exe'), ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(`$ErrorActionPreference='Stop'; ${source}`, 'utf16le').toString('base64')]);
};

export { installerProductVersion } from './native-distribution.mjs';

export function installerName(version, target) {
  assertNativeVersion(version, []);
  if (!Object.hasOwn(nativeTargets, target)) throw new Error('Unknown installer target');
  return `liberty-native-candidate-${version}-${target}.${target.startsWith('darwin-') ? 'dmg' : 'msi'}`;
}

export function installerManifest({ version, target, sourceCommit, archive, installer }) {
  return {
    format: 1, channel: 'candidate', identity: candidateIdentity, productionReady: false,
    sourceCommit, version, target, signed: false, notarized: false,
    signature: target.startsWith('darwin-') ? 'adhoc' : 'none', updatesURL,
    productVersion: target.startsWith('windows-') ? installerProductVersion(version) : null,
    inputArchiveSha256: fileDigest(archive), installer: describe(installerName(version, target), installer),
  };
}

export function assertInstallerManifest(manifest, context) {
  assert.match(context.sourceCommit ?? '', /^[a-f0-9]{40}$/);
  const expected = installerManifest(context);
  assert.deepEqual(manifest, expected, 'Installer provenance, signature claim or content differs');
  const bytes = context.installer;
  if (context.target.startsWith('darwin-')) {
    assert.ok(bytes.length >= 512 && bytes.subarray(-512, -508).toString() === 'koly', 'Invalid DMG trailer');
  } else assert.ok(bytes.length >= 512 && bytes.subarray(0, 8).equals(Buffer.from('d0cf11e0a1b11ae1', 'hex')), 'Invalid MSI container');
  return manifest;
}

async function regularFiles(directory, prefix = '') {
  const result = new Map();
  for (const entry of await readdir(path.join(directory, prefix), { withFileTypes: true })) {
    const name = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) for (const [key, value] of await regularFiles(directory, name)) result.set(key, value);
    else if (entry.isFile()) result.set(name, await readFile(path.join(directory, name)));
    else throw new Error('Installer payload contains a link or special file');
  }
  return result;
}
function compareFiles(actual, expected) {
  sameNames(actual.keys(), expected.keys());
  for (const [name, bytes] of expected) assert.ok(bytes.equals(actual.get(name)), `Installed payload differs: ${name}`);
}

// This function also supports local verification without asserting that a dirty
// working tree is a published source commit. Only buildInstallerArtifacts emits
// source-bound Release manifests, after checking the verified CI input contract.
export async function createInstaller({ files, version, target, provenance, output }) {
  assertRunnableTarget(target);
  const { resources } = assertNativeCandidatePayload({ files, version, target });
  const stage = await mkdtemp(path.join(os.tmpdir(), 'liberty-installer-'));
  const payload = path.join(stage, 'payload');
  const mac = target.startsWith('darwin-');
  const file = installerName(version, target);
  try {
    await mkdir(payload);
    for (const [name, bytes] of files) {
      if (['README.txt', 'candidate-manifest.json'].includes(name)) continue;
      await mkdir(path.dirname(path.join(payload, name)), { recursive: true });
      await writeFile(path.join(payload, name), bytes, { flag: 'wx' });
    }
    await writeFile(path.join(payload, `${resources}native-distribution-provenance.json`), json(provenance));
    await writeFile(path.join(payload, 'README.txt'), `Liberty Native Candidate ${version}\n\n${mac ? '将应用拖入 Applications，正常退出旧候选后打开。应用使用临时签名，未经过 Apple 公证。\nDrag the app into Applications; quit the previous candidate before opening. Ad-hoc signed, not notarized.' : '运行 MSI 安装到当前用户。没有 Windows 可信发布者签名，系统可能显示安全提示。\nRun the MSI to install for the current user. No trusted Windows publisher signature; security prompts may appear.'}\n\n更新下载 / Updates: ${updatesURL}\n升级前正常退出应用；卸载保留会议、模型、凭据和资料目录。\nQuit before upgrading. Uninstall preserves meetings, models, credentials and profile directories.\n本地转写首次使用仍需联网安装运行时和模型。\nLocal transcription still requires initial runtime and model downloads.\n`);
    const executable = path.join(payload, mac ? `${candidateName}.app/Contents/MacOS/liberty-native` : `${candidateName}.exe`);
    if (mac) {
      await chmod(executable, 0o755);
      run('/usr/bin/codesign', ['--force', '--sign', '-', path.join(payload, `${candidateName}.app`)]);
      run('/usr/bin/codesign', ['--verify', '--strict', '--deep', path.join(payload, `${candidateName}.app`)]);
      const expected = await regularFiles(payload);
      await symlink('/Applications', path.join(payload, 'Applications'));
      run('/usr/bin/hdiutil', ['create', '-format', 'UDZO', '-volname', `${candidateName} ${version}`, '-srcfolder', payload, path.join(stage, file)]);
      run('/usr/bin/hdiutil', ['verify', path.join(stage, file)]);
      const mount = path.join(stage, 'mounted');
      await mkdir(mount);
      let mounted = false;
      try {
        run('/usr/bin/hdiutil', ['attach', '-readonly', '-nobrowse', '-mountpoint', mount, path.join(stage, file)]);
        mounted = true;
        sameNames(await readdir(mount), [`${candidateName}.app`, 'Applications', 'README.txt']);
        assert.ok((await lstat(path.join(mount, 'Applications'))).isSymbolicLink());
        assert.equal(await readlink(path.join(mount, 'Applications')), '/Applications');
        const actual = await regularFiles(path.join(mount, `${candidateName}.app`));
        compareFiles(actual, new Map([...expected].filter(([name]) => name.startsWith(`${candidateName}.app/`)).map(([name, bytes]) => [name.slice(`${candidateName}.app/`.length), bytes])));
        assert.ok((await readFile(path.join(mount, 'README.txt'))).equals(expected.get('README.txt')));
        const app = path.join(mount, `${candidateName}.app`);
        run('/usr/bin/codesign', ['--verify', '--strict', '--deep', app]);
        const binary = path.join(app, 'Contents/MacOS/liberty-native');
        assertNativeBinary(await readFile(binary), target);
        assert.equal(run(binary, ['--version']), `Liberty Native ${version} (candidate)`);
      } finally { if (mounted) run('/usr/bin/hdiutil', ['detach', mount]); }
    } else {
      const productVersion = installerProductVersion(version);
      const template = await readFile(new URL('../../packages/desktop-resources/distribution/windows.wxs.in', import.meta.url), 'utf8');
      await writeFile(path.join(stage, 'installer.wxs'), renderWix(template, { target, version, productVersion, files: await readdir(payload) }));
      await compileMsi(stage, file);
      const metadata = JSON.parse(powershell(msiQueryScript(path.join(stage, file))));
      assertMsiMetadata(metadata, target, version, { productVersion });
      const extracted = path.join(stage, 'extracted');
      await mkdir(extracted);
      powershell(`$p=Start-Process -FilePath msiexec.exe -ArgumentList @('/a',${ps(`"${path.join(stage, file)}"`)},'/qn',${ps(`TARGETDIR="${extracted}"`)}) -Wait -PassThru; if($p.ExitCode -ne 0){throw 'MSI extraction failed'}`);
      const extractedFiles = await regularFiles(extracted);
      const installed = [...extractedFiles].filter(([name]) => !name.endsWith('.msi'));
      const expected = await regularFiles(payload);
      assert.equal(installed.length, expected.size, 'MSI extraction contains duplicate or extra files');
      const actual = new Map(installed.map(([name, bytes]) => [path.posix.basename(name), bytes]));
      compareFiles(actual, expected);
      // Execute from the extracted MSI, not from the original build directory.
      const name = [...extractedFiles.keys()].find(name => name.endsWith(`/${candidateName}.exe`) || name === `${candidateName}.exe`);
      assertNativeBinary(actual.get(`${candidateName}.exe`), target);
      assert.equal(run(path.join(extracted, name), ['--version']), `Liberty Native ${version} (candidate)`);
    }
    const installer = await readFile(path.join(stage, file));
    await mkdir(output, { recursive: true });
    await writeFile(path.join(output, file), installer, { flag: 'wx' });
    assert.ok((await readFile(path.join(output, file))).equals(installer), 'Published local installer bytes differ');
    return installer;
  } finally { await rm(stage, { recursive: true, force: true }); }
}

export async function buildInstallerArtifacts({ input, target, sourceCommit }) {
  assertRunnableTarget(target);
  assert.match(sourceCommit ?? '', /^[a-f0-9]{40}$/);
  // Source verification already ran in the preceding CI step. Authenticate that
  // exact contract before converting the portable input to an installer.
  const build = JSON.parse(await readFile(path.join(input, 'native-build-manifest.json')));
  const version = build.version;
  const name = `liberty-native-candidate-${version}-${target}`;
  const archiveName = `${name}.tar.gz`;
  const names = [archiveName, 'candidate-manifest.json', 'native-build-manifest.json'];
  sameNames(await readdir(input), [...names, 'SHA256SUMS']);
  for (const name of [...names, 'SHA256SUMS']) assert.ok((await lstat(path.join(input, name))).isFile(), 'Input must be regular files');
  const bytes = new Map(await Promise.all(names.map(async name => [name, await readFile(path.join(input, name))])));
  assert.equal(await readFile(path.join(input, 'SHA256SUMS'), 'utf8'), names.map(name => `${fileDigest(bytes.get(name))}  ${name}\n`).join(''));
  const archive = bytes.get(archiveName);
  assert.deepEqual(build, { format: 1, sourceCommit, version, target, channel: 'candidate', productionReady: false, signed: false, notarized: false, productionSecurityClearance: false, files: [describe(archiveName, archive), describe('candidate-manifest.json', bytes.get('candidate-manifest.json'))] });
  const archived = readNativeArchive(archive);
  assert.ok([...archived.keys()].every(file => file.startsWith(`${name}/`)), 'Installer archive root differs');
  const files = new Map([...archived].map(([file, bytes]) => [file.slice(name.length + 1), bytes]));
  assert.ok(files.get('candidate-manifest.json')?.equals(bytes.get('candidate-manifest.json')));
  const provenance = { sourceCommit, version, target, identity: candidateIdentity, inputArchiveSha256: fileDigest(archive), updatesURL };
  const installer = await createInstaller({ files, version, target, provenance, output: input });
  const manifest = installerManifest({ version, target, sourceCommit, archive, installer });
  assertInstallerManifest(manifest, { version, target, sourceCommit, archive, installer });
  const manifestName = 'installer-manifest.json';
  const manifestBytes = json(manifest);
  await writeFile(path.join(input, manifestName), manifestBytes, { flag: 'wx' });
  bytes.set(installerName(version, target), installer);
  bytes.set(manifestName, manifestBytes);
  await writeFile(path.join(input, 'SHA256SUMS'), [...bytes].map(([name, value]) => `${fileDigest(value)}  ${name}\n`).join(''));
  sameNames(await readdir(input), [...bytes.keys(), 'SHA256SUMS']);
  for (const [name, value] of bytes) assert.ok((await readFile(path.join(input, name))).equals(value), 'Installer artifact read-back differs');
  return manifest;
}
