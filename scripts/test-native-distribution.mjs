import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, readdir, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { windowsExecutableFixture, windowsIconFixture } from './test-fixtures/native-windows.mjs';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { assertDistributionManifest, assertMsiMetadata, assertSignedProvenance, authenticodeScript, checkDistributionHost, distributionPlan, msiQueryScript, msiVersion, parseDistributionArgs, prepareDistribution, renderWix, upgradeCodes, verifyWixBytes, wixFailureDetails, wixTool } from './lib/native-distribution.mjs';
import { candidateIdentity, candidateName, candidatePlist, fileDigest, nativeTargets } from './lib/native-package.mjs';

const template = await readFile(new URL('../packages/desktop-resources/distribution/windows.wxs.in', import.meta.url), 'utf8');
const installerFiles = [`${candidateName}.exe`, 'README.txt', 'LICENSE', 'THIRD_PARTY_NOTICES.txt', 'LOCAL_TRANSLATION_NOTICES.txt', 'icon.ico', 'native-distribution-provenance.json'];
const commit = 'a'.repeat(40);
const version = '9.8.7';

test('CLI accepts only explicit preparation/build/verification stages', () => {
  assert.deepEqual(parseDistributionArgs(['check']), { command: 'check' });
  assert.deepEqual(parseDistributionArgs(['plan', '--target', 'windows-x64']), { command: 'plan', target: 'windows-x64' });
  assert.equal(parseDistributionArgs(['build', '--target', 'windows-x64', '--commit', commit, '--output', 'output']).command, 'build');
  assert.equal(parseDistributionArgs(['accept-install', '--target', 'windows-x64', '--commit', commit, '--output', 'output', '--previous', 'old.msi']).previous, 'old.msi');
});
for (const args of [[], ['publish'], ['build'], ['check', '--publish', 'true'], ['check', '--commit', commit], ['prepare', '--target', 'windows-x64', '--commit', commit], ['build', '--target', 'linux-x64', '--commit', commit, '--output', 'out'], ['build', '--target', 'windows-x64', '--commit', 'HEAD', '--output', 'out'], ['build', '--target', 'windows-x64', '--commit', commit, '--output', 'out', '--skip-security', 'true'], ['build', '--target', 'windows-x64', '--target', 'windows-x64'], ['verify', '--target', 'windows-x64', '--commit', commit, '--output', 'out', '--previous', 'old.msi']]) {
  test(`rejects unsafe CLI: ${JSON.stringify(args)}`, () => assert.throws(() => parseDistributionArgs(args)));
}
for (const target of Object.keys(nativeTargets)) {
  test(`plan preserves isolated identity and explicit promotion gates: ${target}`, () => {
    const plan = distributionPlan(target);
    assert.equal(plan.identity, candidateIdentity);
    assert.equal(plan.productionReady, false);
    assert.equal(plan.installer.removesUserData, false);
    assert.ok(plan.productionPromotion.requiredEvidence.includes('runtime-security-clearance'));
    assert.ok(plan.productionPromotion.requiredEvidence.includes('distribution-upgrade-and-rollback'));
    assert.ok(!plan.productionPromotion.requiredEvidence.includes('signing-and-notarization'));
    assert.ok(plan.requirements.some(value => /certificate|Developer ID/.test(value)), 'optional signed installers still require signing');
    assert.match(plan.productionPromotion.requiredChange, /launch channel\/profile/);
  });
}
for (const bad of ['1.2.3-beta.1', '1.2.3+build', '01.2.3', '256.1.0', '1.256.0', '1.2.65536', '../1.2.3']) test(`refuses unsupported MSI version ${bad}`, () => assert.throws(() => msiVersion(bad)));

