import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { readNativeArchive, verifyNativeArtifacts } from '../check-native-release-policy.mjs';
import { assertNativeBinary, assertRunnableTarget, candidateIdentity, candidateName, fileDigest, nativeTargets } from './native-package.mjs';

export const repository = fileURLToPath(new URL('../../', import.meta.url));
export const wixTool = Object.freeze({
  version: '3.14.1',
  url: 'https://github.com/wixtoolset/wix3/releases/download/wix3141rtm/wix314-binaries.zip',
  bytes: 41297555,
  sha256: '6ac824e1642d6f7277d0ed7ea09411a508f6116ba6fae0aa5f2c7daa2ff43d31',
});
export const upgradeCodes = Object.freeze({
  'windows-x64': 'D13F1484-68EA-4ABA-B1A8-926C3DE98A02',
});
export const productionPromotion = Object.freeze({
  productionIdentity: 'com.westng.liberty',
  requiredChange: 'Reviewed cutover must switch Go launch channel/profile, bundle identity, input manifest policy and the release entry together, then retire the old client; installer UpgradeCodes also change when an installer is delivered.',
  requiredEvidence: ['all-three-target-execution', 'ui-accessibility-parity', 'runtime-security-clearance', 'data-migration-and-restore', 'distribution-upgrade-and-rollback', 'production-observation'],
});
const env = { ...process.env, GOTOOLCHAIN: 'go1.27.4', GOFLAGS: '-mod=readonly', GOWORK: 'off' };
const xml = value => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;').replaceAll("'", '&apos;');
const ps = value => `'${String(value).replaceAll("'", "''")}'`;
const certificatePath = thumbprint => ['Cert:', 'CurrentUser', 'My', thumbprint].join('\\');
const digestFile = async file => fileDigest(await readFile(file));

export function parseDistributionArgs(args) {
  const [command, ...flags] = args;
  if (!['check', 'plan', 'prepare', 'build', 'verify', 'accept-install'].includes(command)) throw new Error('Expected check, plan, prepare, build, verify or accept-install');
  const options = { command };
  for (let i = 0; i < flags.length; i += 2) {
    const key = flags[i]?.slice(2);
    if (!['target', 'commit', 'output', 'previous'].includes(key) || flags[i] !== `--${key}` || !flags[i + 1] || flags[i + 1].startsWith('--') || Object.hasOwn(options, key)) throw new Error('Unknown, duplicate or incomplete distribution option');
    options[key] = flags[i + 1];
  }
  if (options.target && !Object.hasOwn(nativeTargets, options.target)) throw new Error('Unknown Native distribution target');
  if (command !== 'check' && !options.target) throw new Error('Exact Native distribution target required');
  if (['prepare', 'build', 'verify', 'accept-install'].includes(command) && (!/^[a-f0-9]{40}$/.test(options.commit ?? '') || !options.output)) throw new Error('Exact source commit and output required');
  if ((command === 'accept-install') !== Boolean(options.previous)) throw new Error('Previous signed MSI required only for accept-install');
  if (['plan', 'check'].includes(command) && (options.commit || options.output)) throw new Error('Plan/check do not produce artifacts');
  return options;
}

export function msiVersion(version) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) throw new Error('MSI requires a stable three-part version');
  const parts = version.split('.').map(Number);
  if (parts[0] > 255 || parts[1] > 255 || parts[2] > 65535) throw new Error('Version exceeds Windows Installer limits');
  return version;
}

// MSI compares only three numeric fields. Use the same ordered product version
// for community and certificate-backed installers sharing an UpgradeCode.
export function installerProductVersion(version) {
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(alpha|beta|rc)\.([1-9]\d*))?$/.exec(version);
  if (!match) throw new Error('Installer version requires stable SemVer or alpha.N, beta.N, rc.N');
  const [, major, minor, patch, stage, sequence] = match;
  const rank = stage ? { alpha: 0, beta: 300, rc: 600 }[stage] + Number(sequence) : 999;
  if (+major > 255 || +minor > 255 || +patch > 64 || (sequence && +sequence > 299)) throw new Error('Installer version exceeds ordered MSI limits');
  return `${+major}.${+minor}.${+patch * 1000 + rank}`;
}

