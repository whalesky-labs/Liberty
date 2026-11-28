import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import { finished } from 'node:stream/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gunzipSync } from 'node:zlib';

const root = fileURLToPath(new URL('../', import.meta.url));
const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const trees = ['apps/native', 'packages/desktop-resources', 'packages/shared-types', 'python/funasr-runner', 'benchmarks/asr', 'scripts', 'config', '.github/workflows'];
const rootFiles = ['package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', '.npmrc', '.gitignore', '.gitattributes', 'LICENSE', 'CHANGELOG.md'];
const harnessFiles = ['scripts/check-native-source-isolation.mjs', 'scripts/test-native-source-isolation.mjs'];
const beneath = (name, parent) => name === parent || name.startsWith(`${parent}/`);
const safePath = name => typeof name === 'string' && name.length > 0 && !/[\\:\x00-\x1f]/.test(name)
  && !name.startsWith('/') && name.split('/').every(part => part && part !== '.' && part !== '..');

export function selectNativeSourceFiles(files) {
  assert.ok(Array.isArray(files) && files.every(safePath), 'Invalid source inventory path');
  assert.equal(new Set(files).size, files.length, 'Duplicate source inventory path');
  return files.filter(name => name !== '.github/workflows/build-desktop.yml'
    && (rootFiles.includes(name) || trees.some(tree => beneath(name, tree)))).sort();
}

const presentation = 'apps/native/internal/desktop/presentation';
export const nativeGeneration = Object.freeze([
  { script: 'generate-native-contracts.mjs', outputs: ['apps/native/internal/contracts/schemas_generated.go'] },
  { script: 'generate-native-runtime.mjs', outputs: ['apps/native/internal/infrastructure/managed/resources_generated.go'] },
  { script: 'generate-native-migrations.mjs', outputs: ['apps/native/internal/infrastructure/repositories/schema/migrations_generated.go'] },
  { script: 'generate-native-export.mjs', outputs: ['apps/native/internal/infrastructure/minutesdocx/meeting-minutes.docx'] },
  { script: 'generate-native-presentation.mjs', outputs: ['resources_generated.go', 'sidebar-mascot.webp', 'application-icon.png', 'bloub_frames_generated.json.gz'].map(name => `${presentation}/${name}`) },
  { script: 'generate-native-work-art.mjs', outputs: [`${presentation}/work_art_generated.go`, `${presentation}/work-art`] },
  { script: 'generate-native-pet-art.mjs', outputs: [`${presentation}/pet_art_generated.go`, `${presentation}/pet-art`] },
  { script: 'generate-native-pet.mjs', outputs: ['apps/native/internal/domain/pet_generated.go'] },
  { script: 'generate-native-redemption.mjs', outputs: ['apps/native/internal/domain/pet_redemption_key_generated.go'] },
]);
const generated = name => nativeGeneration.some(({ outputs }) => outputs.some(output => beneath(name, output)));

async function regularSource(directory, name) {
  let current = directory;
  const parts = name.split('/');
  for (const [index, part] of parts.entries()) {
    current = path.join(current, part);
    const info = await lstat(current);
    assert.ok(!info.isSymbolicLink() && (index === parts.length - 1 ? info.isFile() : info.isDirectory()), `Non-regular source: ${name}`);
  }
  return current;
}

export function sourceFileRecord(name, bytes) {
  // gzip implementations may emit different compressed bytes for identical frames.
  const payload = name === `${presentation}/bloub_frames_generated.json.gz`
    ? gunzipSync(bytes, { maxOutputLength: 16 * 1024 * 1024 }) : bytes;
  return { path: name, bytes: bytes.length, sha256: digest(bytes), contentSha256: digest(payload) };
}

export async function copyNativeSources(source, destination, files) {
  source = await realpath(source);
  const destinationInfo = await lstat(destination);
  assert.ok(destinationInfo.isDirectory() && !destinationInfo.isSymbolicLink(), 'Snapshot must be a regular directory');
  destination = await realpath(destination);
  assert.ok(destination !== source && !source.startsWith(destination + path.sep), 'Snapshot must not contain the source');
  assert.equal((await readdir(destination)).length, 0, 'Snapshot must be empty');
  const selected = selectNativeSourceFiles(files);
  // Validate the full inventory before copying. Never follow source links into
  // a local profile, dependency tree or an untracked private file.
  const inputs = [];
  for (const name of selected) inputs.push([name, await regularSource(source, name)]);
  const records = [];
  for (const [name, input] of inputs) {
    const bytes = await readFile(input);
    const output = path.join(destination, name);
    await mkdir(path.dirname(output), { recursive: true });
    await writeFile(output, bytes, { flag: 'wx' });
    await chmod(output, (await lstat(input)).mode & 0o777);
    records.push(sourceFileRecord(name, bytes));
  }
  return records;
}

export async function assertNativeSourceSnapshot(directory, records, { regenerated = false } = {}) {
  for (const forbidden of ['apps/desktop', 'Cargo.toml', 'Cargo.lock', 'rust-toolchain.toml', 'deny.toml', '.github/workflows/build-desktop.yml']) {
    await assert.rejects(lstat(path.join(directory, forbidden)), { code: 'ENOENT' }, `Legacy source reappeared: ${forbidden}`);
  }
  for (const record of records) {
    const file = await regularSource(directory, record.path);
    const actual = sourceFileRecord(record.path, await readFile(file));
    assert.equal(actual[regenerated && generated(record.path) ? 'contentSha256' : 'sha256'],
      record[regenerated && generated(record.path) ? 'contentSha256' : 'sha256'], `Source or regenerated content changed: ${record.path}`);
  }
}