for (const target of ['windows-x64']) {
  test(`MSI ${target} uses transactional per-user upgrades and exact payload`, () => {
    const result = renderWix(template, { target, version, files: installerFiles });
    assert.match(result, /InstallScope="perUser" InstallPrivileges="limited"/);
    assert.match(result, /Platform="x64"/);
    const components = [...result.matchAll(/<Component\s[^>]*>/g)].map(match => match[0]);
    assert.equal(components.length, installerFiles.length + 1);
    for (const component of components) assert.match(component, /\bWin64="yes"/);
    assert.doesNotMatch(result, /@[A-Z_][A-Z0-9_]*@/);
    assert.match(result, />VersionNT64<\/Condition>/);
    assert.match(result, /MajorUpgrade Schedule="afterInstallInitialize"/);
    assert.match(result, /DowngradeErrorMessage=/);
    assert.ok(result.includes(upgradeCodes[target]));
    assert.equal((result.match(/<File /g) ?? []).length, installerFiles.length);
    assert.match(result, /Name="LOCAL_TRANSLATION_NOTICES.txt"/);
    assert.match(result, /<Icon Id="CandidateIcon\.exe" SourceFile="payload\\Liberty Native Candidate\.exe"/);
    assert.match(result, /<Property Id="ARPPRODUCTICON" Value="CandidateIcon\.exe"/);
    assert.match(result, /<Shortcut[^>]+Icon="CandidateIcon\.exe" IconIndex="0"/);
    assert.match(result, /Custom Action="AcceptanceRollback" After="InstallFiles"/);
    assert.match(result, /Execute="deferred" Return="check" Impersonate="yes"/);
    assert.doesNotMatch(result, /RemoveFile|DeleteFile|RemoveRegistryKey|Id="AppDataFolder"|com\.westng\.liberty[^.]/);
    assert.equal((result.match(/RemoveFolder/g) ?? []).length, 1, 'only installer-owned shortcut folder is removed');
  });
}
test('installer identity is isolated from production', () => {
  assert.notEqual(candidateIdentity, distributionPlan('windows-x64').productionPromotion.productionIdentity);
});
test('file components with registry keypaths have stable explicit GUIDs', () => {
  const render = (files, version) => renderWix(template, { target: 'windows-x64', version, files });
  const result = render(installerFiles, version);
  const components = [...result.matchAll(/<Component\s[^>]*>[\s\S]*?<\/Component>/g)].map(match => match[0]);
  const fileComponents = components.filter(component => component.includes('<File '));
  const guids = fileComponents.map(component => {
    assert.match(component, /<RegistryValue[^>]+Root="HKCU"[^>]+KeyPath="yes"/);
    const guid = /\bGuid="([A-F0-9-]{36})"/.exec(component)?.[1];
    assert.ok(guid, 'WiX cannot auto-generate a GUID for a file component with a registry keypath');
    return guid;
  });
  assert.equal(new Set(guids).size, installerFiles.length);
  const updated = render([...installerFiles].reverse(), '9.8.8');
  assert.deepEqual([...updated.matchAll(/<Component\s[^>]*>[\s\S]*?<\/Component>/g)].map(match => match[0]), components, 'component identity and registry keypaths must survive upgrades and directory enumeration order');
});
for (const files of [installerFiles.slice(1), installerFiles.filter(name => name !== 'LOCAL_TRANSLATION_NOTICES.txt'), [...installerFiles, 'secret.txt'], [...installerFiles, '..\\escape.exe'], [...installerFiles, 'bad"/>&.exe']]) test(`refuses noncanonical installer payload ${JSON.stringify(files)}`, () => assert.throws(() => renderWix(template, { target: 'windows-x64', version, files })));
test('refuses unknown template placeholders and wildcard deletion', () => {
  for (const extra of ['@UNREVIEWED@', '@UNREVIEWED64@', '<RemoveFile Name="*" On="uninstall" />']) assert.throws(() => renderWix(template + extra, { target: 'windows-x64', version, files: installerFiles }));
});
test('fixed WiX source is the official 3.14.1 release and rejects unverified bytes', () => {
  assert.equal(wixTool.url, 'https://github.com/wixtoolset/wix3/releases/download/wix3141rtm/wix314-binaries.zip');
  assert.equal(wixTool.sha256, '6ac824e1642d6f7277d0ed7ea09411a508f6116ba6fae0aa5f2c7daa2ff43d31');
  assert.throws(() => verifyWixBytes(Buffer.from('not a trusted compiler')));
});
test('WiX failures expose only bounded codes and template locations', () => {
  const error = {
    stdout: 'C:\\Users\\private-account\\installer.wxs(17) : error CNDL0230 : synthetic sensitive output\n',
    stderr: 'light.exe : error LGHT0204 : ICE64: synthetic private path\nraw account details\n',
  };
  assert.equal(wixFailureDetails(error), '; CNDL0230 at installer.wxs:17, LGHT0204 ICE64');
  assert.equal(wixFailureDetails({ stderr: 'no safe diagnostic fields' }), '');
  assert.equal(wixFailureDetails({ stderr: 'LIBERTY_POWERSHELL_ERROR=ArchiveCmdletPathNotFound\nprivate path' }), '; PowerShell ArchiveCmdletPathNotFound');
  assert.equal(wixFailureDetails({ stderr: 'LIBERTY_POWERSHELL_ERROR=C:\\private\\path' }), '');
  assert.equal(wixFailureDetails({ stdout: Array.from({ length: 50 }, (_, i) => `installer.wxs(${i + 1}) : error CNDL0230 : private`).join('\n') }).split(', ').length, 12);
});
test('Authenticode commands preserve literal paths and demand trusted signer and timestamp', () => {
  const saved = process.env.LIBERTY_WINDOWS_SIGN_THUMBPRINT;
  process.env.LIBERTY_WINDOWS_SIGN_THUMBPRINT = 'A'.repeat(40);
  try {
    const script = authenticodeScript("C:\\synthetic O'Brien\\candidate.msi", true);
    assert.ok(script.includes("'Cert:\\CurrentUser\\My\\" + 'A'.repeat(40) + "'"));
    assert.ok(script.includes("'C:\\synthetic O''Brien\\candidate.msi'"));
    assert.match(script, /HashAlgorithm SHA256/);
    assert.match(script, /TimeStamperCertificate/);
    assert.match(script, /Status -ne 'Valid'/);
    assert.match(msiQueryScript("C:\\O'Brien.msi"), /O''Brien/);
  } finally { if (saved === undefined) delete process.env.LIBERTY_WINDOWS_SIGN_THUMBPRINT; else process.env.LIBERTY_WINDOWS_SIGN_THUMBPRINT = saved; }
});