export function renderWix(template, { target, version, files, productVersion = msiVersion(version) }) {
  if (!Object.hasOwn(upgradeCodes, target)) throw new Error('MSI target must be Windows x64');
  msiVersion(productVersion);
  const expected = [`${candidateName}.exe`, 'README.txt', 'LICENSE', 'THIRD_PARTY_NOTICES.txt', 'LOCAL_TRANSLATION_NOTICES.txt', 'icon.ico', 'native-distribution-provenance.json'];
  assert.deepEqual([...files].sort(), expected.sort(), 'Installer payload must be the exact verified candidate file set');
  const win64 = 'yes';
  const components = files.map((name, index) => `            <Component Id="Payload${index}" Guid="*" Win64="${win64}">\n              <File Id="File${index}" Source="payload\\${xml(name)}" Name="${xml(name)}" Checksum="yes" />\n              <RegistryValue Root="HKCU" Key="Software\\Liberty\\NativeCandidate\\${target}" Name="File${index}" Type="integer" Value="1" KeyPath="yes" />\n            </Component>`).join('\n');
  const values = { TARGET: target, VERSION: productVersion, UPGRADE_CODE: upgradeCodes[target], PLATFORM: 'x64', WIN64: win64, ARCH_CONDITION: 'VersionNT64', COMPONENTS: components, COMPONENT_REFS: files.map((_, i) => `      <ComponentRef Id="Payload${i}" />`).join('\n') };
  const rendered = template.replace(/@([A-Z_]+)@/g, (_, key) => {
    if (!Object.hasOwn(values, key)) throw new Error('Unknown installer template placeholder');
    return values[key];
  });
  if (/@[A-Z_]+@/.test(rendered) || /RemoveFile|RemoveFolder[^>]*Directory=|DeleteFile|RemoveRegistryKey/.test(rendered)) throw new Error('Unsafe installer deletion policy');
  return rendered;
}

export function verifyWixBytes(bytes) {
  if (bytes.length !== wixTool.bytes || fileDigest(bytes) !== wixTool.sha256) throw new Error('WiX archive differs from pinned official 3.14.1 bytes');
}

function run(command, args, { cwd = repository, input, timeout = 30 * 60_000, environment = env } = {}) {
  try { return execFileSync(command, args, { cwd, env: environment, input, timeout, encoding: 'utf8', maxBuffer: 8 * 1024 * 1024, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] }).trim(); }
  catch { throw new Error(`Native distribution tool failed: ${path.basename(command)} ${args[0] ?? ''} (tool output withheld; may contain account information)`); }
}
function powershell(source) {
  if (process.platform !== 'win32' || !process.env.SystemRoot) throw new Error('Windows PowerShell requires a Windows host');
  return run(path.join(process.env.SystemRoot, 'System32/WindowsPowerShell/v1.0/powershell.exe'), ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(`$ErrorActionPreference='Stop'; ${source}`, 'utf16le').toString('base64')]);
}
function pnpm(args) {
  // Windows cannot execute a .cmd through execFile directly. These commands
  // are a closed static allowlist; no path or caller input enters cmd.exe.
  if (!args.every(arg => /^(?:--version|native:[a-z:]+)$/.test(arg))) throw new Error('Unsupported distribution package-manager command');
  return process.platform === 'win32'
    ? run(path.join(process.env.SystemRoot, 'System32/cmd.exe'), ['/d', '/s', '/c', `pnpm.cmd ${args.join(' ')}`])
    : run('pnpm', args);
}
function credential(name, pattern) {
  const value = process.env[name];
  if (!value || !pattern.test(value)) throw new Error(`Missing or invalid ${name}; signing/notarization cannot be skipped`);
  return value;
}
export function distributionPlan(target) {
  if (!Object.hasOwn(nativeTargets, target)) throw new Error('Unknown Native distribution target');
  return {
    target, channel: 'candidate', identity: candidateIdentity, productionReady: false,
    format: target.startsWith('darwin-') ? 'signed-notarized-dmg' : 'signed-per-user-msi',
    requirements: target.startsWith('darwin-') ? ['matching macOS host', 'Developer ID Application identity in keychain', 'notarytool keychain profile', 'Apple codesign, ditto, hdiutil, spctl and Xcode command-line tools'] : ['matching Windows x64 host', 'WiX 3.14.1 pinned ZIP', 'CurrentUser certificate with code-signing private key', 'trusted RFC3161/Authenticode timestamp service'],
    installer: { removesUserData: false, upgrade: target.startsWith('windows-') ? 'MSI MajorUpgrade afterInstallInitialize transaction; explicit downgrade refusal; isolated candidate UpgradeCode' : 'Replace the stopped candidate .app through Finder; retain prior signed DMG for rollback', databaseRollbackVerified: false },
    productionPromotion,
  };
}

