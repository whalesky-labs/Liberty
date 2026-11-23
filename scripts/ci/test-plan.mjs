import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { classifyChanges, createPlan, emitPlan, fullSuites, parseNameStatus, platformSensitiveFiles, selectSuccessfulBase, successfulPushBase } from './plan.mjs';
import { assertGate } from './gate.mjs';
import { runSuite, suiteCommands, suiteEnvironment } from './run.mjs';

const head = 'a'.repeat(40), base = 'b'.repeat(40);
const fixturePlan = files => createPlan({ head, base, files });
test('documentation never selects compilers, dependency installation or packaging', () => {
  assert.deepEqual(classifyChanges(['README.zh-CN.md', 'docs/development.md', 'apps/native/README.md']).suites, ['docs']);
  assert.deepEqual(classifyChanges(['CHANGELOG.md']).suites, ['docs', 'version']);
  assert.equal(suiteCommands.docs.length, 1);
  assert.ok(suiteCommands.docs[0].includes('scripts/ci/check-docs.mjs'));
});
test('mixed changes are a union and UI edits do not require legacy or full regression', () => {
  assert.deepEqual(classifyChanges(['README.md', 'apps/native/internal/desktop/settings.go']).suites, ['docs', 'native']);
  assert.deepEqual(classifyChanges(['apps/desktop/src/App.tsx']).suites, ['docs', 'frontend']);
  assert.deepEqual(classifyChanges(['python/funasr-runner/tests/test_runner.py']).suites, ['docs', 'python']);
  const runner = classifyChanges(['python/funasr-runner/runner.py']);
  assert.deepEqual(runner.suites, ['docs', 'native', 'python', 'rust']);
});
test('persistence, ownership and lifecycle paths require their expensive regressions', () => {
  for (const file of ['apps/native/internal/host/profile.go', 'apps/native/internal/infrastructure/profiletransfer/import.go', 'apps/native/internal/infrastructure/credentials/store.go']) {
    const plan = classifyChanges([file]);
    for (const suite of ['native', 'compat', 'race']) assert.ok(plan.suites.includes(suite), `${file}: missing ${suite}`);
  }
  assert.ok(classifyChanges(['apps/native/internal/infrastructure/repositories/schema/migrations.go']).suites.includes('compat'));
});
test('platform edits execute matching hosts and preserve cross-target compilation', () => {
  assert.deepEqual(classifyChanges(['apps/native/internal/infrastructure/runner/process_windows.go']).suites, ['docs', 'matrix', 'native', 'python', 'race', 'rust', 'target-windows']);
  const mac = classifyChanges(['apps/native/internal/infrastructure/system/memory_darwin.go']);
  for (const suite of ['matrix', 'target-arm', 'target-intel']) assert.ok(mac.suites.includes(suite));
  assert.ok(!mac.suites.includes('target-windows'));
});
test('platform constraints in neutrally named files, including removed constraints, require target checks', () => {
  const goFile = 'apps/native/internal/infrastructure/profile/lock.go';
  const rustFile = 'apps/desktop/src-tauri/src/local_runtime.rs';
  const plain = 'apps/native/internal/desktop/settings.go';
  const platformFiles = platformSensitiveFiles([goFile, rustFile, plain], file => file === goFile
    ? ['//go:build darwin || linux\npackage profile', 'package profile']
    : file === rustFile ? ['', '#[cfg(target_os = "windows")]\nfn check() {}'] : ['package desktop']);
  assert.deepEqual(platformFiles, [goFile, rustFile]);
  const result = createPlan({ head, base, files: [goFile, rustFile, plain], platformFiles });
  for (const suite of ['target-arm', 'target-intel', 'target-windows', 'matrix', 'windows-rust']) assert.ok(result.suites.includes(suite));
  assert.deepEqual(classifyChanges([plain]).suites, ['docs', 'native']);
});
for (const file of ['AGENTS.md', '.github/workflows/quality.yml', 'scripts/ci/plan.mjs', 'package.json', 'pnpm-lock.yaml', 'apps/native/go.mod', 'Cargo.lock', 'apps/desktop/src-tauri/Cargo.toml', 'python/funasr-runner/requirements-lock-windows-x64.txt', 'python/funasr-runner/osv-scanner.toml', 'packages/shared-types/schemas/runner/v2/result.schema.json', 'packages/desktop-resources/runtime/runtime-manifest.json', 'packages/desktop-resources/database/schema.sql', 'unknown/new-input.md']) {
  test(`fail closed for global, shared, security or unknown input: ${file}`, () => {
    const plan = classifyChanges([file]);
    assert.equal(plan.full, true);
    assert.deepEqual(plan.suites, fullSuites);
  });
}
test('renamed and deleted files retain the previous risk classification', () => {
  const files = parseNameStatus('R100\0apps/native/internal/host/profile.go\0docs/profile.md\0D\0Cargo.lock\0A\0README.md\0');
  assert.equal(classifyChanges(files).full, true);
  assert.ok(files.includes('apps/native/internal/host/profile.go'));
  for (const text of ['M\0README.md', 'R100\0only-one-path\0', 'X\0README.md\0', 'M\0../escape\0']) assert.throws(() => parseNameStatus(text));
});
test('large diffs and unusual filenames cannot disappear behind a provider path limit', () => {
  const files = Array.from({ length: 500 }, (_, i) => `docs/change-${i}.md`);
  files.push('scripts/security change.mjs');
  assert.equal(classifyChanges(parseNameStatus(files.map(file => `M\0${file}\0`).join(''))).full, true);
  assert.equal(classifyChanges(['docs/bad\nfile.md']).full, true);
});
test('missing, failed or non-ancestor successful baselines select full checks', async () => {
  assert.equal(createPlan({ head }).full, true);
  const run = { event: 'push', status: 'completed', conclusion: 'success', head_branch: 'main', head_sha: base };
  assert.equal(selectSuccessfulBase([{ ...run, conclusion: 'failure' }, { ...run, event: 'workflow_dispatch' }, run], { head, branch: 'main', isAncestor: () => false }), null);
  assert.equal(selectSuccessfulBase([run], { head, branch: 'main', isAncestor: () => true }), base);
  await assert.rejects(successfulPushBase({ repository: 'example/repo', token: 'synthetic', head, branch: 'main', request: async () => ({ ok: false }), isAncestor: () => true }));
});
test('a docs push after cancelled code CI still includes unverified code', async context => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'liberty-ci-diff-'));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', args, { cwd: directory, encoding: 'utf8' }).trim();
  git('init', '--quiet'); git('config', 'user.name', 'Synthetic CI'); git('config', 'user.email', 'ci@example.invalid');
  await writeFile(path.join(directory, 'README.md'), 'initial\n'); git('add', '.'); git('commit', '-qm', 'initial');
  const accepted = git('rev-parse', 'HEAD');
  await writeFile(path.join(directory, 'package.json'), '{}\n'); git('add', '.'); git('commit', '-qm', 'unverified code');
  const cancelled = git('rev-parse', 'HEAD');
  await writeFile(path.join(directory, 'README.md'), 'updated\n'); git('add', '.'); git('commit', '-qm', 'docs');
  const tip = git('rev-parse', 'HEAD');
  const run = { event: 'push', status: 'completed', conclusion: 'success', head_branch: 'main' };
  const selected = selectSuccessfulBase([{ ...run, head_sha: cancelled, conclusion: 'cancelled' }, { ...run, head_sha: accepted }], { head: tip, branch: 'main', isAncestor: ancestor => { git('merge-base', '--is-ancestor', ancestor, tip); return true; } });
  const diff = execFileSync('git', ['diff', '--name-status', '-z', selected, tip], { cwd: directory, encoding: 'utf8' });
  assert.deepEqual(parseNameStatus(diff), ['README.md', 'package.json']);
  assert.equal(createPlan({ head: tip, base: selected, files: parseNameStatus(diff) }).full, true);
});
test('manual security does not claim complete validation and full cannot reduce selection', () => {
  assert.deepEqual(createPlan({ head, mode: 'security' }).suites, ['security']);
  assert.equal(createPlan({ head, base, files: ['README.md'], mode: 'full' }).full, true);
  assert.throws(() => createPlan({ head, mode: 'skip' }));
});
test('GitHub outputs bind the plan and matrix to a single commit', async context => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'liberty-ci-output-'));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const output = path.join(directory, 'output');
  const plan = fixturePlan(['README.md']);
  await emitPlan(plan, { GITHUB_OUTPUT: output });
  const lines = Object.fromEntries((await readFile(output, 'utf8')).trim().split('\n').map(line => { const i = line.indexOf('='); return [line.slice(0, i), line.slice(i + 1)]; }));
  assert.deepEqual(JSON.parse(lines.matrix), { suite: ['docs'] });
  assert.equal(JSON.parse(lines.plan).head, head);
});
test('gate rejects failure, cancellation, unexpected skip and incomplete matrices', () => {
  const scoped = fixturePlan(['README.md']);
  const full = createPlan({ head });
  assertGate(scoped, { plan: 'success', checks: 'success', full: 'skipped' }, head);
  assertGate(full, { plan: 'success', checks: 'skipped', full: 'success' }, head);
  for (const result of ['failure', 'cancelled', 'skipped', undefined]) {
    assert.throws(() => assertGate(scoped, { plan: 'success', checks: result, full: 'skipped' }, head));
    assert.throws(() => assertGate(full, { plan: 'success', checks: 'skipped', full: result }, head));
  }
  assert.throws(() => assertGate({ ...full, suites: full.suites.slice(1) }, { plan: 'success', checks: 'skipped', full: 'success' }, head));
  assert.throws(() => assertGate(scoped, { plan: 'skipped', checks: 'success', full: 'skipped' }, head));
  assert.throws(() => assertGate(scoped, { plan: 'success', checks: 'success', full: 'skipped' }, base));
});
test('suite dispatcher stops immediately on failure and rejects arbitrary commands', () => {
  const calls = [];
  assert.throws(() => runSuite('python', (...args) => { calls.push(args); return { status: 9 }; }));
  assert.equal(calls.length, 1);
  assert.throws(() => runSuite('python; echo bypass', () => ({ status: 0 })));
  assert.throws(() => suiteEnvironment('target-intel', {}, 'darwin', 'arm64'));
  assert.equal(suiteEnvironment('target-windows', {}, 'win32', 'x64').GOARCH, 'amd64');
});
