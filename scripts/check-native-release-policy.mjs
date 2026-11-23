import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { lstat, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gunzipSync } from 'node:zlib';
import { assertCandidateManifest, assertNativeBinary, assertNativeVersion, candidateName, candidatePlist, fileDigest, nativeTargets } from './lib/native-package.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const actions = {
  checkout: 'actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10',
  node: 'actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38',
  python: 'actions/setup-python@ece7cb06caefa5fff74198d8649806c4678c61a1',
  go: 'actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16',
  upload: 'actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a',
};
const tooling = `corepack enable
corepack prepare pnpm@10.30.3 --activate
test "$(pnpm --version)" = "10.30.3"
pnpm --filter liberty-workspace install --frozen-lockfile --ignore-scripts`;
const scanner = `mkdir -p "\${RUNNER_TEMP}/liberty-native-security"
curl --location --fail --silent --show-error \\
  --output "\${RUNNER_TEMP}/liberty-native-security/osv-scanner" \\
  "https://github.com/google/osv-scanner/releases/download/v2.5.0/osv-scanner_linux_amd64"
printf '%s  %s\\n' \\
  'edcfc41d257db36148f065055655fe3fcfc434b0b423ea67468a84c207524e0c' \\
  "\${RUNNER_TEMP}/liberty-native-security/osv-scanner" | sha256sum --check
chmod 0755 "\${RUNNER_TEMP}/liberty-native-security/osv-scanner"
echo "\${RUNNER_TEMP}/liberty-native-security" >> "$GITHUB_PATH"`;
const targetTests = `test "$(go env GOHOSTOS)" = "$EXPECTED_SYSTEM"
if [[ "$EXPECTED_SYSTEM" = darwin ]]; then
  test "$(go env GOHOSTARCH)" = "$GOARCH"
else
  test "$(go env GOHOSTARCH)" = amd64
fi
go test -count=1 -timeout=30m ./...
go test -count=1 -tags credentials_acceptance ./internal/infrastructure/credentials`;