export function checkDistributionHost(target) {
  assertRunnableTarget(target);
  if (target.startsWith('darwin-')) {
    const identity = credential('LIBERTY_MAC_SIGN_IDENTITY', /^[A-Fa-f0-9]{40}$/);
    credential('LIBERTY_MAC_TEAM_ID', /^[A-Z0-9]{10}$/);
    const notary = credential('LIBERTY_MAC_NOTARY_PROFILE', /^[A-Za-z0-9_.-]{1,128}$/);
    if (!run('/usr/bin/security', ['find-identity', '-v', '-p', 'codesigning']).split('\n').some(line => line.toUpperCase().includes(identity.toUpperCase()) && line.includes('Developer ID Application:'))) throw new Error('Requested Developer ID Application identity is unavailable');
    run('/usr/bin/xcrun', ['notarytool', 'history', '--keychain-profile', notary, '--output-format', 'json']);
    return { os: run('/usr/bin/sw_vers', ['-productVersion']), xcode: run('/usr/bin/xcodebuild', ['-version']) };
  }
  const thumbprint = credential('LIBERTY_WINDOWS_SIGN_THUMBPRINT', /^[A-Fa-f0-9]{40}$/).toUpperCase();
  powershell(`$c=Get-Item ${ps(certificatePath(thumbprint))}; if(-not $c.HasPrivateKey -or $c.NotAfter -le (Get-Date) -or -not ($c.EnhancedKeyUsageList.ObjectId -contains '1.3.6.1.5.5.7.3.3')){throw 'Missing usable code-signing certificate'}`);
  return { os: process.platform, architecture: process.arch, wix: wixTool };
}

async function safeDirectory(directory, create = false) {
  const absolute = path.resolve(directory);
  const parsed = path.parse(absolute);
  let current = parsed.root;
  for (const part of absolute.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    let info;
    try { info = await lstat(current); }
    catch (error) {
      if (error.code !== 'ENOENT') throw error;
      if (!create) return absolute;
      await mkdir(current);
      info = await lstat(current);
    }
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error('Distribution directory or ancestor is not a regular directory');
  }
  return absolute;
}

async function freshStage(output) {
  const absolute = path.resolve(output);
  await safeDirectory(path.dirname(absolute), true);
  try { await lstat(absolute); throw new Error('Distribution output already exists; refusing overwrite'); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  return { output: absolute, stage: await mkdtemp(path.join(path.dirname(absolute), '.native-distribution-')) };
}

async function populate(stage, target, commit, rootDirectory, gatesPassed = false) {
  const report = await verifyNativeArtifacts({ rootDirectory, target, commit, output: path.join(stage, 'input') });
  const name = `liberty-native-candidate-${report.version}-${target}`;
  const archive = await readFile(path.join(stage, 'input', `${name}.tar.gz`));
  const records = readNativeArchive(archive);
  const payload = path.join(stage, 'payload');
  await mkdir(payload);
  for (const [entry, bytes] of records) {
    const name = entry.slice(entry.indexOf('/') + 1);
    if (name === 'candidate-manifest.json' || name === 'README.txt') continue;
    const destination = path.join(payload, name);
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, bytes, { flag: 'wx' });
  }
  await writeFile(path.join(payload, 'README.txt'), `Liberty Native Candidate ${report.version} (${target})\n本包仍为隔离候选版，不代表完成生产切换。卸载不删除会议、模型、凭据或资料目录。\nThis isolated candidate does not establish production cutover. Uninstall preserves meetings, models, credentials and profile directories.\nSignature/notarization results belong to the outer distribution-manifest.json; preparation alone is not a distributable installer.\nKeep the previous signed installer and a validated data snapshot before upgrading. Installer rollback does not prove database rollback.\n`);
  const provenance = { format: 1, sourceCommit: commit, target, version: report.version, identity: candidateIdentity, productionReady: false, inputManifestSha256: await digestFile(path.join(stage, 'input/candidate-manifest.json')), qualityPassed: gatesPassed, securityPassed: gatesPassed };
  const provenanceFile = target.startsWith('darwin-') ? path.join(payload, `${candidateName}.app/Contents/Resources/native-distribution-provenance.json`) : path.join(payload, 'native-distribution-provenance.json');
  await writeFile(provenanceFile, JSON.stringify(provenance, null, 2) + '\n', { flag: 'wx' });
  if (target.startsWith('darwin-')) await chmod(path.join(payload, `${candidateName}.app/Contents/MacOS/liberty-native`), 0o755);
  else await writeFile(path.join(stage, 'installer.wxs'), renderWix(await readFile(new URL('../../packages/desktop-resources/distribution/windows.wxs.in', import.meta.url), 'utf8'), { target, version: report.version, productVersion: installerProductVersion(report.version), files: await readdir(payload) }));
  return report;
}