function metadata(target = 'windows-x64') {
  return { properties: { ProductName: `Liberty Native Candidate (${target})`, ProductVersion: version, UpgradeCode: `{${upgradeCodes[target]}}`, ProductCode: '{AABBCCDD-1111-2222-3333-444455556666}' }, template: 'x64;1033', files: installerFiles };
}
test('accepts architecture-specific MSI metadata', () => { for (const target of Object.keys(upgradeCodes)) assertMsiMetadata(metadata(target), target, version); });
test('accepts the old notice inventory only for a previous MSI rollback input', () => {
  const previous = metadata();
  previous.files = previous.files.filter(name => name !== 'LOCAL_TRANSLATION_NOTICES.txt');
  assert.throws(() => assertMsiMetadata(previous, 'windows-x64', version), /MSI must embed the exact file set/);
  assertMsiMetadata(previous, 'windows-x64', version, { allowLegacyNotices: true });
  previous.files.push('unexpected.txt');
  assert.throws(() => assertMsiMetadata(previous, 'windows-x64', version, { allowLegacyNotices: true }), /MSI must embed the exact file set/);
});
for (const [name, change] of [
  ['machine-wide scope', m => { m.properties.ALLUSERS = '1'; }],
  ['wrong architecture', m => { m.template = 'Intel;1033'; }],
  ['wrong upgrade identity', m => { m.properties.UpgradeCode = '{00000000-1111-2222-3333-444455556666}'; }],
  ['missing provenance', m => { m.files = m.files.filter(name => name !== 'native-distribution-provenance.json'); }],
  ['missing local translation notices', m => { m.files = m.files.filter(name => name !== 'LOCAL_TRANSLATION_NOTICES.txt'); }],
  ['extra payload', m => { m.files = [...m.files, 'unreviewed.exe']; }],
  ['wrong version', m => { m.properties.ProductVersion = '1.0.0'; }],
]) test(`refuses MSI ${name}`, () => { const m = metadata(); change(m); assert.throws(() => assertMsiMetadata(m, 'windows-x64', version)); });