export async function removeNativeGeneratedFiles(directory) {
  for (const { outputs } of nativeGeneration) {
    for (const output of outputs) await rm(path.join(directory, output), { recursive: true, force: true });
  }
}

export function isolationEnvironment(environment = process.env) {
  const excluded = new Set(['NODE_PATH', 'NODE_OPTIONS', 'PYTHONPATH', 'PYTHONHOME']);
  const clean = Object.fromEntries(Object.entries(environment).filter(([key]) => !key.startsWith('LIBERTY_') && !key.startsWith('GIT_') && !excluded.has(key)));
  return { ...clean, GOTOOLCHAIN: 'go1.27.4', GOFLAGS: '-mod=readonly', GOWORK: 'off' };
}

export async function checkNativeSourceIsolation() {
  const pnpm = process.env.npm_execpath;
  assert.ok(pnpm && path.isAbsolute(pnpm), 'Run through pnpm native:source:check');
  const env = isolationEnvironment();
  const git = (...args) => execFileSync('git', args, { cwd: root, env, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  const sourceCommit = git('rev-parse', 'HEAD').trim();
  const tracked = git('ls-files', '-z').split('\0').filter(Boolean);
  const files = [...new Set([...tracked, ...harnessFiles])];
  const worktreeChanges = git('status', '--porcelain=v1', '--untracked-files=all', '--', ...trees, ...rootFiles).trim().length > 0;
  const parent = path.join(root, 'target/native');
  await mkdir(parent, { recursive: true });
  const evidence = await mkdtemp(path.join(parent, 'source-isolation-'));
  // Keep the copy outside the repository so Node cannot fall back to the
  // original checkout's node_modules through ancestor module resolution.
  const source = await realpath(await mkdtemp(path.join(os.tmpdir(), 'liberty-native-source-')));
  const original = await realpath(root);
  assert.ok(source !== original && !source.startsWith(original + path.sep), 'Isolated source must be outside the repository');
  const report = { format: 1, sourceCommit, worktreeChanges, platform: process.platform, arch: process.arch,
    startedAt: new Date().toISOString(), status: 'running', productionReady: false, checks: [] };
  const save = () => writeFile(path.join(evidence, 'result.json'), JSON.stringify(report, null, 2) + '\n');
  const run = async (name, command, args) => {
    console.log(`Native source isolation: ${name}`);
    const entry = { name, status: 'running' };
    report.checks.push(entry);
    await save();
    const log = createWriteStream(path.join(evidence, `${name}.log`), { flags: 'wx' });
    const started = Date.now();
    try {
      await new Promise((resolve, reject) => {
        const child = spawn(command, args, { cwd: source, env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
        child.stdout.pipe(log, { end: false });
        child.stderr.pipe(log, { end: false });
        log.once('error', error => { child.kill(); reject(error); });
        child.once('error', reject);
        child.once('close', (code, signal) => code === 0 ? resolve() : reject(new Error(`${name} failed (${signal ?? code}); inspect its isolated log`)));
      });
      entry.status = 'passed';
    } catch (error) {
      entry.status = 'failed';
      throw error;
    } finally {
      log.end();
      await finished(log);
      entry.durationMs = Date.now() - started;
      await save();
    }
  };
  const pnpmRun = (name, args) => run(name, process.execPath, [pnpm, ...args]);
  try {
    const records = await copyNativeSources(root, source, files);
    assert.equal(git('rev-parse', 'HEAD').trim(), sourceCommit, 'Source HEAD changed during snapshot');
    await assertNativeSourceSnapshot(source, records);
    const inventory = JSON.stringify(records, null, 2) + '\n';
    report.sourceInventorySha256 = digest(inventory);
    report.sourceFileCount = records.length;
    await writeFile(path.join(evidence, 'source-inventory.json'), inventory);
    // ASR blocking self-tests need a Git identity. Give the disposable copy its
    // own empty synthetic repository, never the real repository's .git/history.
    await run('snapshot-git-init', 'git', ['init', '--quiet']);
    await run('snapshot-git-identity', 'git', ['-c', 'user.name=Native Isolation Fixture', '-c', 'user.email=isolation@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '--quiet', '--allow-empty', '-m', 'Synthetic source-isolation test identity']);
    await pnpmRun('install', ['install', '--frozen-lockfile']);
    await pnpmRun('python-bootstrap', ['python:bootstrap']);
    await removeNativeGeneratedFiles(source);
    for (const { script } of nativeGeneration) await run(script.replace('.mjs', ''), process.execPath, [`scripts/${script}`]);
    await assertNativeSourceSnapshot(source, records, { regenerated: true });
    await pnpmRun('native-verify', ['native:verify']);
    await pnpmRun('native-compat', ['native:compat']);
    await pnpmRun('native-build-matrix', ['native:build:matrix']);
    await assertNativeSourceSnapshot(source, records, { regenerated: true });
    report.status = 'passed';
    console.log('Native sources regenerated, verified, replayed compatibility and compiled all targets without apps/desktop or Cargo. Device, runtime and production cutover acceptance remain separate.');
  } catch (error) {
    report.status = 'failed';
    throw error;
  } finally {
    report.finishedAt = new Date().toISOString();
    await save();
    console.log(`Native source isolation evidence: ${evidence}`);
    if (report.status === 'passed') await rm(source, { recursive: true, force: true });
    else console.log(`Failed isolated source retained: ${source}`);
  }
  return report;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  assert.equal(process.argv.length, 2, 'Native source isolation accepts no skip flags');
  await checkNativeSourceIsolation();
}
