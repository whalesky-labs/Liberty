import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { assertSecurityInstaller, assertWorkflow, checkWorkflows, renderYaml, securityInstaller, workflows } from './workflows.mjs';
import { fullSuites } from './plan.mjs';
import { suiteCommands } from './run.mjs';

test('actual workflows match reviewed generated event and gate policy', async () => {
  await checkWorkflows();
  for (const [name, definition] of Object.entries(workflows)) assert.deepEqual(assertWorkflow(name, renderYaml(definition)), definition);
});
test('scanner authentication cannot drift or suppress download failures', () => {
  assertSecurityInstaller(securityInstaller);
  for (const [before, after] of [
    ['sha256sum --check', 'true'],
    ['sha256sum --check', 'sha256sum --check || true'],
    ['edcfc41d257db36148f065055655fe3fcfc434b0b423ea67468a84c207524e0c', '0'.repeat(64)],
    ['download/v2.5.0/', 'download/latest/'],
    ['set -euo pipefail', 'set +e'],
  ]) assert.throws(() => assertSecurityInstaller(securityInstaller.replace(before, after)));
});
test('ordinary CI selects suites without building candidate archives', () => {
  const quality = workflows['quality.yml'];
  assert.deepEqual(Object.keys(quality.on).sort(), ['pull_request', 'push', 'workflow_dispatch']);
  assert.equal(quality.concurrency['cancel-in-progress'], true);
  assert.equal(quality.jobs.checks.uses, './.github/workflows/ci-suite.yml');
  assert.equal(quality.jobs.full.uses, './.github/workflows/ci-full.yml');
  assert.ok(!JSON.stringify(quality).includes('build-native.yml'));
  assert.ok(!JSON.stringify(suiteCommands).includes('package-native.mjs'));
});
test('race and cross-compilation caches cannot be shadowed by the ordinary Go cache', () => {
  const steps = workflows['ci-suite.yml'].jobs.run.steps;
  const cache = steps.find(step => step.name === 'Go race and cross-compilation cache');
  assert.match(cache.if, /"race","matrix"/);
  assert.match(cache.with.key, /inputs.suite/);
  assert.match(cache.with.key, /github.sha/);
  assert.ok(cache.with['restore-keys'].endsWith('-'));
  assert.ok(steps.indexOf(cache) < steps.findIndex(step => step.name === 'Execute required suite'));
});
test('complete gate retains all suites, target vault execution and shared scanner coverage', async () => {
  assert.deepEqual(workflows['ci-full.yml'].jobs.checks.strategy.matrix.suite, fullSuites);
  for (const target of ['target-arm', 'target-intel', 'target-windows']) assert.ok(suiteCommands[target].some(args => args.includes('credentials_acceptance')));
  const scanner = await readFile(new URL('../check-dependency-governance.mjs', import.meta.url), 'utf8');
  assert.match(scanner, /const locks = \[\.\.\.OSV_LOCK_FILES\.filter\([^\n]+PYTHON_LOCK_FILES\[0\]\]/);
  assert.ok(suiteCommands.security.some(args => args.includes('--native')));
});
test('security-only manual result cannot satisfy the required code gate', () => {
  assert.match(workflows['quality.yml'].jobs.gate.name, /security-gate/);
  assert.match(workflows['quality.yml'].jobs.gate.name, /inputs.mode == 'security'/);
});
test('signed/public release entry requires complete validation and remains uncancellable', async () => {
  const source = await readFile(new URL('../../.github/workflows/build-desktop.yml', import.meta.url), 'utf8');
  assert.match(source, /quality:\s*\n\s+name: Quality Gate\s*\n\s+uses: \.\/\.github\/workflows\/ci-full.yml/);
  assert.match(source, /cancel-in-progress: false/);
  assert.match(source, /if: needs.prepare.outputs.should_publish == 'true'/);
  assert.doesNotMatch(source, /^\s*(schedule|pull_request_target):/m);
});

const mutations = [
  ['quality.yml', 'scheduled full runs', value => { value.on.schedule = [{ cron: '0 0 * * *' }]; }],
  ['quality.yml', 'privileged pull requests', value => { value.on.pull_request_target = null; }],
  ['quality.yml', 'top-level path filtering', value => { value.on.push.paths = ['apps/**']; }],
  ['quality.yml', 'untrusted planner', value => { value.jobs.plan.steps.at(-1).run = 'node scripts/ci/plan.mjs'; }],
  ['quality.yml', 'hidden cancellation', value => { value.jobs.gate.if = '${{ success() }}'; }],
  ['quality.yml', 'missing full dependency', value => { value.jobs.gate.needs.pop(); }],
  ['quality.yml', 'required gate without assertion', value => { value.jobs.gate.steps.at(-1).run = 'echo success'; }],
  ['quality.yml', 'duplicate push validation', value => { value.on.push.branches = ['**']; }],
  ['quality.yml', 'extra token privilege', value => { value.permissions['id-token'] = 'write'; }],
  ['ci-full.yml', 'missing security', value => { value.jobs.checks.strategy.matrix.suite = fullSuites.filter(suite => suite !== 'security'); }],
  ['ci-full.yml', 'missing target', value => { value.jobs.checks.strategy.matrix.suite = fullSuites.filter(suite => suite !== 'target-intel'); }],
  ['ci-full.yml', 'failures ignored', value => { value.jobs.checks['continue-on-error'] = true; }],
  ['ci-full.yml', 'skipped accepted as success', value => { value.jobs.gate.steps[0].run = 'test "$CHECKS_RESULT" != failure'; }],
  ['ci-suite.yml', 'floating checkout', value => { value.jobs.run.steps[0].uses = 'actions/checkout@v6'; }],
  ['ci-suite.yml', 'wrong pinned checkout', value => { value.jobs.run.steps[0].uses = 'actions/checkout@' + 'f'.repeat(40); }],
  ['ci-suite.yml', 'mutable checkout', value => { value.jobs.run.steps[0].with.ref = 'main'; }],
  ['ci-suite.yml', 'credential persistence', value => { value.jobs.run.steps[0].with['persist-credentials'] = true; }],
  ['ci-suite.yml', 'wrong Go', value => { value.jobs.run.steps.find(step => step.name === 'Setup Go').with['go-version'] = '1.27.3'; }],
  ['ci-suite.yml', 'unpinned package manager', value => { const step = value.jobs.run.steps.find(step => step.name === 'Pinned package manager'); step.run = step.run.replace('pnpm@10.30.3', 'pnpm@latest'); }],
  ['ci-suite.yml', 'unfrozen installation', value => { value.jobs.run.steps.find(step => step.name === 'Install root tooling').run = 'pnpm install'; }],
  ['ci-suite.yml', 'missing scanner verification', value => { value.jobs.run.steps.find(step => step.name === 'Install authenticated security scanners').run = 'echo installed'; }],
  ['ci-suite.yml', 'suppressed suite failure', value => { value.jobs.run.steps.find(step => step.name === 'Execute required suite').run += ' || true'; }],
  ['ci-suite.yml', 'missing artifact accepted', value => { value.jobs.run.steps.at(-1).with['if-no-files-found'] = 'ignore'; }],
  ['build-native.yml', 'selected gate used for delivery', value => { value.jobs.quality.uses = './.github/workflows/quality.yml'; }],
  ['build-native.yml', 'build without full gate', value => { value.jobs.build.needs = ['select']; }],
  ['build-native.yml', 'unreviewed publication job', value => { value.jobs.publish = { 'runs-on': 'ubuntu-latest', steps: [{ run: 'gh release create v1.2.3' }] }; }],
];
for (const [name, label, change] of mutations) test(`rejects ${label}`, () => {
  const value = structuredClone(workflows[name]); change(value);
  assert.throws(() => assertWorkflow(name, renderYaml(value)));
});
