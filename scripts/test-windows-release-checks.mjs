import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runPowerShellWithAsset } from "./lib/powershell.mjs";
import { assertMsiMetadata, compileMsi, installerProductVersion, msiQueryScript, renderWix } from "./lib/native-distribution.mjs";
import { windowsExecutableFixture, windowsIconFixture } from "./test-fixtures/native-windows.mjs";

const root = mkdtempSync(join(tmpdir(), "liberty-windows-release-checks-"));
const asset = join(root, "asset & 'quoted path'.msi");
const expected = "release-check-path-ok";

try {
  writeFileSync(asset, expected);
  const actual = runPowerShellWithAsset(
    "[System.IO.File]::ReadAllText($env:LIBERTY_RELEASE_ASSET_PATH)",
    asset,
  );
  if (actual !== expected) {
    throw new Error(`PowerShell asset path transport returned ${JSON.stringify(actual)}.`);
  }
  console.log("Windows release PowerShell transport self-test passed.");

  // Compile the actual installer template with the pinned toolchain. Synthetic
  // payload bytes are never executed or installed on the runner.
  const files = ['Liberty Native Candidate.exe', 'README.txt', 'LICENSE', 'THIRD_PARTY_NOTICES.txt', 'LOCAL_TRANSLATION_NOTICES.txt', 'icon.ico', 'native-distribution-provenance.json'];
  const target = 'windows-x64';
  const version = '2.0.0-alpha.4';
  const productVersion = installerProductVersion(version);
  mkdirSync(join(root, 'payload'));
  for (const name of files) writeFileSync(join(root, 'payload', name), name.endsWith('.exe') ? windowsExecutableFixture() : name === 'icon.ico' ? windowsIconFixture : 'synthetic installer regression fixture\n');
  const template = readFileSync(new URL('../packages/desktop-resources/distribution/windows.wxs.in', import.meta.url), 'utf8');
  writeFileSync(join(root, 'installer.wxs'), renderWix(template, { target, version, productVersion, files }));
  await compileMsi(root, 'fixture.msi');
  const metadata = JSON.parse(runPowerShellWithAsset(msiQueryScript(join(root, 'fixture.msi')), join(root, 'fixture.msi')));
  assertMsiMetadata(metadata, target, version, { productVersion });
  console.log('Windows WiX compilation and MSI metadata self-test passed.');
} finally {
  rmSync(root, { recursive: true, force: true });
}
