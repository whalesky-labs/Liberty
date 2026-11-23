import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { fullSuites } from './plan.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
const node = (...args) => [process.execPath, ...args];
const pnpm = script => ['pnpm', script];
const go = (...args) => ['go', '-C', 'apps/native', ...args];
export const suiteCommands = Object.freeze({
  docs: [node('scripts/ci/check-docs.mjs')],
  version: [node('scripts/check-version-consistency.mjs')],
  native: [pnpm('native:check'), node('scripts/check-native-security-baseline.mjs')],
  python: [pnpm('python:bootstrap'), pnpm('python:lint'), pnpm('python:test')],
  frontend: [pnpm('contracts:check'), pnpm('desktop:typecheck'), pnpm('desktop:test'), pnpm('desktop:build:web')],
  rust: [['cargo', 'fmt', '--all', '--', '--check'], ['cargo', 'test', '--workspace', '--locked'], ['cargo', 'clippy', '--workspace', '--all-targets', '--locked', '--', '-D', 'warnings']],
  policy: [pnpm('ci:check'), pnpm('native:policy:check'), pnpm('release:check'), node('scripts/check-release-policy.mjs'), node('scripts/test-release-checks.mjs')],
  compat: [pnpm('native:compat')],
  race: [go('test', '-race', '-count=1', '-timeout=30m', './...')],
  matrix: [pnpm('native:build:matrix')],
  'target-arm': [go('test', '-count=1', '-timeout=30m', './...'), go('test', '-count=1', '-tags', 'credentials_acceptance', './internal/infrastructure/credentials')],
  'target-intel': [go('test', '-count=1', '-timeout=30m', './...'), go('test', '-count=1', '-tags', 'credentials_acceptance', './internal/infrastructure/credentials')],
  'target-windows': [go('test', '-count=1', '-timeout=30m', './...'), go('test', '-count=1', '-tags', 'credentials_acceptance', './internal/infrastructure/credentials')],
  'windows-rust': [['cargo', 'check', '--workspace', '--all-targets', '--locked', '--target', 'x86_64-pc-windows-msvc']],
  'windows-release': [node('scripts/test-windows-release-checks.mjs')],
  security: [node('scripts/test-dependency-governance.mjs'), node('scripts/check-dependency-governance.mjs', 'locks'), node('scripts/check-dependency-governance.mjs', 'licenses'), node('scripts/check-dependency-governance.mjs', 'security'), node('scripts/generate-sbom.mjs'), node('scripts/generate-sbom.mjs', '--native')],
});

export function suiteEnvironment(suite, environment = process.env, platform = process.platform, arch = process.arch) {
  assert.ok(Object.hasOwn(suiteCommands, suite), 'Unknown CI suite');
  const env = { ...environment, GOTOOLCHAIN: 'go1.27.4', GOFLAGS: '-mod=readonly' };
  const targets = { 'target-arm': ['darwin', 'arm64', 'arm64'], 'target-intel': ['darwin', 'x64', 'amd64'], 'target-windows': ['win32', 'x64', 'amd64'] };
  if (targets[suite]) {
    const [os, hostArch, goarch] = targets[suite];
    assert.ok(platform === os && arch === hostArch, 'Target tests require the matching host');
    env.GOARCH = goarch;
    env.CGO_ENABLED = '0';
  }
  if (suite.startsWith('windows-')) assert.ok(platform === 'win32' && arch === 'x64', 'Windows checks require Windows x64');
  return env;
}

export function runSuite(suite, execute = spawnSync, environment = process.env) {
  const env = suiteEnvironment(suite, environment);
  for (const [command, ...args] of suiteCommands[suite]) {
    const result = execute(command, args, { cwd: root, env, stdio: 'inherit', timeout: 60 * 60_000, windowsHide: true });
    if (result.error || result.status !== 0) throw new Error(`Required ${suite} command failed: ${path.basename(command)} (status ${result.status ?? 'unavailable'})`);
  }
}

assert.deepEqual(Object.keys(suiteCommands).filter(key => key !== 'version').sort(), [...fullSuites].sort(), 'Full workflow omits a suite');
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    assert.equal(process.argv.length, 2, 'CI suites accept no skip flags');
    runSuite(process.env.LIBERTY_CI_SUITE);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
