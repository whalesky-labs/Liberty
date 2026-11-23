// Standalone by design: the workflow executes this file from its trusted base.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFile, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const fullSuites = Object.freeze(['docs', 'native', 'python', 'frontend', 'rust', 'policy', 'compat', 'race', 'matrix', 'target-arm', 'target-intel', 'target-windows', 'windows-rust', 'windows-release', 'security']);
export const planVersion = 1;
const sha = /^[a-f0-9]{40}$/;
const allowedPath = value => typeof value === 'string' && value.length > 0 && !/[\x00-\x1f\\]/.test(value) && !value.startsWith('/') && value.split('/').every(part => part && part !== '.' && part !== '..');
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }).trim();

export function classifyChanges(files, { forceFull = false, platformFiles = [] } = {}) {
  const suites = new Set(['docs']);
  const reasons = new Set();
  let full = forceFull;
  const add = (reason, ...jobs) => { reasons.add(reason); jobs.forEach(job => suites.add(job)); };
  const platforms = () => add('platform-boundary', 'matrix', 'target-arm', 'target-intel', 'target-windows');
  if (forceFull) reasons.add('explicit-full-or-unavailable-baseline');
  if (!Array.isArray(files) || files.some(file => !allowedPath(file))) return { full: true, suites: [...fullSuites], reasons: ['invalid-change-inventory'] };
  for (const file of new Set(files)) {
    if (/^(?:README(?:\.[A-Za-z-]+)?\.md|CHANGELOG\.md|docs\/.*\.md)$/.test(file) || /^(?:apps|packages|python|benchmarks)\/.*\/README\.md$/.test(file)) {
      if (file === 'CHANGELOG.md') add('changelog', 'version');
      else reasons.add('documentation');
      continue;
    }
    if (/^(?:\.github\/|scripts\/|config\/|AGENTS\.md$|package\.json$|pnpm-|Cargo\.|rust-toolchain|deny\.toml$|\.npmrc$|\.gitattributes$|\.gitignore$|LICENSE$)/.test(file)
      || /(?:^|\/)(?:go\.(?:mod|sum)|Cargo\.(?:toml|lock)|package\.json|requirements[^/]*\.txt|pyproject\.toml|osv-scanner\.toml)$/.test(file)
      || /^python\/funasr-runner\/(?:tools|vendor)\//.test(file)
      || /^packages\/desktop-resources\/(?:runtime|distribution|redemption)\//.test(file)) {
      full = true; reasons.add('build-release-dependency-or-security-policy'); continue;
    }
    if (/^apps\/native\/.*\.go$/.test(file)) {
      add('native-source', 'native');
      if (/\/(?:client|application|host|runner|managed|profile|profiletransfer|credentials)\//.test(file)) add('native-lifecycle', 'race');
      if (/\/(?:repositories|legacyjobs|profile|profiletransfer|credentials|domain|host)\//.test(file)) add('persistent-or-domain-contract', 'compat');
      if (/_windows(?:_test)?\.go$/.test(file)) add('windows-platform', 'matrix', 'target-windows');
      else if (/_darwin(?:_test)?\.go$/.test(file)) add('macos-platform', 'matrix', 'target-arm', 'target-intel');
      else if (/\/(?:credentials|system)\//.test(file)) platforms();
      if (platformFiles.includes(file) || /_(?:arm64|amd64|unix|other)(?:_test)?\.go$/.test(file)) platforms();
      if (/\/runner\//.test(file)) add('runner-consumers', 'python', 'rust');
      if (/\/contracts\//.test(file)) { full = true; reasons.add('shared-contract'); }
      continue;
    }
    if (/^apps\/desktop\/src-tauri\//.test(file)) {
      add('rust-source', 'rust');
      if (/\/(?:local_db|domain)\/|schema|capabilities|permissions|tauri\.conf|build\.rs/.test(file)) { full = true; reasons.add('production-data-or-permission-boundary'); }
      if (/windows|Cargo\.toml/.test(file) || platformFiles.includes(file)) add('windows-rust', 'windows-rust');
      continue;
    }
    if (/^apps\/desktop\/(?:src|public)\//.test(file) || /^apps\/desktop\/(?:index\.html|vite\.|tsconfig)/.test(file)) {
      add('frontend', 'frontend'); continue;
    }
    if (/^python\/funasr-runner\/tests\//.test(file)) { add('python-tests', 'python'); continue; }
    if (/^python\/funasr-runner\/[^/]+\.py$/.test(file)) { add('shared-runner', 'python', 'native', 'rust'); continue; }
    if (/^packages\/shared-types\/|^packages\/desktop-resources\/(?:database|fixtures)\//.test(file)) {
      full = true; reasons.add('shared-data-contract'); continue;
    }
    if (/^packages\/desktop-resources\/(?:i18n|presentation|pet|images|templates)\//.test(file)) {
      add('shared-desktop-resources', 'native', 'frontend', 'rust');
      if (/\/pet\/.*\.json$/.test(file)) add('pet-economy', 'compat');
      continue;
    }
    full = true; reasons.add('unclassified-path');
  }
  return { full, suites: full ? [...fullSuites] : [...suites].sort(), reasons: [...reasons].sort() };
}

export function parseNameStatus(bytes) {
  if (!bytes) return [];
  const fields = bytes.split('\0');
  if (fields.pop() !== '') throw new Error('Incomplete Git diff');
  const files = [];
  while (fields.length) {
    const status = fields.shift();
    if (!/^(?:[AMDUT]|[RC]\d{1,3})$/.test(status)) throw new Error('Unknown Git diff status');
    const count = /^[RC]/.test(status) ? 2 : 1;
    for (let i = 0; i < count; i++) {
      const name = fields.shift();
      if (!allowedPath(name)) throw new Error('Unsafe or incomplete Git path');
      files.push(name);
    }
  }
  return [...new Set(files)].sort();
}

export function selectSuccessfulBase(runs, { head, branch, isAncestor }) {
  for (const run of runs) {
    if (run.event === 'push' && run.status === 'completed' && run.conclusion === 'success'
      && run.head_branch === branch && sha.test(run.head_sha) && run.head_sha !== head && isAncestor(run.head_sha, head)) return run.head_sha;
  }
  return null;
}

export async function successfulPushBase({ repository, token, head, branch, request = fetch, isAncestor }) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository ?? '') || !token) return null;
  for (let page = 1; page <= 5; page++) {
    const response = await request(`https://api.github.com/repos/${repository}/actions/workflows/quality.yml/runs?branch=${encodeURIComponent(branch)}&event=push&status=success&per_page=100&page=${page}`, {
      headers: { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' }, signal: AbortSignal.timeout(15_000),
    });
    if (!response.ok) throw new Error('Successful CI baseline lookup failed');
    const data = await response.json();
    if (!Array.isArray(data.workflow_runs)) throw new Error('Invalid successful CI inventory');
    const base = selectSuccessfulBase(data.workflow_runs, { head, branch, isAncestor });
    if (base) return base;
    if (data.workflow_runs.length < 100) return null;
  }
  return null;
}

// Mixed-platform files can have neutral names. Inspect both revisions so
// removing a build constraint cannot silently remove platform validation.
export function platformSensitiveFiles(files, readVersions) {
  return files.filter(file => /\.(?:go|rs)$/.test(file)).filter(file => readVersions(file).some(source =>
    file.endsWith('.go') ? /^\/\/(?:go:build|\s+\+build)[^\n]*(?:darwin|windows|linux|amd64|arm64|unix)\b/m.test(source)
      : /\b(?:cfg|cfg_attr)!?\s*\([^)]*(?:windows|target_os|target_arch|target_family|unix)\b/s.test(source)));
}

export function createPlan({ head, base = null, files = [], platformFiles = [], forceFull = false, mode = 'auto', reason }) {
  if (!sha.test(head) || (base !== null && !sha.test(base)) || !['auto', 'full', 'security'].includes(mode)) throw new Error('Invalid CI plan identity');
  const selection = mode === 'security' ? { full: false, suites: ['security'], reasons: ['manual-security-only'] }
    : classifyChanges(files, { forceFull: forceFull || mode === 'full' || !base, platformFiles });
  if (reason) selection.reasons.push(reason);
  return { format: planVersion, head, base, mode, ...selection, changedFiles: files.length, diffSha256: createHash('sha256').update(JSON.stringify(files)).digest('hex') };
}

export async function emitPlan(plan, env = process.env) {
  if (env.GITHUB_OUTPUT) await appendFile(env.GITHUB_OUTPUT, `plan=${JSON.stringify(plan)}\nfull=${plan.full}\nmatrix=${JSON.stringify({ suite: plan.full ? ['docs'] : plan.suites })}\n`);
  if (env.GITHUB_STEP_SUMMARY) await appendFile(env.GITHUB_STEP_SUMMARY, `### CI selection\n\nCommit: \`${plan.head}\`\n\nBaseline: \`${plan.base ?? 'unavailable — full validation'}\`\n\nRequired suites: ${plan.suites.map(s => `\`${s}\``).join(', ')}\n\nReasons: ${plan.reasons.join(', ')}\n\nChanged paths: ${plan.changedFiles}. No scheduled runs or candidate packaging.\n`);
  console.log(JSON.stringify(plan, null, 2));
}

export async function main(env = process.env) {
  const head = git('rev-parse', 'HEAD');
  if (head !== env.GITHUB_SHA || !sha.test(head)) throw new Error('Checkout differs from event commit');
  const event = JSON.parse(await readFile(env.GITHUB_EVENT_PATH, 'utf8'));
  const mode = env.GITHUB_EVENT_NAME === 'workflow_dispatch' ? (event.inputs?.mode ?? 'full') : 'auto';
  let base = null, files = [], platformFiles = [], reason;
  try {
    if (env.GITHUB_EVENT_NAME === 'pull_request') {
      if (!sha.test(event.pull_request?.base?.sha) || !sha.test(event.pull_request?.head?.sha)) throw new Error('Missing PR identity');
      // Comparing with the complete merge result also covers base-branch changes.
      base = git('merge-base', event.pull_request.base.sha, event.pull_request.head.sha);
    } else if (env.GITHUB_EVENT_NAME === 'push' && env.GITHUB_REF === 'refs/heads/main') {
      base = await successfulPushBase({ repository: env.GITHUB_REPOSITORY, token: env.GH_TOKEN, head, branch: 'main',
        isAncestor: (ancestor, tip) => { try { git('merge-base', '--is-ancestor', ancestor, tip); return true; } catch { return false; } } });
    } else if (env.GITHUB_EVENT_NAME !== 'workflow_dispatch') throw new Error('Unsupported CI event');
    if (base) {
      files = parseNameStatus(execFileSync('git', ['diff', '--name-status', '-z', '--find-renames', base, head, '--'], { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }));
      if (!classifyChanges(files).full) platformFiles = platformSensitiveFiles(files, file => [base, head].map(revision => {
        const entry = git('ls-tree', revision, '--', file);
        if (!entry) return ''; // Added or deleted in this revision.
        return git('show', `${revision}:${file}`);
      }));
    }
  } catch { base = null; reason = 'change-detection-unavailable'; }
  await emitPlan(createPlan({ head, base, files, platformFiles, mode, reason }), env);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
