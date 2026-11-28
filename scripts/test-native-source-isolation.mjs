import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { gzipSync } from 'node:zlib';
import { assertNativeSourceSnapshot, copyNativeSources, isolationEnvironment, nativeGeneration, removeNativeGeneratedFiles, selectNativeSourceFiles, sourceFileRecord } from './check-native-source-isolation.mjs';

async function fixture(context) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'native-source-test-'));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, 'original');
  const destination = path.join(directory, 'snapshot');
  await mkdir(source);
  await mkdir(destination);
  const put = async (name, text = 'synthetic fixture') => {
    await mkdir(path.dirname(path.join(source, name)), { recursive: true });
    await writeFile(path.join(source, name), text);
    return name;
  };
  return { directory, source, destination, put };
}

test('isolated inventory excludes the old GUI, Cargo, profiles and dependency caches', async context => {
  const f = await fixture(context);
  const kept = ['package.json', 'pnpm-lock.yaml', 'apps/native/go.mod', 'packages/desktop-resources/database/v01-baseline.sql', 'benchmarks/asr/thresholds.json', 'scripts/run-native.mjs'];
  const excluded = ['apps/desktop/package.json', 'Cargo.lock', 'Cargo.toml', 'rust-toolchain.toml', 'deny.toml', '.github/workflows/build-desktop.yml', '.git/config', 'node_modules/private.txt', 'target/local.db', '.liberty-secrets/key'];
  for (const name of [...kept, ...excluded]) await f.put(name);
  await f.put('apps/native/untracked-private.txt');
  assert.deepEqual(selectNativeSourceFiles([...kept, ...excluded]), [...kept].sort());
  const records = await copyNativeSources(f.source, f.destination, [...kept, ...excluded]);
  await assertNativeSourceSnapshot(f.destination, records);
  assert.equal(records.length, kept.length);
  await assert.rejects(readFile(path.join(f.destination, 'apps/native/untracked-private.txt')), { code: 'ENOENT' });
  await writeFile(path.join(f.destination, 'Cargo.lock'), 'unexpected');
  await assert.rejects(assertNativeSourceSnapshot(f.destination, records), /Legacy source reappeared/);
  await rm(path.join(f.destination, 'Cargo.lock'));
  await writeFile(path.join(f.destination, kept[0]), 'changed');
  await assert.rejects(assertNativeSourceSnapshot(f.destination, records), /content changed/);
});

test('snapshot refuses unsafe names, duplicates and a populated destination', async context => {
  for (const name of ['../outside', '/absolute', 'apps/native/../../secret', 'apps\\native\\secret', 'apps/native/a:b', 'apps/native/\0']) {
    assert.throws(() => selectNativeSourceFiles([name]), /Invalid source inventory/);
  }
  assert.throws(() => selectNativeSourceFiles(['package.json', 'package.json']), /Duplicate/);
  const f = await fixture(context);
  await writeFile(path.join(f.destination, 'keep'), 'untouched');
  await assert.rejects(copyNativeSources(f.source, f.destination, []), /must be empty/);
  assert.equal(await readFile(path.join(f.destination, 'keep'), 'utf8'), 'untouched');
});

test('source symlinks and linked parent directories fail before copying any input', async context => {
  const f = await fixture(context);
  await f.put('package.json');
  const outside = path.join(f.directory, 'outside');
  await mkdir(outside);
  await writeFile(path.join(outside, 'private.txt'), 'never copied');
  await mkdir(path.join(f.source, 'apps'));
  try { await symlink(outside, path.join(f.source, 'apps/native'), 'junction'); }
  catch (error) { if (error.code === 'EPERM') return context.skip('Host refuses test symlinks'); throw error; }
  await assert.rejects(copyNativeSources(f.source, f.destination, ['package.json', 'apps/native/private.txt']), /Non-regular source/);
  assert.deepEqual(await readdir(f.destination), []);
});

test('regeneration cannot pass with a missing, stale or corrupt embedded output', async context => {
  const f = await fixture(context);
  const names = nativeGeneration.flatMap(({ outputs }) => outputs).filter(name => name.endsWith('.go'));
  for (const name of names) await f.put(name);
  const compressed = 'apps/native/internal/desktop/presentation/bloub_frames_generated.json.gz';
  const frames = Buffer.from('{"frames":[1,2,3]}');
  await f.put(compressed, gzipSync(frames, { level: 1 }));
  const records = await copyNativeSources(f.source, f.destination, [...names, compressed]);
  await removeNativeGeneratedFiles(f.destination);
  await assert.rejects(assertNativeSourceSnapshot(f.destination, records, { regenerated: true }), { code: 'ENOENT' });
  for (const name of names) await writeFile(path.join(f.destination, name), 'synthetic fixture');
  await writeFile(path.join(f.destination, compressed), gzipSync(frames, { level: 9 }));
  await assertNativeSourceSnapshot(f.destination, records, { regenerated: true });
  await writeFile(path.join(f.destination, compressed), gzipSync(Buffer.from('{"frames":[4]}')));
  await assert.rejects(assertNativeSourceSnapshot(f.destination, records, { regenerated: true }), /content changed/);
  assert.throws(() => sourceFileRecord(compressed, Buffer.from('broken gzip')));
  assert.equal(await readFile(path.join(f.source, names[0]), 'utf8'), 'synthetic fixture');
});

test('isolation cannot inherit profile selectors or an external Git/Go workspace', () => {
  const env = isolationEnvironment({ PATH: '/tools', LIBERTY_NATIVE_FIXTURE_DIR: '/private', LIBERTY_DEV_PYTHON: '/private/python', GIT_DIR: '/private/.git', NODE_PATH: '/private/node_modules', NODE_OPTIONS: '--import=/private/hook.mjs', PYTHONPATH: '/private/python', GOWORK: '/external/go.work', GOFLAGS: '-mod=mod', GOTOOLCHAIN: 'auto' });
  assert.deepEqual(env, { PATH: '/tools', GOWORK: 'off', GOFLAGS: '-mod=readonly', GOTOOLCHAIN: 'go1.27.4' });
});
