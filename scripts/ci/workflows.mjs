import assert from 'node:assert/strict';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { fullSuites } from './plan.mjs';
import { parseWorkflow } from './yaml.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
// Keep download authentication in the same closed contract as the job graph.
export const securityInstaller = `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "\${RUNNER_TEMP}/liberty-security-tools" "\${HOME}/.local/bin"
curl --location --fail --silent --show-error \\
  --output "\${RUNNER_TEMP}/liberty-security-tools/cargo-deny.tar.gz" \\
  "https://github.com/EmbarkStudios/cargo-deny/releases/download/0.20.2/cargo-deny-0.20.2-x86_64-unknown-linux-musl.tar.gz"
printf '%s  %s\\n' \\
  '9f12ed4c49936e09b48bf862b595cde2fe64fcbd9d74dfacac6131ca824c8d5f' \\
  "\${RUNNER_TEMP}/liberty-security-tools/cargo-deny.tar.gz" | sha256sum --check
tar --extract --gzip --file "\${RUNNER_TEMP}/liberty-security-tools/cargo-deny.tar.gz" --directory "\${RUNNER_TEMP}/liberty-security-tools"
install -m 0755 "\${RUNNER_TEMP}/liberty-security-tools/cargo-deny-0.20.2-x86_64-unknown-linux-musl/cargo-deny" "\${HOME}/.local/bin/cargo-deny"
curl --location --fail --silent --show-error \\
  --output "\${RUNNER_TEMP}/liberty-security-tools/osv-scanner" \\
  "https://github.com/google/osv-scanner/releases/download/v2.5.0/osv-scanner_linux_amd64"
printf '%s  %s\\n' \\
  'edcfc41d257db36148f065055655fe3fcfc434b0b423ea67468a84c207524e0c' \\
  "\${RUNNER_TEMP}/liberty-security-tools/osv-scanner" | sha256sum --check
install -m 0755 "\${RUNNER_TEMP}/liberty-security-tools/osv-scanner" "\${HOME}/.local/bin/osv-scanner"
echo "\${HOME}/.local/bin" >> "\${GITHUB_PATH}"
`;
export function assertSecurityInstaller(source) {
  assert.equal(source, securityInstaller, 'Security tools must retain authenticated downloads and fail-closed installation');
}
export const actions = Object.freeze({
  checkout: 'actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10',
  node: 'actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38',
  python: 'actions/setup-python@ece7cb06caefa5fff74198d8649806c4678c61a1',
  go: 'actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16',
  rust: 'dtolnay/rust-toolchain@fa04a1451ff1842e2626ccb99004d0195b455a88',
  rustCache: 'swatinem/rust-cache@42dc69e1aa15d09112580998cf2ef0119e2e91ae',
  cache: 'actions/cache@0057852bfaa89a56745cba8c7296529d2fc39830',
  upload: 'actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a',
  download: 'actions/download-artifact@37930b1c2abaa49bbe596cd826c3c89aef350131',
});
const checkout = (deep = false) => ({ name: 'Checkout exact source', uses: actions.checkout, with: { ref: '${{ github.sha }}', 'persist-credentials': false, ...(deep ? { 'fetch-depth': 0 } : {}) } });
const node = () => ({ name: 'Setup Node', uses: actions.node, with: { 'node-version': 24 } });
const go = () => ({ name: 'Setup Go', uses: actions.go, with: { 'go-version': '1.27.4', 'cache-dependency-path': 'apps/native/go.sum' } });
const only = suites => `\${{ contains(fromJSON('${JSON.stringify(suites)}'), inputs.suite) }}`;
const readonly = { contents: 'read' };
const environment = { FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true, GOTOOLCHAIN: 'go1.27.4', GOFLAGS: '-mod=readonly' };
export const targets = Object.freeze([
  { target: 'darwin-aarch64', runner: 'macos-latest', system: 'darwin', arch: 'arm64' },
  { target: 'darwin-x64', runner: 'macos-15-intel', system: 'darwin', arch: 'amd64' },
  { target: 'windows-x64', runner: 'windows-2022', system: 'windows', arch: 'amd64' },
]);
const bootstrap = `if [[ "$TRUSTED_BASE" =~ ^[a-f0-9]{40}$ ]] && git show "$TRUSTED_BASE:scripts/ci/plan.mjs" > "$RUNNER_TEMP/liberty-ci-plan.mjs"; then
  node "$RUNNER_TEMP/liberty-ci-plan.mjs"
else
  node --input-type=module <<'JS'
import { appendFileSync } from 'node:fs';
const plan = { format: 1, head: process.env.GITHUB_SHA, base: null, mode: 'full', full: true, suites: ${JSON.stringify(fullSuites)}, reasons: ['trusted-planner-unavailable'], changedFiles: 0 };
appendFileSync(process.env.GITHUB_OUTPUT, 'plan=' + JSON.stringify(plan) + '\\nfull=true\\nmatrix={"suite":["docs"]}\\n');
console.log(JSON.stringify(plan, null, 2));
JS
fi`;