function distribution(target = 'windows-x64') {
  return { format: 1, channel: 'candidate', identity: candidateIdentity, productionReady: false, sourceCommit: commit, target, version, signed: true, notarized: target.startsWith('darwin-'), qualityPassed: true, securityPassed: true, databaseRollbackVerified: false, installerTransactionAcceptance: false, file: `liberty-native-candidate-${version}-${target}.${target.startsWith('darwin-') ? 'dmg' : 'msi'}`, sha256: 'b'.repeat(64), inputManifestSha256: 'c'.repeat(64), bytes: 100 };
}
for (const target of Object.keys(nativeTargets)) test(`accepts signed but unpromoted distribution manifest ${target}`, () => assertDistributionManifest(distribution(target), { target, commit }));
for (const [key, value] of [['productionReady', true], ['identity', 'com.westng.liberty'], ['channel', 'production'], ['sourceCommit', 'b'.repeat(40)], ['signed', false], ['securityPassed', false], ['qualityPassed', false], ['databaseRollbackVerified', true], ['installerTransactionAcceptance', true], ['file', '../escape.msi'], ['bytes', -1], ['inputManifestSha256', 'unknown'], ['notarized', true]]) test(`refuses unsupported distribution claim ${key}`, () => assert.throws(() => assertDistributionManifest({ ...distribution(), [key]: value }, { target: 'windows-x64', commit })));
test('signed embedded provenance binds source to artifact instead of trusting editable outer JSON', () => {
  const manifest = distribution();
  const provenance = { format: 1, sourceCommit: commit, target: manifest.target, version, identity: candidateIdentity, productionReady: false, inputManifestSha256: manifest.inputManifestSha256, qualityPassed: true, securityPassed: true };
  assertSignedProvenance(provenance, manifest);
  for (const [key, value] of [['sourceCommit', 'd'.repeat(40)], ['inputManifestSha256', 'e'.repeat(64)], ['qualityPassed', false], ['securityPassed', false], ['identity', 'com.westng.liberty'], ['productionReady', true]]) assert.throws(() => assertSignedProvenance({ ...provenance, [key]: value }, manifest));
});