export async function prepareDistribution({ target, commit, output, rootDirectory = repository }) {
  const paths = await freshStage(output);
  try {
    const report = await populate(paths.stage, target, commit, rootDirectory);
    await writeFile(path.join(paths.stage, 'preparation.json'), JSON.stringify({ format: 1, sourceCommit: commit, target, version: report.version, prepared: true, distributable: false, productionReady: false, plan: distributionPlan(target) }, null, 2) + '\n');
    await rename(paths.stage, paths.output);
    return paths.output;
  } catch (error) { await rm(paths.stage, { recursive: true, force: true }); throw error; }
}

async function inventory(directory, prefix = '') {
  const files = [];
  for (const entry of await readdir(path.join(directory, prefix), { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) files.push(...await inventory(directory, relative));
    else if (entry.isFile()) files.push({ path: relative, sha256: await digestFile(path.join(directory, relative)) });
    else throw new Error('Distribution contains a link or special file');
  }
  return files.sort((a, b) => a.path.localeCompare(b.path));
}

function assertMacSignature(app) {
  run('/usr/bin/codesign', ['--verify', '--strict', '--deep', app]);
  const identity = credential('LIBERTY_MAC_TEAM_ID', /^[A-Z0-9]{10}$/);
  run('/usr/bin/codesign', ['--verify', '--strict', '-R', `anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "${identity}"`, app]);
}
function macNotarize(file) {
  const profile = credential('LIBERTY_MAC_NOTARY_PROFILE', /^[A-Za-z0-9_.-]{1,128}$/);
  const result = JSON.parse(run('/usr/bin/xcrun', ['notarytool', 'submit', file, '--keychain-profile', profile, '--wait', '--timeout', '20m', '--output-format', 'json']));
  if (result.status !== 'Accepted' || !/^[a-f0-9-]{36}$/i.test(result.id ?? '')) throw new Error('Apple notarization did not return Accepted with a submission identity');
  return result.id;
}
async function macBuild(stage, target, version) {
  const app = path.join(stage, 'payload', `${candidateName}.app`);
  const sign = credential('LIBERTY_MAC_SIGN_IDENTITY', /^[A-Fa-f0-9]{40}$/);
  run('/usr/bin/codesign', ['--force', '--options', 'runtime', '--timestamp', '--sign', sign, app]);
  assertMacSignature(app);
  const zip = path.join(stage, 'notary-app.zip');
  run('/usr/bin/ditto', ['-c', '-k', '--keepParent', app, zip]);
  const appSubmission = macNotarize(zip);
  run('/usr/bin/xcrun', ['stapler', 'staple', app]);
  run('/usr/bin/xcrun', ['stapler', 'validate', app]);
  run('/usr/sbin/spctl', ['--assess', '--type', 'execute', app]);
  const file = `liberty-native-candidate-${version}-${target}.dmg`;
  const dmg = path.join(stage, file);
  run('/usr/bin/hdiutil', ['create', '-format', 'UDZO', '-volname', `Liberty Native Candidate ${version}`, '-srcfolder', path.join(stage, 'payload'), dmg]);
  run('/usr/bin/codesign', ['--timestamp', '--sign', sign, dmg]);
  const dmgSubmission = macNotarize(dmg);
  run('/usr/bin/xcrun', ['stapler', 'staple', dmg]);
  await verifyMacDmg(dmg, target, version, stage);
  return { file, signed: true, notarized: true, notarization: { appSubmission, dmgSubmission } };
}
async function verifyMacDmg(file, target, version, stage, expectedProvenance) {
  assertMacSignature(file);
  run('/usr/bin/xcrun', ['stapler', 'validate', file]);
  run('/usr/sbin/spctl', ['--assess', '--type', 'open', '--context', 'context:primary-signature', file]);
  run('/usr/bin/hdiutil', ['verify', file]);
  const mount = await mkdtemp(path.join(stage, '.mount-'));
  let attached = false;
  try {
    run('/usr/bin/hdiutil', ['attach', '-readonly', '-nobrowse', '-mountpoint', mount, file]);
    attached = true;
    const app = path.join(mount, `${candidateName}.app`);
    assertMacSignature(app);
    run('/usr/bin/xcrun', ['stapler', 'validate', app]);
    run('/usr/sbin/spctl', ['--assess', '--type', 'execute', app]);
    const binary = path.join(app, 'Contents/MacOS/liberty-native');
    assertNativeBinary(await readFile(binary), target);
    if (run(binary, ['--version'], { timeout: 15_000 }) !== `Liberty Native ${version} (candidate)`) throw new Error('Mounted distribution version/channel mismatch');
    const provenance = JSON.parse(await readFile(path.join(app, 'Contents/Resources/native-distribution-provenance.json'), 'utf8'));
    if (expectedProvenance) assertSignedProvenance(provenance, expectedProvenance);
    const source = path.join(stage, 'payload');
    try { assert.deepEqual(await inventory(mount), await inventory(source)); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  } finally {
    if (attached) run('/usr/bin/hdiutil', ['detach', mount]);
    await rm(mount, { recursive: true, force: true });
  }
}

export function authenticodeScript(file, sign = false) {
  const thumb = credential('LIBERTY_WINDOWS_SIGN_THUMBPRINT', /^[A-Fa-f0-9]{40}$/).toUpperCase();
  return `${sign ? `$c=Get-Item ${ps(certificatePath(thumb))}; Set-AuthenticodeSignature -LiteralPath ${ps(file)} -Certificate $c -HashAlgorithm SHA256 -TimestampServer 'https://timestamp.digicert.com' | Out-Null; ` : ''}$s=Get-AuthenticodeSignature -LiteralPath ${ps(file)}; if($s.Status -ne 'Valid' -or $s.SignerCertificate.Thumbprint -ne '${thumb}' -or $null -eq $s.TimeStamperCertificate){throw 'Authenticode signer/trust/timestamp verification failed'}`;
}
async function installWix(stage) {
  const archive = path.join(stage, 'wix314-binaries.zip');
  const response = await fetch(wixTool.url, { signal: AbortSignal.timeout(120_000) });
  if (!response.ok) throw new Error('Pinned WiX download failed');
  const bytes = Buffer.from(await response.arrayBuffer());
  verifyWixBytes(bytes);
  await writeFile(archive, bytes, { flag: 'wx' });
  const tools = path.join(stage, 'wix');
  powershell(`Expand-Archive -LiteralPath ${ps(archive)} -DestinationPath ${ps(tools)}`);
  return tools;
}
export function msiQueryScript(file) {
  return `$i=New-Object -ComObject WindowsInstaller.Installer; $d=$i.OpenDatabase(${ps(file)},0); $p=@{}; $q=$d.OpenView('SELECT \`Property\`,\`Value\` FROM \`Property\`'); $q.Execute(); while($r=$q.Fetch()){$p[$r.StringData(1)]=$r.StringData(2)}; $f=@(); $q=$d.OpenView('SELECT \`FileName\` FROM \`File\`'); $q.Execute(); while($r=$q.Fetch()){$f+=($r.StringData(1) -split '\\|')[-1]}; @{properties=$p;files=@($f);template=$d.SummaryInformation(0).Property(7)} | ConvertTo-Json -Depth 5 -Compress`;
}
export function assertMsiMetadata(metadata, target, version, { allowLegacyNotices = false, productVersion = msiVersion(version) } = {}) {
  if (!Object.hasOwn(upgradeCodes, target)) throw new Error('MSI target must be Windows x64');
  const properties = metadata.properties ?? {};
  if (properties.ProductName !== `Liberty Native Candidate (${target})` || properties.ProductVersion !== msiVersion(productVersion) || properties.UpgradeCode?.replace(/[{}]/g, '').toUpperCase() !== upgradeCodes[target] || properties.ALLUSERS || metadata.template?.split(';')[0] !== 'x64') throw new Error('MSI product, version, identity, per-user scope or architecture mismatch');
  const expected = [`${candidateName}.exe`, 'README.txt', 'LICENSE', 'THIRD_PARTY_NOTICES.txt', 'icon.ico', 'native-distribution-provenance.json'];
  if (!allowLegacyNotices || metadata.files.includes('LOCAL_TRANSLATION_NOTICES.txt')) expected.push('LOCAL_TRANSLATION_NOTICES.txt');
  assert.deepEqual([...metadata.files].sort(), expected.sort(), 'MSI must embed the exact file set');
  if (!/^\{[A-F0-9-]{36}\}$/i.test(properties.ProductCode ?? '')) throw new Error('MSI ProductCode missing');
  return properties;
}
async function verifyMsi(file, target, version, stage, comparePayload = false, expectedProvenance) {
  powershell(authenticodeScript(file));
  const metadata = JSON.parse(powershell(msiQueryScript(file)));
  assertMsiMetadata(metadata, target, version, { productVersion: installerProductVersion(version) });
  const extracted = path.join(stage, '.msi-extracted');
  await mkdir(extracted);
  try {
    powershell(`$p=Start-Process -FilePath msiexec.exe -ArgumentList @('/a',${ps(`"${file}"`)},'/qn',${ps(`TARGETDIR="${extracted}"`)}) -Wait -PassThru; if($p.ExitCode -ne 0){throw 'MSI extraction failed'}`);
    const files = (await inventory(extracted)).filter(entry => !entry.path.endsWith('.msi'));
    assert.deepEqual(files.map(entry => path.posix.basename(entry.path)).sort(), [...metadata.files].sort());
    const binary = files.find(entry => entry.path.endsWith('.exe'));
    const executable = path.join(extracted, binary.path);
    assertNativeBinary(await readFile(executable), target);
    powershell(authenticodeScript(executable));
    if (run(executable, ['--version'], { timeout: 15_000 }) !== `Liberty Native ${version} (candidate)`) throw new Error('MSI extracted executable version/channel mismatch');
    const provenanceFile = files.find(entry => path.posix.basename(entry.path) === 'native-distribution-provenance.json');
    const provenance = JSON.parse(await readFile(path.join(extracted, provenanceFile.path), 'utf8'));
    if (expectedProvenance) assertSignedProvenance(provenance, expectedProvenance);
    if (comparePayload) for (const entry of files) {
      if (entry.sha256 !== await digestFile(path.join(stage, 'payload', path.posix.basename(entry.path)))) throw new Error('MSI embedded bytes differ from signed payload');
    }
  } finally { await rm(extracted, { recursive: true, force: true }); }
  return metadata;
}
async function windowsBuild(stage, target, version) {
  const tools = await installWix(stage);
  powershell(authenticodeScript(path.join(stage, 'payload', `${candidateName}.exe`), true));
  run(path.join(tools, 'candle.exe'), ['-nologo', '-arch', 'x64', '-out', 'installer.wixobj', 'installer.wxs'], { cwd: stage });
  const file = `liberty-native-candidate-${version}-${target}.msi`;
  run(path.join(tools, 'light.exe'), ['-nologo', '-out', file, 'installer.wixobj'], { cwd: stage });
  powershell(authenticodeScript(path.join(stage, file), true));
  await verifyMsi(path.join(stage, file), target, version, stage, true);
  return { file, signed: true, notarized: false, notarization: null, installerTransactionAcceptance: false, wix: wixTool };
}

export async function buildDistribution(options) {
  const { target, commit, output, rootDirectory = repository } = options;
  if (path.resolve(rootDirectory) !== path.resolve(repository)) throw new Error('Distribution builds must use the script checkout');
  const host = checkDistributionHost(target);
  const workspace = JSON.parse(await readFile(path.join(repository, 'package.json'), 'utf8'));
  if (run('git', ['rev-parse', 'HEAD']) !== commit || run('git', ['status', '--porcelain=v1', '--untracked-files=all', '--', 'apps/native', 'packages', 'python', 'scripts', 'config', 'package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', '.npmrc', '.gitattributes', 'LICENSE'])) throw new Error('Distribution requires the exact clean source checkout');
  // Signing must never promote a stale portable candidate merely because its
  // version string still matches HEAD. Build a new source-bound input here.
  const prefix = `liberty-native-candidate-${workspace.version}-${target}-`;
  try {
    if ((await readdir(path.join(repository, 'target/native/packages'))).some(name => name.startsWith(prefix))) throw new Error('Use a fresh checkout/package directory: refusing to reuse or overwrite existing target candidates');
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (pnpm(['--version']) !== '10.30.3') throw new Error('Distribution requires pnpm 10.30.3');
  for (const gate of ['native:verify', 'native:compat', 'native:build:matrix', 'native:security:check', 'native:licenses:check', 'native:sbom:generate']) pnpm([gate]);
  run('go', ['test', '-race', '-count=1', '-timeout=30m', './...'], { cwd: path.join(rootDirectory, 'apps/native') });
  const targetEnvironment = { ...env, CGO_ENABLED: '0', GOOS: nativeTargets[target].os, GOARCH: nativeTargets[target].arch };
  run('go', ['test', '-count=1', '-timeout=30m', './...'], { cwd: path.join(rootDirectory, 'apps/native'), environment: targetEnvironment });
  run('go', ['test', '-count=1', '-tags', 'credentials_acceptance', './internal/infrastructure/credentials'], { cwd: path.join(rootDirectory, 'apps/native'), environment: targetEnvironment });
  run(process.execPath, ['scripts/package-native.mjs', '--target', target, '--verify-host']);
  const paths = await freshStage(output);
  try {
    const input = await populate(paths.stage, target, commit, rootDirectory, true);
    const result = target.startsWith('darwin-') ? await macBuild(paths.stage, target, input.version) : await windowsBuild(paths.stage, target, input.version);
    const artifact = await readFile(path.join(paths.stage, result.file));
    const manifest = { format: 1, channel: 'candidate', identity: candidateIdentity, productionReady: false, productionPromotion, sourceCommit: commit, target, version: input.version, ...result, sha256: fileDigest(artifact), bytes: artifact.length, inputManifestSha256: await digestFile(path.join(paths.stage, 'input/candidate-manifest.json')), qualityPassed: true, securityPassed: true, host, installerTransactionAcceptance: false, databaseRollbackVerified: false };
    assertDistributionManifest(manifest, { target, commit });
    // Publish only the checked installer and evidence. The unsigned candidate
    // input remains byte-for-byte untouched in its original package directory.
    const publication = await mkdtemp(path.join(path.dirname(paths.output), '.native-distribution-publish-'));
    try {
      const manifestBytes = Buffer.from(JSON.stringify(manifest, null, 2) + '\n');
      await writeFile(path.join(publication, result.file), artifact, { flag: 'wx' });
      await writeFile(path.join(publication, 'distribution-manifest.json'), manifestBytes, { flag: 'wx' });
      await writeFile(path.join(publication, 'SHA256SUMS'), `${fileDigest(artifact)}  ${result.file}\n${fileDigest(manifestBytes)}  distribution-manifest.json\n`, { flag: 'wx' });
      await rename(publication, paths.output);
    } catch (error) { await rm(publication, { recursive: true, force: true }); throw error; }
    return manifest;
  } finally { await rm(paths.stage, { recursive: true, force: true }); }
}

export function assertDistributionManifest(manifest, { target, commit }) {
  if (!Object.hasOwn(nativeTargets, target) || manifest?.format !== 1 || manifest.channel !== 'candidate' || manifest.identity !== candidateIdentity || manifest.productionReady !== false || manifest.target !== target || manifest.sourceCommit !== commit || !/^[a-f0-9]{40}$/.test(commit ?? '') || manifest.signed !== true || manifest.qualityPassed !== true || manifest.securityPassed !== true || manifest.databaseRollbackVerified !== false || manifest.installerTransactionAcceptance !== false) throw new Error('Invalid candidate distribution evidence');
  const expected = `liberty-native-candidate-${msiVersion(manifest.version)}-${target}.${target.startsWith('darwin-') ? 'dmg' : 'msi'}`;
  if (manifest.file !== expected || !/^[a-f0-9]{64}$/.test(manifest.sha256 ?? '') || !Number.isSafeInteger(manifest.bytes) || manifest.bytes <= 0 || manifest.notarized !== target.startsWith('darwin-')) throw new Error('Distribution asset binding mismatch');
  if (!/^[a-f0-9]{64}$/.test(manifest.inputManifestSha256 ?? '')) throw new Error('Distribution input manifest digest required');
  return manifest;
}
export function assertSignedProvenance(provenance, manifest) {
  assert.deepEqual(provenance, { format: 1, sourceCommit: manifest.sourceCommit, target: manifest.target, version: manifest.version, identity: candidateIdentity, productionReady: false, inputManifestSha256: manifest.inputManifestSha256, qualityPassed: true, securityPassed: true }, 'Signed embedded source provenance differs from outer distribution evidence');
}
export async function verifyDistribution({ target, commit, output }) {
  assertRunnableTarget(target);
  await safeDirectory(output);
  const manifestBytes = await readFile(path.join(output, 'distribution-manifest.json'));
  const manifest = assertDistributionManifest(JSON.parse(manifestBytes), { target, commit });
  assert.deepEqual((await readdir(output)).sort(), ['distribution-manifest.json', 'SHA256SUMS', manifest.file].sort());
  for (const name of [manifest.file, 'distribution-manifest.json', 'SHA256SUMS']) if (!(await lstat(path.join(output, name))).isFile()) throw new Error('Distribution asset is not a regular file');
  const artifact = await readFile(path.join(output, manifest.file));
  if (artifact.length !== manifest.bytes || fileDigest(artifact) !== manifest.sha256 || await readFile(path.join(output, 'SHA256SUMS'), 'utf8') !== `${fileDigest(artifact)}  ${manifest.file}\n${fileDigest(manifestBytes)}  distribution-manifest.json\n`) throw new Error('Distribution hash mismatch');
  const stage = await mkdtemp(path.join(await realpath(path.dirname(path.resolve(output))), '.verify-distribution-'));
  try {
    if (target.startsWith('darwin-')) await verifyMacDmg(path.resolve(output, manifest.file), target, manifest.version, stage, manifest);
    else await verifyMsi(path.resolve(output, manifest.file), target, manifest.version, stage, false, manifest);
  } finally { await rm(stage, { recursive: true, force: true }); }
  return manifest;
}

export async function acceptWindowsInstall(options) {
  if (!options.target.startsWith('windows-')) throw new Error('MSI transaction acceptance requires Windows');
  const manifest = await verifyDistribution(options);
  const previous = path.resolve(options.previous);
  powershell(authenticodeScript(previous));
  const old = JSON.parse(powershell(msiQueryScript(previous)));
  const oldProperties = assertMsiMetadata(old, options.target, old.properties?.ProductVersion, { allowLegacyNotices: true });
  const current = path.resolve(options.output, manifest.file);
  const currentMetadata = JSON.parse(powershell(msiQueryScript(current)));
  const currentProperties = assertMsiMetadata(currentMetadata, options.target, manifest.version, { productVersion: installerProductVersion(manifest.version) });
  if (currentProperties.ProductCode === oldProperties.ProductCode) throw new Error('Different installer versions must have different ProductCodes');
  const newer = currentProperties.ProductVersion.split('.').map(Number), older = oldProperties.ProductVersion.split('.').map(Number);
  if (!newer.some((part, index) => newer.slice(0, index).every((part, i) => part === older[i]) && part > older[index])) throw new Error('Acceptance requires an older signed candidate MSI');
  const upgrade = `{${upgradeCodes[options.target]}}`;
  powershell(`$i=New-Object -ComObject WindowsInstaller.Installer; if(@($i.RelatedProducts(${ps(upgrade)})).Count -ne 0){throw 'Refuse acceptance while a candidate is installed'}`);
  const directory = await mkdtemp(path.join(await realpath(process.env.TEMP), 'liberty-native-msi-'));
  const destination = path.join(directory, 'program');
  const marker = path.join(directory, 'isolated-profile', 'synthetic-preserve.txt');
  await mkdir(path.dirname(marker)); await writeFile(marker, 'synthetic profile retained\n');
  const install = (file, failure = false, injectFailure = false) => powershell(`$p=Start-Process msiexec.exe -ArgumentList @('/i',${ps(`"${file}"`)},'/qn','/norestart',${ps(`INSTALLFOLDER="${destination}"`)}${injectFailure ? ",'LIBERTY_ACCEPTANCE_FAIL=1'" : ''}) -Wait -PassThru; if($p.ExitCode -ne ${failure ? '1603' : '0'}){throw 'Unexpected MSI transaction status'}`);
  const uninstall = code => powershell(`$p=Start-Process msiexec.exe -ArgumentList @('/x',${ps(code)},'/qn','/norestart') -Wait -PassThru; if($p.ExitCode -ne 0){throw 'MSI uninstall failed'}`);
  let active;
  try {
    install(previous); active = oldProperties.ProductCode;
    const binary = path.join(destination, `${candidateName}.exe`);
    const oldHash = await digestFile(binary);
    install(current, true, true);
    if (await digestFile(binary) !== oldHash) throw new Error('Failed upgrade did not restore previous program bytes');
    install(current);
    active = currentProperties.ProductCode;
    if (run(binary, ['--version']) !== `Liberty Native ${manifest.version} (candidate)`) throw new Error('Installed upgrade version mismatch');
    powershell(authenticodeScript(binary));
    const upgradedHash = await digestFile(binary);
    install(previous, true);
    if (await digestFile(binary) !== upgradedHash) throw new Error('Blocked downgrade changed the installed program');
    uninstall(active); active = undefined;
    await assert.rejects(readFile(binary), { code: 'ENOENT' });
    if (await readFile(marker, 'utf8') !== 'synthetic profile retained\n') throw new Error('Uninstall removed isolated profile evidence');
    install(previous); active = oldProperties.ProductCode;
    if (await digestFile(binary) !== oldHash) throw new Error('Explicit installer rollback did not restore previous program bytes');
    uninstall(active); active = undefined;
    return { target: options.target, sourceCommit: options.commit, upgrade: true, failedUpgradeRollback: true, downgradeBlocked: true, uninstall: true, explicitOlderInstallerRollback: true, isolatedProfileRetained: true, productionReady: false, databaseRollbackVerified: false };
  } finally {
    // Even if a transaction or metadata read throws unexpectedly, clean only
    // the two verified candidate ProductCodes created by this acceptance run.
    for (const code of [oldProperties.ProductCode, currentProperties.ProductCode]) {
      powershell(`$i=New-Object -ComObject WindowsInstaller.Installer; if($i.ProductState(${ps(code)}) -eq 5){$p=Start-Process msiexec.exe -ArgumentList @('/x',${ps(code)},'/qn','/norestart') -Wait -PassThru; if($p.ExitCode -ne 0){throw 'Candidate acceptance cleanup failed'}}`);
    }
    await rm(directory, { recursive: true, force: true });
  }
}