export const workflows = {
  'quality.yml': {
    name: 'Quality Gate',
    on: { pull_request: { branches: ['main'] }, push: { branches: ['main'] }, workflow_dispatch: { inputs: { mode: { description: 'On-demand validation', required: true, type: 'choice', default: 'full', options: ['full', 'security'] } } } },
    permissions: readonly,
    env: { FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true },
    concurrency: { group: 'ci-${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}', 'cancel-in-progress': true },
    jobs: {
      plan: {
        name: 'Select checks', 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 5,
        permissions: { contents: 'read', actions: 'read' },
        outputs: { plan: '${{ steps.select.outputs.plan }}', full: '${{ steps.select.outputs.full }}', matrix: '${{ steps.select.outputs.matrix }}' },
        steps: [checkout(true), node(), { name: 'Classify with trusted base rules', id: 'select', shell: 'bash', env: { TRUSTED_BASE: "${{ github.event.pull_request.base.sha || github.event.before || github.event_name == 'workflow_dispatch' && github.sha }}", GH_TOKEN: '${{ github.token }}' }, run: bootstrap }],
      },
      checks: {
        name: 'Selected (${{ matrix.suite }})', needs: 'plan', if: "${{ needs.plan.outputs.full == 'false' }}",
        strategy: { 'fail-fast': false, matrix: '${{ fromJSON(needs.plan.outputs.matrix) }}' },
        uses: './.github/workflows/ci-suite.yml', with: { suite: '${{ matrix.suite }}' }, permissions: readonly,
      },
      full: { name: 'Full validation', needs: 'plan', if: "${{ needs.plan.outputs.full == 'true' }}", uses: './.github/workflows/ci-full.yml', permissions: readonly },
      gate: {
        name: "${{ github.event_name == 'workflow_dispatch' && inputs.mode == 'security' && 'security-gate' || 'ci-gate' }}", if: '${{ always() }}', needs: ['plan', 'checks', 'full'], 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 5,
        steps: [checkout(), node(), { name: 'Require every selected check', env: { CI_PLAN: '${{ needs.plan.outputs.plan }}', CI_RESULTS: '{"plan":"${{ needs.plan.result }}","checks":"${{ needs.checks.result }}","full":"${{ needs.full.result }}"}' }, run: 'node scripts/ci/gate.mjs' }],
      },
    },
  },
  'ci-full.yml': {
    name: 'Complete Validation', on: { workflow_call: { inputs: { source_commit: { description: 'Optional exact source SHA for main-authorized publication', type: 'string', required: false, default: '' } } }, workflow_dispatch: null }, permissions: readonly,
    jobs: {
      checks: { name: 'Full (${{ matrix.suite }})', strategy: { 'fail-fast': false, matrix: { suite: [...fullSuites] } }, uses: './.github/workflows/ci-suite.yml', with: { suite: '${{ matrix.suite }}', source_commit: '${{ inputs.source_commit }}' }, permissions: readonly },
      gate: { name: 'full-gate', if: '${{ always() }}', needs: 'checks', 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 5,
        steps: [{ name: 'Require all tests and security', shell: 'bash', env: { CHECKS_RESULT: '${{ needs.checks.result }}' }, run: 'test "$CHECKS_RESULT" = success' }] },
    },
  },
  'ci-suite.yml': {
    name: 'Validation Suite', on: { workflow_call: { inputs: { suite: { description: 'Closed validation suite identifier', required: true, type: 'string' }, source_commit: { description: 'Optional exact source SHA', required: false, type: 'string', default: '' } } } }, permissions: readonly, env: environment,
    jobs: {
      run: {
        name: '${{ inputs.suite }}',
        'runs-on': "${{ inputs.suite == 'target-arm' && 'macos-latest' || inputs.suite == 'target-intel' && 'macos-15-intel' || startsWith(inputs.suite, 'windows-') && 'windows-2022' || inputs.suite == 'target-windows' && 'windows-2022' || 'ubuntu-22.04' }}",
        'timeout-minutes': 60,
        steps: [{ ...checkout(), with: { ref: '${{ inputs.source_commit || github.sha }}', 'persist-credentials': false } },
          { name: 'Require immutable validation source', shell: 'bash', env: { LIBERTY_SOURCE_COMMIT: '${{ inputs.source_commit }}' }, run: 'test -z "$LIBERTY_SOURCE_COMMIT" || [[ "$LIBERTY_SOURCE_COMMIT" =~ ^[a-f0-9]{40}$ ]]' }, node(),
          { ...go(), if: only(['native', 'policy', 'compat', 'race', 'matrix', 'target-arm', 'target-intel', 'target-windows', 'security']) },
          { name: 'Locate specialized Go build cache', id: 'go-build-cache', if: only(['race', 'matrix']), shell: 'bash', run: `node --input-type=module <<'JS'
import { execFileSync } from 'node:child_process';
import { appendFileSync } from 'node:fs';
appendFileSync(process.env.GITHUB_OUTPUT, 'path=' + execFileSync('go', ['env', 'GOCACHE'], { encoding: 'utf8' }).trim() + '\\n');
JS` },
          { name: 'Go race and cross-compilation cache', if: only(['race', 'matrix']), uses: actions.cache, with: {
            path: '${{ steps.go-build-cache.outputs.path }}',
            key: "go-build-${{ runner.os }}-${{ runner.arch }}-1.27.4-${{ inputs.suite }}-${{ hashFiles('apps/native/go.sum') }}-${{ github.sha }}",
            'restore-keys': "go-build-${{ runner.os }}-${{ runner.arch }}-1.27.4-${{ inputs.suite }}-${{ hashFiles('apps/native/go.sum') }}-",
          } },
          { name: 'Setup Python', if: only(['python', 'rust']), uses: actions.python, with: { 'python-version': '3.12', cache: 'pip', 'cache-dependency-path': 'python/funasr-runner/requirements-dev-lock.txt' } },
          { name: 'Setup Rust', if: only(['rust', 'windows-rust', 'security']), uses: actions.rust, with: { toolchain: '1.94.0', components: 'clippy,rustfmt' } },
          { name: 'Rust cache', if: only(['rust', 'windows-rust', 'security']), uses: actions.rustCache, with: { workspaces: '. -> target', key: '${{ runner.os }}-${{ runner.arch }}-${{ inputs.suite }}' } },
          { name: 'Linux desktop libraries', if: "${{ inputs.suite == 'rust' }}", shell: 'bash', run: 'sudo apt-get update\nsudo apt-get install --yes --no-install-recommends build-essential libayatana-appindicator3-dev librsvg2-dev libssl-dev libwebkit2gtk-4.1-dev libxdo-dev' },
          { name: 'Pinned package manager', if: only(['native', 'python', 'frontend', 'policy', 'compat', 'matrix', 'security']), shell: 'bash', run: 'corepack enable\ncorepack prepare pnpm@10.30.3 --activate\ntest "$(pnpm --version)" = "10.30.3"\npnpm config set store-dir "$RUNNER_TEMP/liberty-pnpm-store"' },
          { name: 'Node dependency cache', if: only(['native', 'python', 'frontend', 'policy', 'compat', 'matrix', 'security']), uses: actions.cache, with: { path: '${{ runner.temp }}/liberty-pnpm-store', key: "pnpm-${{ runner.os }}-${{ runner.arch }}-10.30.3-${{ hashFiles('pnpm-lock.yaml') }}" } },
          { name: 'Install root tooling', if: only(['native', 'python', 'policy', 'compat', 'matrix', 'security']), run: 'pnpm --filter liberty-workspace install --frozen-lockfile --ignore-scripts' },
          { name: 'Install frontend dependencies', if: "${{ inputs.suite == 'frontend' }}", run: 'pnpm install --frozen-lockfile' },
          { name: 'Install authenticated security scanners', if: "${{ inputs.suite == 'security' }}", shell: 'bash', run: 'bash scripts/ci/install-security-tools.sh' },
          { name: 'Execute required suite', env: { LIBERTY_CI_SUITE: '${{ inputs.suite }}' }, run: 'node scripts/ci/run.mjs' },
          { name: 'Archive verified inventories', if: "${{ inputs.suite == 'security' }}", uses: actions.upload, with: { name: 'liberty-inventories-${{ github.sha }}', path: 'artifacts/sbom/', 'if-no-files-found': 'error', 'retention-days': 14 } },
        ],
      },
    },
  },
  'build-native.yml': {
    name: 'Build Native Candidates', on: {
      workflow_dispatch: { inputs: { target: { description: 'Candidate target', type: 'choice', required: true, default: 'all', options: ['all', ...targets.map(target => target.target)] } } },
      workflow_call: { inputs: { target: { description: 'Candidate target', type: 'string', required: false, default: 'all' } } },
    }, permissions: readonly, env: environment,
    concurrency: { group: 'native-candidate-${{ github.workflow }}-${{ github.ref }}', 'cancel-in-progress': false },
    jobs: {
      quality: { name: 'Required complete validation', uses: './.github/workflows/ci-full.yml', permissions: readonly },
      select: { name: 'Select candidate target', 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 5,
        outputs: { matrix: '${{ steps.target.outputs.matrix }}' },
        steps: [node(), { name: 'Validate requested target', id: 'target', shell: 'bash', env: { NATIVE_TARGET: '${{ inputs.target }}' }, run: `node --input-type=module <<'JS'
import { appendFileSync } from 'node:fs';
const targets = ${JSON.stringify(targets)};
const selected = process.env.NATIVE_TARGET === 'all' ? targets : targets.filter(target => target.target === process.env.NATIVE_TARGET);
if (!selected.length) throw new Error('Unsupported candidate target');
appendFileSync(process.env.GITHUB_OUTPUT, 'matrix=' + JSON.stringify({ include: selected }) + '\\n');
JS` }] },
      build: {
        name: 'Candidate (${{ matrix.target }})', needs: ['quality', 'select'], 'runs-on': '${{ matrix.runner }}', 'timeout-minutes': 60,
        strategy: { 'fail-fast': false, matrix: '${{ fromJSON(needs.select.outputs.matrix) }}' },
        steps: [checkout(), node(), go(),
          { name: 'Package and execute extracted candidate', shell: 'bash', env: { NATIVE_TARGET: '${{ matrix.target }}' }, run: 'node scripts/package-native.mjs --target "$NATIVE_TARGET" --verify-host' },
          { name: 'Verify exact archive, manifest and checksums', shell: 'bash', env: { NATIVE_TARGET: '${{ matrix.target }}', NATIVE_COMMIT: '${{ github.sha }}' }, run: 'node scripts/check-native-release-policy.mjs verify-artifacts --target "$NATIVE_TARGET" --commit "$NATIVE_COMMIT" --output "artifacts/native/$NATIVE_TARGET"' },
          { name: 'Upload verified unsigned candidate', uses: actions.upload, with: { name: 'liberty-native-candidate-${{ matrix.target }}-${{ github.sha }}', path: 'artifacts/native/${{ matrix.target }}/', 'if-no-files-found': 'error', 'retention-days': 14 } },
        ],
      },
    },
  },
  'release-native.yml': {
    name: 'Publish Native Prerelease',
    on: { workflow_dispatch: { inputs: {
      candidate_run_id: { description: 'Successful three-platform Build Native Candidates run ID', type: 'string', required: true },
      replace_release_id: { description: 'Optional exact manual prerelease ID to migrate to the bot; changes Release ID', type: 'string', required: false, default: '' },
    } } },
    permissions: readonly,
    env: { FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true },
    concurrency: { group: 'native-release', 'cancel-in-progress': false },
    jobs: {
      prepare: {
        name: 'Authorize exact main source', 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 5,
        permissions: { contents: 'read', actions: 'read' },
        outputs: { source_commit: '${{ steps.source.outputs.source_commit }}' },
        steps: [checkout(true), node(), { name: 'Validate source, version and successful candidate run', id: 'source', env: { GH_TOKEN: '${{ github.token }}', NATIVE_CANDIDATE_RUN_ID: '${{ inputs.candidate_run_id }}', NATIVE_REPLACE_RELEASE_ID: '${{ inputs.replace_release_id }}' }, run: 'node scripts/publish-native-release.mjs prepare' }],
      },
      quality: {
        name: 'Required source validation', needs: 'prepare', uses: './.github/workflows/ci-full.yml',
        with: { source_commit: '${{ needs.prepare.outputs.source_commit }}' }, permissions: readonly,
      },
      stage: {
        name: 'Verify and preserve release inputs', needs: ['prepare', 'quality'], 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 30,
        permissions: { contents: 'read', actions: 'read' },
        outputs: { bundle_attempt: '${{ steps.bundle.outputs.bundle_attempt }}' },
        steps: [checkout(true), node(),
          { name: 'Authenticate artifacts and stage complete release', id: 'bundle', env: { GH_TOKEN: '${{ github.token }}', NATIVE_CANDIDATE_RUN_ID: '${{ inputs.candidate_run_id }}', NATIVE_REPLACE_RELEASE_ID: '${{ inputs.replace_release_id }}', NATIVE_EXPECTED_SOURCE_COMMIT: '${{ needs.prepare.outputs.source_commit }}' }, run: 'node scripts/publish-native-release.mjs stage' },
          { name: 'Preserve verified bundle and any previous release before writes', uses: actions.upload, with: { name: 'native-release-bundle-${{ github.run_id }}-${{ github.run_attempt }}', path: 'artifacts/native-release/', 'if-no-files-found': 'error', 'retention-days': 14 } },
        ],
      },
      publish: {
        name: 'Publish verified prerelease as bot', needs: ['prepare', 'quality', 'stage'], 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 30,
        permissions: { contents: 'write', actions: 'read' },
        steps: [checkout(true), node(),
          { name: 'Download immutable verified release bundle', uses: actions.download, with: { name: 'native-release-bundle-${{ github.run_id }}-${{ needs.stage.outputs.bundle_attempt }}', path: 'artifacts/native-release/' } },
          { name: 'Create or resume exact bot draft, verify downloads and publish', env: { GH_TOKEN: '${{ github.token }}', NATIVE_CANDIDATE_RUN_ID: '${{ inputs.candidate_run_id }}', NATIVE_REPLACE_RELEASE_ID: '${{ inputs.replace_release_id }}', NATIVE_EXPECTED_SOURCE_COMMIT: '${{ needs.prepare.outputs.source_commit }}' }, run: 'node scripts/publish-native-release.mjs publish' },
          { name: 'Archive exact publication result', if: '${{ always() }}', uses: actions.upload, with: { name: 'native-publication-${{ github.run_id }}-${{ github.run_attempt }}', path: 'artifacts/native-publication.json', 'if-no-files-found': 'error', 'retention-days': 14 } },
        ],
      },
    },
  },
};

