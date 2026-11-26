import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, readdir, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { assertNativeCandidatePayload, assertNativeWorkflow, parseNativeWorkflow, readNativeArchive, verifyNativeArtifacts } from './check-native-release-policy.mjs';
import { candidateName, candidatePlist, fileDigest, nativeTargets } from './lib/native-package.mjs';

const workflow = await readFile(new URL('../.github/workflows/build-native.yml', import.meta.url), 'utf8');
const commit = 'a'.repeat(40);
const version = '9.8.7';

test('candidate builds require complete validation before publishing verified artifacts', () => {
  const parsed = assertNativeWorkflow(workflow);
  assert.deepEqual(parsed.jobs.build.needs, ['quality', 'select']);
  assert.equal(parsed.jobs.quality.uses, './.github/workflows/ci-full.yml');
  assert.equal(parsed.jobs.build.strategy.matrix, '${{ fromJSON(needs.select.outputs.matrix) }}');
});

for (const [name, before, after] of [
  ['automatic tag publication', '  workflow_dispatch:', '  push:\n    tags:\n      - "v*"\n  workflow_dispatch:'],
  ['untrusted event', '  workflow_call:', '  pull_request_target:'],
  ['write token', 'contents: "read"', 'contents: "write"'],
  ['mutable source checkout', 'ref: "${{ github.sha }}"', 'ref: "main"'],
  ['persisted checkout token', 'persist-credentials: false', 'persist-credentials: true'],
  ['full gate bypass', './.github/workflows/ci-full.yml', './.github/workflows/quality.yml'],
  ['missing runnable archive check', ' --verify-host', ''],
  ['unverified upload', 'run: "node scripts/check-native-release-policy.mjs verify-artifacts', 'run: "echo node scripts/check-native-release-policy.mjs verify-artifacts'],
  ['missing artifact silently accepted', 'if-no-files-found: "error"', 'if-no-files-found: "ignore"'],
  ['Intel silently cross-compiled', '"runner":"macos-15-intel"', '"runner":"macos-latest"'],
]) {
  test(`rejects candidate workflow mutation: ${name}`, () => {
    assert.ok(workflow.includes(before), 'mutation must affect the actual workflow');
    assert.throws(() => assertNativeWorkflow(workflow.replace(before, after)));
  });
}

test('rejects duplicate keys and ambiguous YAML policy overrides', () => {
  for (const source of ['permissions:\n  contents: read\n  contents: write\n', 'on: *other\n', 'on: { push: null }\n', 'jobs:\n\tbuild: yes\n']) {
    assert.throws(() => parseNativeWorkflow(source));
  }
});