// Deliberately accept a small, unambiguous YAML subset. Anchors, aliases, tags,
// flow collections and duplicate keys cannot hide policy overrides.
export function parseNativeWorkflow(source) {
  const lines = source.replaceAll('\r\n', '\n').split('\n');
  let index = 0;
  const indent = line => line.match(/^ */)[0].length;
  const skip = () => { while (index < lines.length && /^\s*(?:#.*)?$/.test(lines[index])) index++; };
  function scalar(text) {
    if (text.startsWith('"')) return JSON.parse(text);
    if (text.startsWith("'")) {
      if (!/^'(?:[^']|'')*'$/.test(text)) throw new Error('Invalid workflow quote');
      return text.slice(1, -1).replaceAll("''", "'");
    }
    if (/^[&*!\[\]{},]|\s#/.test(text)) throw new Error('Unsupported workflow YAML construct');
    if (text === 'true' || text === 'false') return text === 'true';
    if (/^\d+$/.test(text)) return Number(text);
    return text;
  }
  function entry(text) {
    const match = text.match(/^([A-Za-z][A-Za-z0-9_-]*):(?: +(.*))?$/);
    if (!match) throw new Error('Unsupported workflow mapping');
    return [match[1], match[2]];
  }
  function value(text, level) {
    if (text === '|') {
      const result = [];
      while (index < lines.length && (!lines[index].trim() || indent(lines[index]) > level)) {
        if (lines[index].trim() && indent(lines[index]) < level + 2) throw new Error('Invalid workflow block indentation');
        result.push(lines[index].slice(level + 2));
        index++;
      }
      return result.join('\n').trimEnd();
    }
    if (text !== undefined) return scalar(text);
    skip();
    return index < lines.length && indent(lines[index]) > level ? block(level + 2) : null;
  }
  function mapping(level, initial) {
    const result = {};
    if (initial) result[initial[0]] = value(initial[1], level);
    while (true) {
      skip();
      if (index >= lines.length || indent(lines[index]) < level) break;
      if (indent(lines[index]) !== level || lines[index].trimStart().startsWith('- ')) throw new Error('Invalid workflow mapping indentation');
      const [key, text] = entry(lines[index].slice(level));
      if (Object.hasOwn(result, key)) throw new Error(`Duplicate workflow key: ${key}`);
      index++;
      result[key] = value(text, level);
    }
    return result;
  }
  function block(level) {
    skip();
    if (indent(lines[index]) !== level) throw new Error('Invalid workflow indentation');
    if (!lines[index].slice(level).startsWith('- ')) return mapping(level);
    const result = [];
    while (true) {
      skip();
      if (index >= lines.length || indent(lines[index]) < level) break;
      if (indent(lines[index]) !== level || !lines[index].slice(level).startsWith('- ')) throw new Error('Invalid workflow sequence');
      const first = entry(lines[index].slice(level + 2));
      index++;
      result.push(mapping(level + 2, first));
    }
    return result;
  }
  if (source.includes('\t')) throw new Error('Workflow tabs are prohibited');
  const result = block(0);
  skip();
  if (index !== lines.length) throw new Error('Unexpected workflow content');
  return result;
}

const checkout = () => ({ name: 'Checkout exact source', uses: actions.checkout, with: { ref: '${{ github.sha }}', 'persist-credentials': false } });
const node = () => ({ name: 'Setup Node', uses: actions.node, with: { 'node-version': 24 } });
const go = () => ({ name: 'Setup Go', uses: actions.go, with: { 'go-version': '1.27.4', 'cache-dependency-path': 'apps/native/go.sum' } });
const install = () => ({ name: 'Install pinned root tooling', shell: 'bash', run: tooling });
const command = (name, run) => ({ name, run });

export function assertNativeWorkflow(source) {
  const workflow = parseNativeWorkflow(source);
  // Closed contracts prevent an appended command, skipped check, elevated
  // permission or additional publishing job from bypassing the required steps.
  const expected = {
    name: 'Build Native Candidates',
    on: { workflow_dispatch: null, workflow_call: null },
    permissions: { contents: 'read' },
    env: { FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true, GOTOOLCHAIN: 'go1.27.4', GOFLAGS: '-mod=readonly' },
    concurrency: { group: 'native-candidate-${{ github.workflow }}-${{ github.ref }}', 'cancel-in-progress': false },
    jobs: {
      quality: {
        name: 'Independent Native Quality', 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 60,
        steps: [checkout(), node(), { name: 'Setup Python', uses: actions.python, with: { 'python-version': '3.12' } }, go(), install(),
          command('Bootstrap Python test tools', 'pnpm python:bootstrap'),
          command('Verify Native sources, Python and candidate policy', 'pnpm native:verify'),
          command('Replay frozen Native compatibility fixtures', 'pnpm native:compat'),
          { name: 'Check Native concurrency', 'working-directory': 'apps/native', run: 'go test -race -count=1 -timeout=30m ./...' },
          command('Compile all four Native targets', 'pnpm native:build:matrix'),
          command('Check Native licenses', 'pnpm native:licenses:check'),
          command('Generate Native SBOM', 'pnpm native:sbom:generate'),
          { name: 'Upload Native SBOM and notices', uses: actions.upload, with: { name: 'liberty-native-sbom-${{ github.sha }}', path: 'artifacts/sbom/liberty-native.cdx.json\nartifacts/sbom/native-go-NOTICES.txt', 'if-no-files-found': 'error', 'retention-days': 14 } }],
      },
      security: {
        name: 'Required Native Dependency Security', 'runs-on': 'ubuntu-22.04', 'timeout-minutes': 30,
        steps: [checkout(), node(), go(), install(), { name: 'Install authenticated OSV Scanner', shell: 'bash', run: scanner }, command('Enforce real Native vulnerability scan', 'pnpm native:security:check')],
      },
      build: {
        name: 'Candidate (${{ matrix.target }})', needs: 'quality', 'runs-on': '${{ matrix.runner }}', 'timeout-minutes': 60,
        strategy: { 'fail-fast': false, matrix: { include: [
          { target: 'darwin-aarch64', runner: 'macos-latest', system: 'darwin', arch: 'arm64' },
          { target: 'darwin-x64', runner: 'macos-15-intel', system: 'darwin', arch: 'amd64' },
          { target: 'windows-x64', runner: 'windows-2022', system: 'windows', arch: 'amd64' },
          { target: 'windows-x86', runner: 'windows-2022', system: 'windows', arch: '386' },
        ] } },
        steps: [checkout(), node(), go(),
          { name: 'Execute target tests and isolated system credentials', 'working-directory': 'apps/native', shell: 'bash', env: { GOARCH: '${{ matrix.arch }}', CGO_ENABLED: '0', EXPECTED_SYSTEM: '${{ matrix.system }}' }, run: targetTests },
          { name: 'Package and execute extracted candidate', shell: 'bash', env: { NATIVE_TARGET: '${{ matrix.target }}' }, run: 'node scripts/package-native.mjs --target "$NATIVE_TARGET" --verify-host' },
          { name: 'Verify exact archive, manifest and checksums', shell: 'bash', env: { NATIVE_TARGET: '${{ matrix.target }}', NATIVE_COMMIT: '${{ github.sha }}' }, run: 'node scripts/check-native-release-policy.mjs verify-artifacts --target "$NATIVE_TARGET" --commit "$NATIVE_COMMIT" --output "artifacts/native/$NATIVE_TARGET"' },
          { name: 'Upload verified unsigned candidate', uses: actions.upload, with: { name: 'liberty-native-candidate-${{ matrix.target }}-${{ github.sha }}', path: 'artifacts/native/${{ matrix.target }}/', 'if-no-files-found': 'error', 'retention-days': 14 } }],
      },
    },
  };
  try { assert.deepEqual(workflow, expected); }
  catch { throw new Error('Native workflow differs from the reviewed read-only candidate, security, quality or four-platform delivery policy'); }
  return workflow;
}

const safePath = name => typeof name === 'string' && name && !/[\\:\x00]/.test(name) && !name.startsWith('/') && name.split('/').every(part => part && part !== '.' && part !== '..');
const sameSet = (actual, expected) => assert.deepEqual([...actual].sort(), [...expected].sort(), 'Candidate file set differs');

async function regularFiles(directory, prefix = '') {
  const files = new Map();
  for (const entry of await readdir(path.join(directory, prefix), { withFileTypes: true })) {
    const name = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (!safePath(name) || entry.isSymbolicLink()) throw new Error('Candidate contains an unsafe package path');
    if (entry.isDirectory()) {
      const nested = await regularFiles(directory, name);
      if (!nested.size) throw new Error('Candidate contains an unexpected empty directory');
      for (const [key, bytes] of nested) files.set(key, bytes);
    } else if (entry.isFile()) files.set(name, await readFile(path.join(directory, name)));
    else throw new Error('Candidate contains a non-regular file');
  }
  return files;
}

// Inspect tar records before any extraction. Never hand an unverified link or
// path to the OS tar utility. PAX metadata is accepted only for times and paths.
export function readNativeArchive(compressed) {
  const archive = gunzipSync(compressed, { maxOutputLength: 512 * 1024 * 1024 });
  const files = new Map();
  const seen = new Set();
  const directories = new Set();
  let pax = {};
  for (let offset = 0; offset < archive.length;) {
    const header = archive.subarray(offset, offset + 512);
    if (header.length !== 512) throw new Error('Truncated candidate archive');
    if (header.every(byte => byte === 0)) {
      if (archive.length - offset < 1024 || !archive.subarray(offset).every(byte => byte === 0) || Object.keys(pax).length) throw new Error('Invalid candidate archive trailer');
      const ancestors = new Set();
      for (const name of files.keys()) {
        const parts = name.split('/');
        for (let i = 1; i < parts.length; i++) ancestors.add(parts.slice(0, i).join('/'));
      }
      if ([...directories].some(name => !ancestors.has(name))) throw new Error('Candidate archive contains an unexpected directory');
      return files;
    }
    const text = (start, end) => header.subarray(start, end).toString('utf8').replace(/\0.*$/s, '');
    const octal = (start, end) => {
      const value = text(start, end).trim();
      if (!/^[0-7]+$/.test(value)) throw new Error('Invalid candidate archive number');
      return Number.parseInt(value, 8);
    };
    const checksum = [...header].reduce((sum, byte, index) => sum + (index >= 148 && index < 156 ? 32 : byte), 0);
    if (checksum !== octal(148, 156) || !['ustar', 'ustar '].includes(text(257, 263))) throw new Error('Invalid candidate archive header');
    const prefix = text(345, 500);
    const recordName = `${prefix ? `${prefix}/` : ''}${text(0, 100)}`;
    const size = octal(124, 136);
    const type = text(156, 157);
    if (offset + 512 + size > archive.length || (!safePath(recordName.replace(/\/$/, '')) && !(type === 'L' && recordName === '././@LongLink'))) throw new Error('Unsafe candidate archive record');
    const bytes = archive.subarray(offset + 512, offset + 512 + size);
    if (type === 'L') {
      if (Object.keys(pax).length || bytes.at(-1) !== 0 || bytes.subarray(0, -1).includes(0)) throw new Error('Invalid candidate archive long path');
      pax.path = bytes.subarray(0, -1).toString('utf8');
    } else if (type === 'x') {
      if (Object.keys(pax).length) throw new Error('Duplicate candidate archive metadata');
      for (let cursor = 0; cursor < bytes.length;) {
        const space = bytes.indexOf(32, cursor);
        const count = Number(bytes.subarray(cursor, space).toString());
        if (space < cursor || !Number.isSafeInteger(count) || count <= space - cursor + 1 || cursor + count > bytes.length || bytes[cursor + count - 1] !== 10) throw new Error('Invalid candidate archive metadata');
        const pair = bytes.subarray(space + 1, cursor + count - 1).toString();
        const split = pair.indexOf('=');
        const key = pair.slice(0, split);
        if (split < 1 || !['path', 'mtime', 'atime', 'ctime'].includes(key) || Object.hasOwn(pax, key)) throw new Error('Unsupported candidate archive metadata');
        pax[key] = pair.slice(split + 1);
        cursor += count;
      }
    } else {
      const name = (pax.path ?? recordName).replace(/\/$/, '');
      pax = {};
      if (!safePath(name) || seen.has(name) || !['0', '', '5'].includes(type)) throw new Error('Unsafe, duplicate or linked candidate archive entry');
      seen.add(name);
      if (type === '5') {
        if (size !== 0) throw new Error('Invalid candidate archive directory');
        directories.add(name);
      } else files.set(name, bytes);
    }
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  throw new Error('Missing candidate archive trailer');
}

const nativeBuildInputs = [
  'apps/native', 'packages/desktop-resources', 'packages/shared-types',
  'python/funasr-runner', 'scripts', 'config', '.github/workflows/build-native.yml',
  'package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', '.npmrc', '.gitattributes',
  'go.work', 'go.work.sum', 'LICENSE',
];

async function assertRegularAncestors(directory) {
  const parsed = path.parse(directory);
  let current = parsed.root;
  for (const part of directory.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    let info;
    try { info = await lstat(current); }
    catch (error) { if (error.code === 'ENOENT') return; throw error; }
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error('Candidate artifact output ancestors must be regular directories without symlinks');
  }
}

export async function verifyNativeArtifacts({ rootDirectory = root, target, commit, output, currentCommit }) {
  if (!Object.hasOwn(nativeTargets, target) || !/^[a-f0-9]{40}$/.test(commit ?? '')) throw new Error('Exact candidate target and source commit are required');
  // currentCommit is an explicit synthetic-fixture seam, never exposed by the
  // CLI. Real build evidence requires both HEAD and clean committed inputs.
  const checkedOutCommit = currentCommit ?? execFileSync('git', ['rev-parse', 'HEAD'], { cwd: rootDirectory, encoding: 'utf8' }).trim();
  if (checkedOutCommit !== commit) throw new Error('Candidate source commit differs from checkout');
  if (currentCommit === undefined) {
    const status = execFileSync('git', ['status', '--porcelain=v1', '--untracked-files=all', '--', ...nativeBuildInputs], { cwd: rootDirectory, encoding: 'utf8' });
    if (status.trim()) throw new Error('Candidate source inputs are dirty or untracked; cannot bind artifact evidence to HEAD');
  }
  const workspace = JSON.parse(await readFile(path.join(rootDirectory, 'package.json'), 'utf8'));
  const host = await readFile(path.join(rootDirectory, 'apps/native/internal/host/profile.go'), 'utf8');
  const version = workspace.version;
  assertNativeVersion(version, [host.match(/^const Version = "([^"]+)"$/m)?.[1]]);
  if (workspace.packageManager !== 'pnpm@10.30.3') throw new Error('Candidate tooling version drift');
  const name = `liberty-native-candidate-${version}-${target}`;
  const packages = path.join(rootDirectory, 'target/native/packages');
  const entries = (await readdir(packages, { withFileTypes: true })).filter(entry => entry.name.startsWith(`${name}-`));
  if (entries.length !== 1 || !entries[0].isDirectory() || !entries[0].name.startsWith(`${name}-`) || !/^[A-Za-z0-9]+$/.test(entries[0].name.slice(name.length + 1))) throw new Error('Expected exactly one target candidate package');
  const parent = path.join(packages, entries[0].name);
  sameSet(await readdir(parent), [name, `${name}.tar.gz`, 'SHA256SUMS']);
  for (const [relative, directory] of [[name, true], [`${name}.tar.gz`, false], ['SHA256SUMS', false]]) {
    const info = await lstat(path.join(parent, relative));
    if (info.isSymbolicLink() || (directory ? !info.isDirectory() : !info.isFile())) throw new Error('Candidate assets must be regular files and directories');
  }
  const files = await regularFiles(path.join(parent, name));
  const manifestBytes = files.get('candidate-manifest.json');
  const manifest = JSON.parse(manifestBytes?.toString() ?? 'null');
  assertCandidateManifest(manifest);
  assertNativeVersion(version, [manifest.version]);
  if (manifest.target !== target || manifest.defaultRuntimeProfile !== 'production' || JSON.stringify(manifest.availableRuntimeProfiles) !== '["production"]') throw new Error('Candidate target or runtime profile differs');
  sameSet(Object.keys(manifest), ['format', 'channel', 'identity', 'version', 'target', 'productionReady', 'signed', 'notarized', 'defaultRuntimeProfile', 'availableRuntimeProfiles', 'files']);
  const mac = nativeTargets[target].os === 'darwin';
  const resources = mac ? `${candidateName}.app/Contents/Resources/` : '';
  const binary = mac ? `${candidateName}.app/Contents/MacOS/liberty-native` : `${candidateName}.exe`;
  const required = [binary, 'README.txt', `${resources}LICENSE`, `${resources}THIRD_PARTY_NOTICES.txt`, `${resources}${mac ? 'icon.icns' : 'icon.ico'}`, ...(mac ? [`${candidateName}.app/Contents/Info.plist`] : [])];
  sameSet(manifest.files.map(file => file.path), required);
  sameSet(files.keys(), [...required, 'candidate-manifest.json']);
  for (const file of manifest.files) {
    sameSet(Object.keys(file), ['path', 'bytes', 'sha256']);
    const bytes = files.get(file.path);
    if (bytes.length !== file.bytes || fileDigest(bytes) !== file.sha256) throw new Error('Candidate payload integrity failed');
  }
  assertNativeBinary(files.get(binary), target);
  if (mac && files.get(`${candidateName}.app/Contents/Info.plist`).toString() !== candidatePlist(version)) throw new Error('Candidate bundle identity differs');
  const archiveName = `${name}.tar.gz`;
  const archive = await readFile(path.join(parent, archiveName));
  if ((await readFile(path.join(parent, 'SHA256SUMS'), 'utf8')) !== `${fileDigest(archive)}  ${archiveName}\n`) throw new Error('Candidate archive checksum differs');
  const archivedFiles = readNativeArchive(archive);
  sameSet(archivedFiles.keys(), [...files.keys()].map(file => `${name}/${file}`));
  for (const [file, bytes] of files) {
    if (!bytes.equals(archivedFiles.get(`${name}/${file}`))) throw new Error('Candidate archive bytes differ from verified package');
  }
  const outputDirectory = path.resolve(rootDirectory, output ?? `artifacts/native/${target}`);
  await assertRegularAncestors(outputDirectory);
  await mkdir(outputDirectory, { recursive: true });
  await assertRegularAncestors(outputDirectory);
  const outputInfo = await lstat(outputDirectory);
  if (outputInfo.isSymbolicLink() || !outputInfo.isDirectory() || (await readdir(outputDirectory)).length) throw new Error('Candidate artifact output must be empty and regular');
  const artifactFiles = new Map([[archiveName, archive], ['candidate-manifest.json', manifestBytes]]);
  const report = {
    format: 1, sourceCommit: commit, version, target, channel: 'candidate', productionReady: false, signed: false, notarized: false, productionSecurityClearance: false,
    files: [...artifactFiles].map(([name, bytes]) => ({ name, bytes: bytes.length, sha256: fileDigest(bytes) })),
  };
  artifactFiles.set('native-build-manifest.json', Buffer.from(JSON.stringify(report, null, 2) + '\n'));
  artifactFiles.set('SHA256SUMS', Buffer.from([...artifactFiles].map(([name, bytes]) => `${fileDigest(bytes)}  ${name}\n`).join('')));
  for (const [name, bytes] of artifactFiles) await writeFile(path.join(outputDirectory, name), bytes, { flag: 'wx', mode: 0o600 });
  sameSet(await readdir(outputDirectory), artifactFiles.keys());
  for (const [name, bytes] of artifactFiles) if (!(await readFile(path.join(outputDirectory, name))).equals(bytes)) throw new Error('Candidate artifact publication differs');
  return report;
}

async function main() {
  const args = process.argv.slice(2);
  if (!args.length) {
    assertNativeWorkflow(await readFile(path.join(root, '.github/workflows/build-native.yml'), 'utf8'));
    console.log('Native candidate workflow preserves independent quality, required security, four execution targets and read-only delivery.');
    return;
  }
  if (args[0] !== 'verify-artifacts' || args.length !== 7 || args[1] !== '--target' || args[3] !== '--commit' || args[5] !== '--output') throw new Error('Expected verify-artifacts --target <target> --commit <sha> --output <artifact-directory>');
  const report = await verifyNativeArtifacts({ target: args[2], commit: args[4], output: args[6] });
  console.log(`Verified exact unsigned ${report.target} candidate archive, manifest and checksums for ${report.sourceCommit}. Production clearance remains false.`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