export function assertWorkflow(name, source) {
  assert.ok(Object.hasOwn(workflows, name), 'Unknown generated workflow');
  const actual = parseWorkflow(source);
  assert.deepEqual(actual, workflows[name], `${name} differs from the reviewed event, validation, permission or delivery policy`);
  return actual;
}

export async function checkWorkflows(directory = path.join(root, '.github/workflows')) {
  assert.deepEqual((await readdir(directory)).sort(), [...Object.keys(workflows), 'build-desktop.yml'].sort(), 'Unreviewed workflow entry');
  for (const name of Object.keys(workflows)) assertWorkflow(name, await readFile(path.join(directory, name), 'utf8'));
  const release = await readFile(path.join(directory, 'build-desktop.yml'), 'utf8');
  assert.ok(!/^\s*(?:schedule|pull_request_target):/m.test(release), 'Scheduled or privileged PR workflows are prohibited');
  assert.match(release, /uses: \.\/\.github\/workflows\/ci-full\.yml/, 'Release must call complete validation');
  assertSecurityInstaller(await readFile(path.join(root, 'scripts/ci/install-security-tools.sh'), 'utf8'));
}

export function renderYaml(value, indent = 0) {
  const pad = ' '.repeat(indent);
  const scalar = item => typeof item === 'string' ? JSON.stringify(item) : String(item);
  const entry = (key, item, prefix = pad) => {
    if (item === null) return `${prefix}${key}:\n`;
    if (typeof item === 'object') return `${prefix}${key}:\n${renderYaml(item, indent + 2)}`;
    if (typeof item === 'string' && item.includes('\n')) return `${prefix}${key}: |\n${item.split('\n').map(line => `${pad}  ${line}`).join('\n')}\n`;
    return `${prefix}${key}: ${scalar(item)}\n`;
  };
  if (Array.isArray(value)) return value.map(item => {
    if (typeof item !== 'object' || item === null) return `${pad}- ${scalar(item)}\n`;
    const mapping = renderYaml(item, indent + 2).split('\n');
    mapping[0] = `${pad}- ${mapping[0].slice(indent + 2)}`;
    return mapping.join('\n');
  }).join('');
  return Object.entries(value).map(([key, item]) => entry(key, item)).join('');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  assert.ok(process.argv.length === 3 && ['--write', '--check'].includes(process.argv[2]), 'Expected --write or --check');
  if (process.argv[2] === '--write') {
    for (const [name, workflow] of Object.entries(workflows)) {
      const source = '# Generated from scripts/ci/workflows.mjs; run pnpm ci:generate.\n' + renderYaml(workflow);
      assert.deepEqual(parseWorkflow(source), workflow);
      await writeFile(path.join(root, '.github/workflows', name), source);
    }
    await writeFile(path.join(root, 'scripts/ci/install-security-tools.sh'), securityInstaller);
  }
  else await checkWorkflows();
  console.log('CI/CD preserves selected checks, complete release gates, pinned tools and no scheduled runs.');
}