function tar(records) {
  const output = [];
  for (const [name, bytes] of records) {
    const header = Buffer.alloc(512), split = name.lastIndexOf('/');
    header.write(name.slice(split + 1), 0, 100); header.write(name.slice(0, split), 345, 155);
    header.write('0000644\0', 100); header.write('0000000\0', 108); header.write('0000000\0', 116);
    header.write(`${bytes.length.toString(8).padStart(11, '0')}\0`, 124); header.write('00000000000\0', 136);
    header.fill(32, 148, 156); header.write('0', 156); header.write('ustar\0', 257); header.write('00', 263);
    header.write(`${header.reduce((sum, byte) => sum + byte, 0).toString(8).padStart(6, '0')}\0 `, 148);
    output.push(header, bytes, Buffer.alloc((512 - bytes.length % 512) % 512));
  }
  return gzipSync(Buffer.concat([...output, Buffer.alloc(1024)]));
}
async function fixture(context, target) {
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), 'liberty-distribution-')));
  context.after(() => rm(directory, { recursive: true, force: true }));
  await mkdir(path.join(directory, 'apps/native/internal/host'), { recursive: true });
  await writeFile(path.join(directory, 'package.json'), JSON.stringify({ version, packageManager: 'pnpm@10.30.3' }));
  await writeFile(path.join(directory, 'apps/native/internal/host/profile.go'), `package host\nconst Version = "${version}"\n`);
  await writeFile(path.join(directory, '.gitignore'), 'target/\noutput/\n');
  const git = (...args) => execFileSync('git', args, { cwd: directory, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  git('init', '--quiet'); git('add', '.'); git('-c', 'user.name=Synthetic Fixture', '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '--quiet', '-m', 'Synthetic committed build input');
  const sourceCommit = git('rev-parse', 'HEAD');
  const mac = target.startsWith('darwin-');
  const resources = mac ? `${candidateName}.app/Contents/Resources/` : '';
  const binary = mac ? `${candidateName}.app/Contents/MacOS/liberty-native` : `${candidateName}.exe`;
  const program = mac ? Buffer.alloc(256) : windowsExecutableFixture();
  if (mac) { program.writeUInt32LE(0xfeedfacf, 0); program.writeUInt32LE(nativeTargets[target].machine, 4); }
  const files = new Map([[binary, program], ['README.txt', Buffer.from('unsigned candidate fixture\n')], [`${resources}LICENSE`, Buffer.from('license')], [`${resources}THIRD_PARTY_NOTICES.txt`, Buffer.from('notices')], [`${resources}LOCAL_TRANSLATION_NOTICES.txt`, Buffer.from('local translation notices')], [`${resources}${mac ? 'icon.icns' : 'icon.ico'}`, mac ? Buffer.from('icon') : windowsIconFixture], ...(mac ? [[`${candidateName}.app/Contents/Info.plist`, Buffer.from(candidatePlist(version))]] : [])]);
  const manifest = { format: 1, channel: 'candidate', identity: candidateIdentity, version, target, productionReady: false, signed: false, notarized: false, defaultRuntimeProfile: 'production', availableRuntimeProfiles: ['production'], files: [...files].map(([path, bytes]) => ({ path, bytes: bytes.length, sha256: fileDigest(bytes) })) };
  const name = `liberty-native-candidate-${version}-${target}`;
  const parent = path.join(directory, 'target/native/packages', `${name}-abcdef`);
  const payload = path.join(parent, name);
  files.set('candidate-manifest.json', Buffer.from(JSON.stringify(manifest) + '\n'));
  for (const [file, bytes] of files) { await mkdir(path.dirname(path.join(payload, file)), { recursive: true }); await writeFile(path.join(payload, file), bytes); }
  const archive = tar([...files].map(([file, bytes]) => [`${name}/${file}`, bytes]));
  await writeFile(path.join(parent, `${name}.tar.gz`), archive);
  await writeFile(path.join(parent, 'SHA256SUMS'), `${fileDigest(archive)}  ${name}.tar.gz\n`);
  return { directory, target, commit: sourceCommit, output: path.join(directory, 'output/prepared'), rootDirectory: directory, archive: path.join(parent, `${name}.tar.gz`), archiveHash: fileDigest(archive) };
}
for (const target of Object.keys(nativeTargets)) test(`prepares real verified bytes without changing candidate or claiming signatures: ${target}`, async context => {
  const f = await fixture(context, target);
  await prepareDistribution(f);
  assert.equal(fileDigest(await readFile(f.archive)), f.archiveHash);
  const preparation = JSON.parse(await readFile(path.join(f.output, 'preparation.json'), 'utf8'));
  assert.equal(preparation.distributable, false);
  assert.equal(preparation.productionReady, false);
  const provenancePath = path.join(f.output, 'payload', target.startsWith('darwin-') ? `${candidateName}.app/Contents/Resources/native-distribution-provenance.json` : 'native-distribution-provenance.json');
  const provenance = JSON.parse(await readFile(provenancePath, 'utf8'));
  assert.equal(provenance.sourceCommit, f.commit);
  assert.equal(provenance.qualityPassed, false);
  assert.equal(provenance.securityPassed, false);
  if (target.startsWith('windows-')) assert.match(await readFile(path.join(f.output, 'installer.wxs'), 'utf8'), /afterInstallInitialize/);
  assert.equal((await readdir(path.join(f.directory, 'output'))).length, 1);
  await assert.rejects(prepareDistribution(f), /refusing overwrite/);
});
test('preparation rejects dirty provenance and removes staging output', async context => {
  const f = await fixture(context, 'windows-x64');
  await writeFile(path.join(f.directory, 'apps/native/dirty.go'), 'package native\n');
  await assert.rejects(prepareDistribution(f), /dirty or untracked/);
  assert.deepEqual(await readdir(path.dirname(f.output)), []);
});
test('preparation refuses symlink ancestors without writing outside', async context => {
  const f = await fixture(context, 'windows-x64');
  const outside = path.join(f.directory, 'outside'); await mkdir(outside);
  await mkdir(path.join(f.directory, 'output'));
  try { await symlink(outside, path.join(f.directory, 'output/link'), 'junction'); }
  catch (error) { if (error.code === 'EPERM') return context.skip('host refuses unprivileged symlink'); throw error; }
  f.output = path.join(f.directory, 'output/link/new');
  await assert.rejects(prepareDistribution(f), /ancestor/);
  assert.deepEqual(await readdir(outside), []);
});
test('host checks fail closed without signing identities before any production output', () => {
  const vars = ['LIBERTY_MAC_SIGN_IDENTITY', 'LIBERTY_MAC_TEAM_ID', 'LIBERTY_MAC_NOTARY_PROFILE', 'LIBERTY_WINDOWS_SIGN_THUMBPRINT'];
  const saved = Object.fromEntries(vars.map(key => [key, process.env[key]]));
  try { for (const key of vars) delete process.env[key]; for (const target of Object.keys(nativeTargets)) assert.throws(() => checkDistributionHost(target)); }
  finally { for (const [key, value] of Object.entries(saved)) if (value === undefined) delete process.env[key]; else process.env[key] = value; }
});
test('CLI static check is runnable and publication is rejected', () => {
  const cli = fileURLToPath(new URL('./package-native-distribution.mjs', import.meta.url));
  const success = spawnSync(process.execPath, [cli, 'check'], { encoding: 'utf8' });
  assert.equal(success.status, 0, success.stderr);
  assert.equal(JSON.parse(success.stdout).productionReady, false);
  assert.equal(spawnSync(process.execPath, [cli, 'publish'], { encoding: 'utf8' }).status, 1);
});
