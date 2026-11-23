import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

async function fixture(context, version) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'liberty-version-'));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const files = {
    'package.json': JSON.stringify({ version, packageManager: 'pnpm@10.30.3' }),
    'apps/desktop/package.json': JSON.stringify({ version }),
    'apps/desktop/src-tauri/tauri.conf.json': JSON.stringify({ version }),
    'apps/desktop/src-tauri/Cargo.toml': `version = "${version}"\n`,
    'Cargo.lock': `[[package]]\nname = "liberty"\nversion = "${version}"\n`,
    'apps/native/internal/host/profile.go': `package host\nconst Version = "${version}"\n`,
    'CHANGELOG.md': `## ${version} - 2026-11-24\n\n### 中文\n原生候选。\n\n### English\nNative candidate.\n`,
  };
  for (const [file, contents] of Object.entries(files)) {
    await mkdir(path.dirname(path.join(directory, file)), { recursive: true });
    await writeFile(path.join(directory, file), contents);
  }
  await copyFile(new URL('./check-version-consistency.mjs', import.meta.url), path.join(directory, 'check.mjs'));
  return { directory, run: (...args) => spawnSync(process.execPath, ['check.mjs', ...args], { cwd: directory, encoding: 'utf8' }) };
}

test('Native alpha metadata and changelog retain the exact prerelease identity', async context => {
  const { run, directory } = await fixture(context, '2.0.0-alpha.1');
  assert.equal(run('--tag', 'v2.0.0-alpha.1', '--changelog-output', 'notes.md').status, 0);
  assert.equal(run('--native', '--print-version').stdout, '2.0.0-alpha.1');
  assert.notEqual(run('--tag', 'v2.0.0').status, 0);
  await writeFile(path.join(directory, 'apps/native/internal/host/profile.go'), 'const Version = "2.0.0"\n');
  assert.notEqual(run('--tag', 'v2.0.0-alpha.1').status, 0);
});

for (const version of ['1.1.24', '1.2.0-alpha.1', '1.2.0+build.1', '2.0.0-alpha.1', '2.0.0']) {
  test(`legacy publication version boundary: ${version}`, async context => {
    const { run } = await fixture(context, version);
    const result = run('--legacy-publication', '--tag', `v${version}`);
    assert.equal(result.status, version === '1.1.24' ? 0 : 1, result.stderr);
  });
}