function tar(records) {
  const blocks = [];
  for (const { name, bytes = Buffer.alloc(0), type = '0' } of records) {
    const header = Buffer.alloc(512);
    let leaf = name;
    let prefix = '';
    if (Buffer.byteLength(name) > 100) {
      const slash = name.lastIndexOf('/');
      prefix = name.slice(0, slash);
      leaf = name.slice(slash + 1);
      assert.ok(Buffer.byteLength(prefix) <= 155 && Buffer.byteLength(leaf) <= 100);
    }
    header.write(leaf, 0, 100);
    header.write('0000644\0', 100);
    header.write('0000000\0', 108);
    header.write('0000000\0', 116);
    header.write(`${bytes.length.toString(8).padStart(11, '0')}\0`, 124);
    header.write('00000000000\0', 136);
    header.fill(32, 148, 156);
    header.write(type, 156);
    header.write('ustar\0', 257);
    header.write('00', 263);
    header.write(prefix, 345, 155);
    header.write(`${header.reduce((sum, byte) => sum + byte, 0).toString(8).padStart(6, '0')}\0 `, 148);
    blocks.push(header, bytes, Buffer.alloc((512 - bytes.length % 512) % 512));
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}

async function fixture(context, target = 'windows-x64') {
  const directory = await realpath(await mkdtemp(path.join(os.tmpdir(), 'liberty-native-policy-')));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const name = `liberty-native-candidate-${version}-${target}`;
  const parent = path.join(directory, 'target/native/packages', `${name}-abc123`);
  const payload = path.join(parent, name);
  await mkdir(path.join(directory, 'apps/native/internal/host'), { recursive: true });
  await writeFile(path.join(directory, 'package.json'), JSON.stringify({ version, packageManager: 'pnpm@10.30.3' }));
  await writeFile(path.join(directory, 'apps/native/internal/host/profile.go'), `package host\nconst Version = "${version}"\n`);
  const mac = target.startsWith('darwin-');
  const resources = mac ? `${candidateName}.app/Contents/Resources/` : '';
  const binary = mac ? `${candidateName}.app/Contents/MacOS/liberty-native` : `${candidateName}.exe`;
  const program = Buffer.alloc(256);
  if (mac) {
    program.writeUInt32LE(0xfeedfacf, 0);
    program.writeUInt32LE(nativeTargets[target].machine, 4);
  } else {
    program.write('MZ');
    program.writeUInt32LE(64, 0x3c);
    program.writeUInt32LE(0x4550, 64);
    program.writeUInt16LE(nativeTargets[target].machine, 68);
  }
  const files = new Map([[binary, program], ['README.txt', Buffer.from('Unsigned, unnotarized isolated candidate. Not an installer.\n')],
    [`${resources}LICENSE`, Buffer.from('Synthetic license\n')], [`${resources}THIRD_PARTY_NOTICES.txt`, Buffer.from('Synthetic notices\n')],
    [`${resources}LOCAL_TRANSLATION_NOTICES.txt`, Buffer.from('Synthetic local translation notices\n')],
    [`${resources}${mac ? 'icon.icns' : 'icon.ico'}`, Buffer.from('Synthetic icon\n')],
    ...(mac ? [[`${candidateName}.app/Contents/Info.plist`, Buffer.from(candidatePlist(version))]] : [])]);
  const manifest = { format: 1, channel: 'candidate', identity: 'com.westng.liberty.native-candidate', version, target,
    productionReady: false, signed: false, notarized: false, defaultRuntimeProfile: 'production', availableRuntimeProfiles: ['production'], files: [] };
  async function refresh(records) {
    manifest.files = [...files].map(([path, bytes]) => ({ path, bytes: bytes.length, sha256: fileDigest(bytes) }));
    for (const [name, bytes] of files) {
      await mkdir(path.dirname(path.join(payload, name)), { recursive: true });
      await writeFile(path.join(payload, name), bytes);
    }
    const manifestBytes = Buffer.from(JSON.stringify(manifest, null, 2) + '\n');
    await writeFile(path.join(payload, 'candidate-manifest.json'), manifestBytes);
    const archive = tar(records ?? [...files, ['candidate-manifest.json', manifestBytes]].map(([file, bytes]) => ({ name: `${name}/${file}`, bytes })));
    await writeFile(path.join(parent, `${name}.tar.gz`), archive);
    await writeFile(path.join(parent, 'SHA256SUMS'), `${fileDigest(archive)}  ${name}.tar.gz\n`);
  }
  await refresh();
  const options = { rootDirectory: directory, target, commit, currentCommit: commit };
  return { directory, parent, payload, name, files, binary, manifest, refresh, options };
}

for (const target of Object.keys(nativeTargets)) {
  test(`verifies and publishes exactly four candidate artifact files: ${target}`, async context => {
    const f = await fixture(context, target);
    const report = await verifyNativeArtifacts(f.options);
    assert.equal(report.target, target);
    assert.equal(report.sourceCommit, commit);
    assert.equal(report.productionReady, false);
    assert.equal(report.productionSecurityClearance, false);
    const output = path.join(f.directory, 'artifacts/native', target);
    assert.deepEqual((await readdir(output)).sort(), ['SHA256SUMS', 'candidate-manifest.json', `${f.name}.tar.gz`, 'native-build-manifest.json'].sort());
    const checksums = await readFile(path.join(output, 'SHA256SUMS'), 'utf8');
    for (const name of ['candidate-manifest.json', `${f.name}.tar.gz`, 'native-build-manifest.json']) {
      assert.ok(checksums.includes(`${fileDigest(await readFile(path.join(output, name)))}  ${name}\n`));
    }
    await assert.rejects(verifyNativeArtifacts(f.options), /output must be empty/);
  });

  test(`permits the old notice inventory only for explicit rollback inputs: ${target}`, async context => {
    const f = await fixture(context, target);
    const resources = target.startsWith('darwin-') ? `${candidateName}.app/Contents/Resources/` : '';
    f.files.delete(`${resources}LOCAL_TRANSLATION_NOTICES.txt`);
    await f.refresh();
    const files = new Map(f.files);
    files.set('candidate-manifest.json', await readFile(path.join(f.payload, 'candidate-manifest.json')));
    const options = { files, target, version };
    assert.throws(() => assertNativeCandidatePayload(options), /Candidate file set differs/);
    assertNativeCandidatePayload({ ...options, allowLegacyNotices: true });
    files.set('unexpected.txt', Buffer.from('extra'));
    assert.throws(() => assertNativeCandidatePayload({ ...options, allowLegacyNotices: true }), /Candidate file set differs/);
  });
}

test('accepts archives produced by the real platform tar utility', async context => {
  const f = await fixture(context, 'darwin-aarch64');
  execFileSync('tar', ['-czf', `${f.name}.tar.gz`, f.name], { cwd: f.parent });
  const archive = await readFile(path.join(f.parent, `${f.name}.tar.gz`));
  await writeFile(path.join(f.parent, 'SHA256SUMS'), `${fileDigest(archive)}  ${f.name}.tar.gz\n`);
  await verifyNativeArtifacts(f.options);
});

async function committedFixture(context) {
  const f = await fixture(context);
  await writeFile(path.join(f.directory, '.gitignore'), 'target/\nartifacts/\n');
  await mkdir(path.join(f.directory, 'scripts/lib'), { recursive: true });
  for (const name of ['check-native-release-policy.mjs', 'lib/native-package.mjs', 'ci/workflows.mjs', 'ci/plan.mjs', 'ci/yaml.mjs']) {
    await mkdir(path.dirname(path.join(f.directory, 'scripts', name)), { recursive: true });
    await writeFile(path.join(f.directory, 'scripts', name), await readFile(new URL(name, import.meta.url)));
  }
  const git = (...args) => execFileSync('git', args, { cwd: f.directory, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  git('init', '--quiet');
  git('add', '.');
  git('-c', 'user.name=Native Policy Fixture', '-c', 'user.email=native-policy@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '--quiet', '-m', 'Synthetic candidate inputs');
  delete f.options.currentCommit;
  f.options.commit = git('rev-parse', 'HEAD');
  return { ...f, git };
}

test('default CLI binds only clean committed inputs and permits ignored build caches', async context => {
  const f = await committedFixture(context);
  await writeFile(path.join(f.directory, 'target/ignored-build-cache'), 'cache');
  const output = path.join(f.directory, 'artifacts/cli-output');
  const result = spawnSync(process.execPath, [path.join(f.directory, 'scripts/check-native-release-policy.mjs'), 'verify-artifacts', '--target', f.options.target, '--commit', f.options.commit, '--output', output], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  const manifest = JSON.parse(await readFile(path.join(output, 'native-build-manifest.json'), 'utf8'));
  assert.equal(manifest.sourceCommit, f.options.commit);
});

for (const [name, mutate] of [
  ['unstaged Native source', async f => writeFile(path.join(f.directory, 'apps/native/internal/host/profile.go'), `package host\nconst Version = "${version}"\n// dirty\n`)],
  ['staged Native source', async f => { await writeFile(path.join(f.directory, 'apps/native/uncommitted.go'), 'package native\n'); f.git('add', 'apps/native/uncommitted.go'); }],
  ['untracked Native source', async f => writeFile(path.join(f.directory, 'apps/native/untracked.go'), 'package native\n')],
  ['untracked shared resource', async f => { await mkdir(path.join(f.directory, 'packages/desktop-resources'), { recursive: true }); await writeFile(path.join(f.directory, 'packages/desktop-resources/untracked-input.json'), '{}'); }],
  ['untracked shared contract', async f => { await mkdir(path.join(f.directory, 'packages/shared-types'), { recursive: true }); await writeFile(path.join(f.directory, 'packages/shared-types/untracked-schema.json'), '{}'); }],
  ['modified packaging script', async f => writeFile(path.join(f.directory, 'scripts/lib/native-package.mjs'), '// dirty script\n')],
  ['uncommitted checkout byte policy', async f => writeFile(path.join(f.directory, '.gitattributes'), '*.txt text eol=crlf\n')],
]) {
  test(`default provenance rejects ${name} before artifact output`, async context => {
    const f = await committedFixture(context);
    await mutate(f);
    await assert.rejects(verifyNativeArtifacts(f.options), /source inputs are dirty or untracked/);
    await assert.rejects(readdir(path.join(f.directory, 'artifacts')), { code: 'ENOENT' });
  });
}

test('default CLI rejects dirty inputs without emitting HEAD-bound evidence', async context => {
  const f = await committedFixture(context);
  await writeFile(path.join(f.directory, 'apps/native/untracked.go'), 'package native\n');
  const output = path.join(f.directory, 'artifacts/rejected');
  const result = spawnSync(process.execPath, [path.join(f.directory, 'scripts/check-native-release-policy.mjs'), 'verify-artifacts', '--target', f.options.target, '--commit', f.options.commit, '--output', output], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /source inputs are dirty or untracked/);
  await assert.rejects(readdir(output), { code: 'ENOENT' });
});

for (const existingLeaf of [false, true]) {
  test(`rejects output ancestor symlink with ${existingLeaf ? 'existing' : 'absent'} leaf`, async context => {
    const f = await fixture(context);
    const outside = path.join(f.directory, 'outside');
    await mkdir(outside);
    if (existingLeaf) await mkdir(path.join(outside, 'leaf'));
    try { await symlink(outside, path.join(f.directory, 'linked-output'), 'junction'); }
    catch (error) { if (error.code === 'EPERM') return context.skip('OS disallows unprivileged symlink creation'); throw error; }
    f.options.output = path.join(f.directory, 'linked-output/leaf');
    await assert.rejects(verifyNativeArtifacts(f.options), /ancestors.*without symlinks/);
    assert.deepEqual(await readdir(outside), existingLeaf ? ['leaf'] : []);
    if (existingLeaf) assert.deepEqual(await readdir(path.join(outside, 'leaf')), []);
  });
}

for (const [name, mutate] of [
  ['claim of production readiness', async f => { f.manifest.productionReady = true; await f.refresh(); }],
  ['claim of signing', async f => { f.manifest.signed = true; await f.refresh(); }],
  ['claim of notarization', async f => { f.manifest.notarized = true; await f.refresh(); }],
  ['production identity', async f => { f.manifest.identity = 'com.westng.liberty'; await f.refresh(); }],
  ['wrong runtime default', async f => { f.manifest.defaultRuntimeProfile = 'security-candidate'; await f.refresh(); }],
  ['wrong target', async f => { f.manifest.target = 'darwin-x64'; await f.refresh(); }],
  ['wrong source commit', async f => { f.options.currentCommit = 'b'.repeat(40); }],
  ['wrong version', async f => { f.manifest.version = '9.8.6'; await f.refresh(); }],
  ['unknown package metadata', async f => { f.manifest.installer = true; await f.refresh(); }],
  ['extra executable with matching digest', async f => { f.files.set('installer.exe', Buffer.from('unreviewed')); await f.refresh(); }],
  ['missing local translation notices', async f => { f.files.delete('LOCAL_TRANSLATION_NOTICES.txt'); await rm(path.join(f.payload, 'LOCAL_TRANSLATION_NOTICES.txt')); await f.refresh(); }],
  ['modified local translation notices and stale manifest', async f => { await writeFile(path.join(f.payload, 'LOCAL_TRANSLATION_NOTICES.txt'), 'tampered'); }],
  ['modified binary and stale manifest', async f => { await writeFile(path.join(f.payload, f.binary), Buffer.from('tampered')); }],
  ['wrong binary architecture with matching digest', async f => { f.files.get(f.binary).writeUInt16LE(0xaa64, 68); await f.refresh(); }],
  ['missing archive', async f => { await rm(path.join(f.parent, `${f.name}.tar.gz`)); }],
  ['unexpected sibling asset', async f => { await writeFile(path.join(f.parent, 'extra.zip'), 'bad'); }],
  ['second candidate package', async f => { await mkdir(path.join(f.directory, 'target/native/packages', `${f.name}-old`)); }],
  ['checksum mismatch', async f => { await writeFile(path.join(f.parent, 'SHA256SUMS'), '0'.repeat(64)); }],
  ['missing archive file with fresh checksum', async f => { await f.refresh([{ name: `${f.name}/README.txt`, bytes: f.files.get('README.txt') }]); }],
  ['changed archive bytes with fresh checksum', async f => {
    const bytes = await readFile(path.join(f.payload, 'candidate-manifest.json'));
    const records = [...f.files, ['candidate-manifest.json', bytes]].map(([name, bytes]) => ({ name: `${f.name}/${name}`, bytes }));
    records[0].bytes = Buffer.from('archive differs');
    await f.refresh(records);
  }],
  ['existing output content', async f => { f.options.output = f.payload; }],
]) {
  test(`rejects authenticated candidate mutation: ${name}`, async context => {
    const f = await fixture(context);
    await mutate(f);
    await assert.rejects(verifyNativeArtifacts(f.options));
    await assert.rejects(readdir(path.join(f.directory, 'artifacts/native', f.options.target)), { code: 'ENOENT' });
  });
}

for (const [name, records] of [
  ['traversal', [{ name: '../escape', bytes: Buffer.from('bad') }]],
  ['absolute path', [{ name: '/escape', bytes: Buffer.from('bad') }]],
  ['symbolic link', [{ name: 'sample', type: '2' }]],
  ['hard link', [{ name: 'sample', type: '1' }]],
  ['duplicate path', [{ name: 'sample' }, { name: 'sample' }]],
  ['unexpected directory', [{ name: 'extra/', type: '5' }, { name: 'sample' }]],
]) {
  test(`rejects archive ${name} before extraction`, () => assert.throws(() => readNativeArchive(tar(records))));
}

test('rejects a symlink replacing the authenticated archive', async context => {
  const f = await fixture(context);
  const archivePath = path.join(f.parent, `${f.name}.tar.gz`);
  const outside = path.join(f.directory, 'outside.tar.gz');
  await writeFile(outside, await readFile(archivePath));
  await rm(archivePath);
  try { await symlink(outside, archivePath); }
  catch (error) { if (error.code === 'EPERM') return context.skip('OS disallows unprivileged symlink creation'); throw error; }
  await assert.rejects(verifyNativeArtifacts(f.options), /regular files/);
});

test('candidate verification CLI fails closed on unknown or incomplete flags', () => {
  for (const args of [['--publish'], ['verify-artifacts'], ['verify-artifacts', '--target', 'windows-x64', '--commit', commit, '--output', 'artifacts/native/windows-x64', '--force']]) {
    const result = spawnSync(process.execPath, [fileURLToPath(new URL('./check-native-release-policy.mjs', import.meta.url)), ...args], { encoding: 'utf8' });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /Expected verify-artifacts/);
  }
});
