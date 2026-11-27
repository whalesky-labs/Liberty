import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { assertMsiMetadata, msiVersion, renderWix, upgradeCodes } from './lib/native-distribution.mjs';
import { assertInstallerManifest, installerManifest, installerName, installerProductVersion, updatesURL } from './lib/native-installers.mjs';

const sourceCommit = 'a'.repeat(40);
const version = '2.0.0-alpha.3';
const archive = Buffer.from('synthetic verified input');
const files = ['Liberty Native Candidate.exe', 'README.txt', 'LICENSE', 'THIRD_PARTY_NOTICES.txt', 'LOCAL_TRANSLATION_NOTICES.txt', 'icon.ico', 'native-distribution-provenance.json'];
function context(target = 'windows-x64') {
  const installer = Buffer.alloc(1024);
  if (target.startsWith('darwin-')) installer.write('koly', installer.length - 512);
  else Buffer.from('d0cf11e0a1b11ae1', 'hex').copy(installer);
  return { target, version, sourceCommit, archive, installer };
}
test('ordered MSI encoding preserves prerelease and stable upgrade order', () => {
  const versions = ['2.0.0-alpha.1', '2.0.0-alpha.3', '2.0.0-alpha.299', '2.0.0-beta.1', '2.0.0-beta.299', '2.0.0-rc.1', '2.0.0-rc.299', '2.0.0', '2.0.1-alpha.1', '2.0.1'];
  const encoded = versions.map(version => Number(installerProductVersion(version).split('.')[2]));
  assert.deepEqual([...encoded].sort((a, b) => a - b), encoded);
  assert.equal(new Set(encoded).size, encoded.length);
  for (const version of versions) msiVersion(installerProductVersion(version));
  assert.equal(installerProductVersion('255.255.64'), '255.255.64999');
});
for (const version of ['2.0.0-alpha.0', '2.0.0-alpha.300', '2.0.0-alpha.01', '2.0.0-preview.1', '2.0.0+build', '256.0.0', '2.256.0', '2.0.65', '2.0.0;bad']) test(`refuses ambiguous or overflowing installer version ${version}`, () => assert.throws(() => installerProductVersion(version)));
test('alpha MSI uses explicit numeric ProductVersion without weakening signed distribution defaults', async () => {
  const template = await readFile(new URL('../packages/desktop-resources/distribution/windows.wxs.in', import.meta.url), 'utf8');
  assert.throws(() => renderWix(template, { target: 'windows-x64', version, files }));
  const productVersion = installerProductVersion(version);
  const result = renderWix(template, { target: 'windows-x64', version, productVersion, files });
  assert.ok(result.includes(`Version="${productVersion}"`));
  assert.match(result, /InstallScope="perUser"/);
  assert.match(result, /MajorUpgrade Schedule="afterInstallInitialize"/);
  const metadata = { properties: { ProductName: 'Liberty Native Candidate (windows-x64)', ProductVersion: productVersion, UpgradeCode: `{${upgradeCodes['windows-x64']}}`, ProductCode: '{AABBCCDD-1111-2222-3333-444455556666}' }, template: 'x64;1033', files };
  assertMsiMetadata(metadata, 'windows-x64', version, { productVersion });
  assert.throws(() => assertMsiMetadata(metadata, 'windows-x64', version));
});
for (const target of ['darwin-aarch64', 'darwin-x64', 'windows-x64']) {
  test(`installer manifest binds all source and artifact bytes: ${target}`, () => {
    const c = context(target);
    const manifest = installerManifest(c);
    assertInstallerManifest(manifest, c);
    assert.equal(manifest.signed, false);
    assert.equal(manifest.signature, target.startsWith('darwin-') ? 'adhoc' : 'none');
    assert.equal(manifest.updatesURL, updatesURL);
    for (const mutate of [m => { m.signed = true; }, m => { m.notarized = true; }, m => { m.productionReady = true; }, m => { m.signature = 'trusted'; }, m => { m.sourceCommit = 'b'.repeat(40); }, m => { m.inputArchiveSha256 = 'c'.repeat(64); }, m => { m.installer.name = '../bad'; }, m => { m.installer.sha256 = 'd'.repeat(64); }, m => { m.updatesURL = 'https://untrusted.invalid'; }, m => { m.productVersion = '1.0.0'; }, m => { m.extra = true; }]) {
      const changed = structuredClone(manifest);
      mutate(changed);
      assert.throws(() => assertInstallerManifest(changed, c));
    }
    const invalid = { ...c, installer: Buffer.from('not an installer') };
    assert.throws(() => assertInstallerManifest(installerManifest(invalid), invalid));
  });
}
test('asset names reject unsafe target/version input', () => {
  assert.throws(() => installerName('../escape', 'windows-x64'));
  assert.throws(() => installerName(version, '../windows-x64'));
});
