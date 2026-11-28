import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { gzipSync } from 'node:zlib';
import { candidateIdentity, candidateName, candidatePlist, fileDigest, nativeTargets } from './lib/native-package.mjs';
import { assembleRelease, assertCandidateRun, assertFreshSourceValidation, assertMainPublication, assertPrereleaseVersion, assertRecoveryRun, assertWorkflowArtifacts, checksums, describe, jsonBytes, latestAttemptJobs, positiveId, publishRelease, recoveryBundleNames, releaseAssetNames, releaseMarkers, releaseNotes, releaseTargets, resolveTag, restoreReleaseBackup, verifyReleaseBundle } from './lib/native-release.mjs';
import { extractWorkflowZip, GitHubReleaseClient } from './publish-native-release.mjs';
import { fullSuites } from './ci/plan.mjs';
import { installerManifest, installerName } from './lib/native-installers.mjs';
import { windowsExecutableFixture, windowsIconFixture } from './test-fixtures/native-windows.mjs';

const { version } = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));
const sourceCommit = 'a'.repeat(40);
const repository = 'example/liberty';
const license = Buffer.from('Synthetic project license\n');
const notices = Buffer.from('Synthetic module and Go toolchain notices\n');
const tag = `v${version}`;
const environment = { GITHUB_ACTIONS: 'true', GITHUB_EVENT_NAME: 'workflow_dispatch', GITHUB_REF: 'refs/heads/main', GITHUB_REPOSITORY: repository, GITHUB_SHA: 'b'.repeat(40), GITHUB_RUN_ID: '456' };

function tar(files) {
  const blocks = [];
  for (const [name, bytes] of files) {
    const header = Buffer.alloc(512);
    const slash = name.length > 100 ? name.lastIndexOf('/') : -1;
    header.write(slash < 0 ? name : name.slice(slash + 1), 0, 100);
    if (slash >= 0) header.write(name.slice(0, slash), 345, 155);
    header.write('0000644\0', 100);
    header.write('0000000\0', 108);
    header.write('0000000\0', 116);
    header.write(`${bytes.length.toString(8).padStart(11, '0')}\0`, 124);
    header.write('00000000000\0', 136);
    header.fill(32, 148, 156);
    header.write('0', 156);
    header.write('ustar\0', 257);
    header.write('00', 263);
    header.write(`${header.reduce((sum, byte) => sum + byte, 0).toString(8).padStart(6, '0')}\0 `, 148);
    blocks.push(header, bytes, Buffer.alloc((512 - bytes.length % 512) % 512));
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}

function runFixture() {
  const run = { id: 123, run_attempt: 1, head_sha: sourceCommit, path: '.github/workflows/build-native.yml', event: 'workflow_dispatch', status: 'completed', conclusion: 'success', repository: { id: 8, full_name: repository }, head_repository: { id: 8, full_name: repository } };
  const names = ['Select candidate target', 'Required complete validation / full-gate', ...fullSuites.map(suite => `Required complete validation / Full (${suite}) / ${suite}`), ...releaseTargets.map(target => `Candidate (${target})`)];
  const jobs = names.map(name => ({ name, status: 'completed', conclusion: 'success' }));
  const artifactNames = [...releaseTargets.map(target => `liberty-native-candidate-${target}-${sourceCommit}`), `liberty-inventories-${sourceCommit}`];
  const artifacts = artifactNames.map((name, index) => ({ id: index + 1, name, size_in_bytes: 100, digest: 'sha256:' + 'c'.repeat(64), expired: false, workflow_run: { id: 123, head_sha: sourceCommit, repository_id: 8, head_repository_id: 8 } }));
  return { run, jobs, artifacts };
}

function fixture() {
  const { artifacts } = runFixture();
  const context = { repository, sourceCommit, version, tag, license, candidateRunId: 123, candidateRunAttempt: 1, workflowSourceCommit: environment.GITHUB_SHA, publicationRunId: 456, artifacts, replaceReleaseId: null, stableLatest: { id: 9, tag: 'v1.2.3' } };
  const payload = new Map();
  for (const target of releaseTargets) {
    const mac = target.startsWith('darwin-');
    const resources = mac ? `${candidateName}.app/Contents/Resources/` : '';
    const binary = mac ? `${candidateName}.app/Contents/MacOS/liberty-native` : `${candidateName}.exe`;
    const program = mac ? Buffer.alloc(256) : windowsExecutableFixture();
    if (mac) { program.writeUInt32LE(0xfeedfacf); program.writeUInt32LE(nativeTargets[target].machine, 4); }
    const files = new Map([[binary, program], ['README.txt', Buffer.from('Synthetic unsigned candidate\n')], [`${resources}LICENSE`, license], [`${resources}THIRD_PARTY_NOTICES.txt`, notices], [`${resources}LOCAL_TRANSLATION_NOTICES.txt`, Buffer.from('Synthetic local translation notices\n')], [`${resources}${mac ? 'icon.icns' : 'icon.ico'}`, mac ? Buffer.from('Synthetic icon') : windowsIconFixture], ...(mac ? [[`${candidateName}.app/Contents/Info.plist`, Buffer.from(candidatePlist(version))]] : [])]);
    const manifest = jsonBytes({ format: 1, channel: 'candidate', identity: candidateIdentity, version, target, productionReady: false, signed: false, notarized: false, defaultRuntimeProfile: 'production', availableRuntimeProfiles: ['production'], files: [...files].map(([path, bytes]) => ({ path, bytes: bytes.length, sha256: fileDigest(bytes) })) });
    files.set('candidate-manifest.json', manifest);
    const name = `liberty-native-candidate-${version}-${target}`;
    const archiveName = `${name}.tar.gz`;
    const archive = tar([...files].map(([file, bytes]) => [`${name}/${file}`, bytes]));
    payload.set(archiveName, archive);
    payload.set(`candidate-manifest-${target}.json`, manifest);
    payload.set(`native-build-manifest-${target}.json`, jsonBytes({ format: 1, sourceCommit, version, target, channel: 'candidate', productionReady: false, signed: false, notarized: false, productionSecurityClearance: false, files: [describe(archiveName, archive), describe('candidate-manifest.json', manifest)] }));
    const installer = Buffer.alloc(1024);
    if (mac) installer.write('koly', installer.length - 512);
    else Buffer.from('d0cf11e0a1b11ae1', 'hex').copy(installer);
    payload.set(installerName(version, target), installer);
    payload.set(`installer-manifest-${target}.json`, jsonBytes(installerManifest({ version, target, sourceCommit, archive, installer })));
  }
  payload.set('native-go-NOTICES.txt', notices);
  payload.set('liberty-native.cdx.json', jsonBytes({ bomFormat: 'CycloneDX', specVersion: '1.5', metadata: { component: { name: 'Liberty Native', version } }, components: [{ 'bom-ref': 'pkg:golang/example/module@v1.0.0', purl: 'pkg:golang/example/module@v1.0.0' }] }));
  const files = assembleRelease(payload, context);
  return { context, payload, files, notes: releaseMarkers(context, files).join('\n') + '\n\nSynthetic bilingual release: 中文 / English\n' };
}

class FakeGitHub {
  constructor(f) {
    this.releases = [];
    this.tagCommit = sourceCommit;
    this.stable = { id: 9, tag_name: 'v1.2.3', draft: false, prerelease: false };
    this.nextReleaseId = 100;
    this.nextAssetId = 1000;
    this.bytes = new Map();
    this.mutations = [];
    this.downloads = [];
    this.f = f;
  }
  async list(endpoint) { assert.equal(endpoint, 'releases'); return structuredClone(this.releases); }
  async json(endpoint, options = {}) {
    const method = options.method ?? 'GET';
    if (method !== 'GET') this.mutations.push({ endpoint, method, body: options.body });
    if (endpoint === 'releases/latest') return structuredClone(this.stable);
    if (endpoint.startsWith('git/ref/tags/')) return this.tagCommit ? { object: { type: 'commit', sha: this.tagCommit } } : null;
    if (endpoint.startsWith('releases/tags/')) return structuredClone(this.releases.find(release => release.tag_name === tag) ?? null);
    if (endpoint === 'releases' && method === 'POST') {
      if (this.failCreate) { this.failCreate = false; throw new Error('Synthetic create interruption'); }
      const release = { id: this.nextReleaseId++, ...options.body, author: { login: this.humanToken ? 'synthetic-user' : 'github-actions[bot]', type: this.humanToken ? 'User' : 'Bot' }, assets: [], published_at: null };
      this.releases.push(release);
      return structuredClone(release);
    }
    const id = Number(endpoint.split('/').at(-1));
    const release = this.releases.find(release => release.id === id);
    if (method === 'DELETE') { assert.ok(release); this.releases = this.releases.filter(release => release.id !== id); return null; }
    if (method === 'PATCH') {
      assert.ok(release);
      Object.assign(release, options.body, { published_at: '2026-11-24T01:00:00Z' });
      this.tagCommit ??= options.body.target_commitish;
    }
    return structuredClone(release ?? null);
  }
  async upload(releaseId, name, bytes) {
    if (this.failUploadAt === this.mutations.filter(item => item.method === 'UPLOAD').length + 1) { this.failUploadAt = null; throw new Error('Synthetic upload interruption'); }
    this.mutations.push({ method: 'UPLOAD', releaseId, name });
    const release = this.releases.find(release => release.id === releaseId);
    assert.ok(release && release.draft);
    assert.ok(!release.assets.some(asset => asset.name === name), 'No overwrites');
    const asset = { id: this.nextAssetId++, name, state: 'uploaded', size: bytes.length, digest: `sha256:${fileDigest(bytes)}`, browser_download_url: `https://github.com/${repository}/releases/download/${tag}/${name}` };
    release.assets.push(asset);
    this.bytes.set(asset.id, bytes);
    return structuredClone(asset);
  }
  async asset(asset, authenticated) {
    this.downloads.push({ id: asset.id, authenticated });
    if (this.badDraftDownload && authenticated || this.badPublicDownload && !authenticated) return Buffer.from('Synthetic mismatched bytes');
    return this.bytes.get(asset.id);
  }
  addManual(f, id = 99) {
    f.context.replaceReleaseId = id;
    f.notes = releaseMarkers(f.context, f.files).join('\n') + '\nSynthetic migrated release\n';
    const before = JSON.parse(f.files.get('release-manifest.json'));
    Object.assign(before, { format: 1, releaseId: id });
    const previousFiles = new Map(f.payload);
    previousFiles.set('release-manifest.json', jsonBytes(before));
    previousFiles.set('SHA256SUMS', Buffer.from(checksums(previousFiles)));
    const previousRelease = { id, tag_name: tag, target_commitish: sourceCommit, name: `Liberty ${tag}`, body: `<!-- liberty-release-commit:${sourceCommit} -->\nSynthetic original release notes\n`, draft: false, prerelease: true, immutable: false, author: { login: 'synthetic-user', type: 'User' }, published_at: '2026-11-23T23:23:53Z', assets: [...previousFiles].map(([name, bytes]) => {
      const asset = { id: this.nextAssetId++, name, state: 'uploaded', size: bytes.length, digest: `sha256:${fileDigest(bytes)}`, download_count: 0 };
      this.bytes.set(asset.id, bytes);
      return asset;
    }) };
    this.releases.push(structuredClone(previousRelease));
    return { previousRelease, previousFiles };
  }
}

const publish = (f, api, more = {}) => publishRelease({ api, context: f.context, files: f.files, notes: f.notes, ...more });

function recoveryFixture() {
  const f = fixture();
  const api = new FakeGitHub(f);
  const previous = api.addManual(f);
  const oldContext = { ...f.context };
  const bundle = new Map([...f.files].map(([name, bytes]) => [`assets/${name}`, bytes]));
  bundle.set('plan.json', jsonBytes({ ...oldContext, assets: verifyReleaseBundle(f.files, f.context), hasPreviousRelease: true }));
  bundle.set('notes.md', Buffer.from(f.notes));
  bundle.set('previous/release.json', jsonBytes(previous.previousRelease));
  for (const name of ['release-manifest.json', 'SHA256SUMS']) bundle.set(`previous/${name}`, previous.previousFiles.get(name));
  f.context = { ...f.context, recoveryRunId: oldContext.publicationRunId, publicationRunId: 789, workflowSourceCommit: 'c'.repeat(40) };
  api.releases = [];
  return { f, api, previous, oldContext, bundle };
}

test('publication dispatch requires Actions, explicit main, exact SHA and valid IDs', () => {
  assertMainPublication(environment);
  for (const [key, value] of [['GITHUB_ACTIONS', 'false'], ['GITHUB_REF', 'refs/tags/v2.0.0-alpha.1'], ['GITHUB_REF', 'refs/heads/other'], ['GITHUB_EVENT_NAME', 'pull_request'], ['GITHUB_SHA', 'main'], ['GITHUB_RUN_ID', '0'], ['GITHUB_REPOSITORY', '../other']]) assert.throws(() => assertMainPublication({ ...environment, [key]: value }));
  for (const id of ['0', '-1', '1e3', '1;echo', '9007199254740993']) assert.throws(() => positiveId(id));
  for (const value of ['1.2.3', '2.0.0', '2.0.0-alpha.01', '2.0.0-alpha.1+other', '3.0.0-alpha.1']) assert.throws(() => assertPrereleaseVersion(value));
});

test('candidate proof requires all fifteen full suites and three host candidates', () => {
  const { run, jobs, artifacts } = runFixture();
  assert.equal(assertCandidateRun(run, jobs, repository, run.id), sourceCommit);
  assertWorkflowArtifacts(artifacts, run);
  for (const mutation of [value => { value.conclusion = 'failure'; }, value => { value.head_repository.full_name = 'other/fork'; }, value => { value.path = '.github/workflows/other.yml'; }, value => { value.head_sha = 'main'; }]) {
    const changed = structuredClone(run); mutation(changed);
    assert.throws(() => assertCandidateRun(changed, jobs, repository, run.id));
  }
  for (const conclusion of ['skipped', 'cancelled', 'failure']) {
    const changed = structuredClone(jobs); changed.find(job => job.name.includes('(security)')).conclusion = conclusion;
    assert.throws(() => assertCandidateRun(run, changed, repository, run.id));
  }
  assert.throws(() => assertCandidateRun(run, jobs.slice(1), repository, run.id));
  for (const mutate of [value => { value[0].expired = true; }, value => { value[0].digest = null; }, value => { value[0].workflow_run.head_sha = 'd'.repeat(40); }, value => { value[0].workflow_run.head_repository_id = 99; }, value => { value.push(value[0]); }]) {
    const changed = structuredClone(artifacts); mutate(changed);
    assert.throws(() => assertWorkflowArtifacts(changed, run));
  }
});

test('publishing or resuming requires every exact source check within the last 24 hours', () => {
  const now = new Date('2026-11-24T02:00:00Z');
  const names = ['Required source validation / full-gate', ...fullSuites.map(suite => `Required source validation / Full (${suite}) / ${suite}`)];
  const jobs = names.map(name => ({ name, status: 'completed', conclusion: 'success', completed_at: '2026-11-24T01:00:00Z' }));
  assertFreshSourceValidation(jobs, now);
  for (const change of [value => { value.pop(); }, value => { value[0].conclusion = 'skipped'; }, value => { value[0].completed_at = '2026-11-22T01:00:00Z'; }, value => { value[0].completed_at = null; }, value => { value.push(value[0]); }]) {
    const changed = structuredClone(jobs); change(changed);
    assert.throws(() => assertFreshSourceValidation(changed, now));
  }
});

test('failed-job retries preserve prior successful gates but supersede an older job outcome', async () => {
  const api = { list: async endpoint => endpoint.includes('/attempts/2/') ? [{ name: 'publish', conclusion: 'success' }] : [{ name: 'source gate', conclusion: 'success' }, { name: 'publish', conclusion: 'failure' }] };
  const jobs = await latestAttemptJobs(api, 123, 2);
  assert.equal(jobs.find(job => job.name === 'source gate').conclusion, 'success');
  assert.equal(jobs.find(job => job.name === 'publish').conclusion, 'success');
  api.list = async () => [{ name: 'same' }, { name: 'same' }];
  await assert.rejects(latestAttemptJobs(api, 123, 1), /Duplicate/);
});

test('recovery requires an exact completed main publication with fresh source gates and a preserved stage', () => {
  const { run } = runFixture();
  Object.assign(run, { id: 456, head_sha: 'b'.repeat(40), path: '.github/workflows/release-native.yml', head_branch: 'main', conclusion: 'failure', run_attempt: 2 });
  const completed_at = new Date(Date.now() - 60_000).toISOString();
  const names = ['Required source validation / full-gate', ...fullSuites.map(suite => `Required source validation / Full (${suite}) / ${suite}`)];
  const jobs = [...names.map(name => ({ name, status: 'completed', conclusion: 'success', completed_at })), { name: 'Verify and preserve release inputs', status: 'completed', conclusion: 'success', run_attempt: 1 }];
  assert.deepEqual(assertRecoveryRun(run, jobs, repository, 456), { workflowSourceCommit: run.head_sha, bundleAttempt: 1 });
  for (const mutate of [value => { value.id = 457; }, value => { value.head_repository.full_name = 'other/fork'; }, value => { value.head_branch = 'other'; }, value => { value.path = '.github/workflows/build-native.yml'; }, value => { value.status = 'in_progress'; }]) {
    const changed = structuredClone(run); mutate(changed);
    assert.throws(() => assertRecoveryRun(changed, jobs, repository, 456));
  }
  for (const mutate of [value => { value.pop(); }, value => { value.at(-1).conclusion = 'failure'; }, value => { value.at(-1).run_attempt = 3; }, value => { value[0].conclusion = 'skipped'; }, value => { value[0].completed_at = '2020-01-01T00:00:00Z'; }]) {
    const changed = structuredClone(jobs); mutate(changed);
    assert.throws(() => assertRecoveryRun(run, changed, repository, 456));
  }
});

test('new main publication imports the exact old backup and restores the deleted release as bot', async () => {
  const { f, api, previous, oldContext, bundle } = recoveryFixture();
  assert.deepEqual([...bundle.keys()].sort(), recoveryBundleNames(version));
  const restored = restoreReleaseBackup(bundle, f.files, f.context, oldContext.workflowSourceCommit);
  assert.deepEqual(restored.previousRelease, previous.previousRelease);
  for (const [name, bytes] of previous.previousFiles) assert.ok(bytes.equals(restored.previousFiles.get(name)));
  const originalJson = api.json.bind(api);
  api.json = async (endpoint, options) => {
    if (options?.method === 'POST') assert.equal(options.body.target_commitish, 'main', 'Historical creation targets require a different GitHub permission');
    return originalJson(endpoint, options);
  };
  const result = await publish(f, api, restored);
  assert.equal(result.publisher, 'github-actions[bot]');
  assert.equal(result.recoveryRunId, oldContext.publicationRunId);
  assert.equal(api.tagCommit, sourceCommit);
  assert.equal(api.releases[0].target_commitish, 'main');
  assert.equal(api.mutations.filter(item => item.method === 'DELETE').length, 0);
});

for (const [label, mutate] of [
  ['publication ID', value => { value.publicationRunId = 457; }],
  ['workflow source', value => { value.workflowSourceCommit = 'd'.repeat(40); }],
  ['candidate run', value => { value.candidateRunId = 124; }],
  ['source commit', value => { value.sourceCommit = 'e'.repeat(40); }],
  ['original Release ID', value => { value.replaceReleaseId = 98; }],
  ['missing original backup', value => { value.hasPreviousRelease = false; }],
  ['candidate artifact proof', value => { value.artifacts[0].digest = 'sha256:' + 'e'.repeat(64); }],
]) test(`recovery refuses a different ${label}`, () => {
  const { f, oldContext, bundle } = recoveryFixture();
  const plan = JSON.parse(bundle.get('plan.json')); mutate(plan);
  bundle.set('plan.json', jsonBytes(plan));
  assert.throws(() => restoreReleaseBackup(bundle, f.files, f.context, oldContext.workflowSourceCommit));
});

test('recovery refuses missing or modified original bytes and unknown files', () => {
  for (const mutate of [value => { value.delete('previous/SHA256SUMS'); }, value => { value.set('previous/SHA256SUMS', Buffer.from('Corrupt original checksums')); }, value => { value.set('assets/native-go-NOTICES.txt', Buffer.from('Corrupt notices')); }, value => { value.set('unknown.txt', Buffer.from('Unexpected file')); }]) {
    const { f, oldContext, bundle } = recoveryFixture(); mutate(bundle);
    assert.throws(() => restoreReleaseBackup(bundle, f.files, f.context, oldContext.workflowSourceCommit));
  }
});

test('three independent archives and installers assemble into nineteen verified assets', () => {
  const f = fixture();
  assert.deepEqual([...f.files.keys()].sort(), releaseAssetNames(version));
  assert.equal(verifyReleaseBundle(f.files, f.context).length, 19);
  const manifest = JSON.parse(f.files.get('release-manifest.json'));
  assert.equal(manifest.releaseId, undefined);
  assert.equal(manifest.createdAt, undefined);
  assert.ok(assembleRelease(f.payload, { ...f.context, publicationRunId: 999 }).get('release-manifest.json').equals(f.files.get('release-manifest.json')));
});

test('publication refuses archive-only payloads and modified installer claims or bytes', () => {
  for (const change of [
    payload => { payload.delete(installerName(version, 'windows-x64')); },
    payload => { payload.delete('installer-manifest-darwin-aarch64.json'); },
    payload => { payload.set(installerName(version, 'windows-x64'), Buffer.from('corrupt MSI')); },
    payload => { const name = 'installer-manifest-darwin-x64.json'; const value = JSON.parse(payload.get(name)); value.notarized = true; payload.set(name, jsonBytes(value)); },
  ]) {
    const f = fixture();
    change(f.payload);
    assert.throws(() => assembleRelease(f.payload, f.context));
  }
});

test('real repository changelog generates complete bilingual notes and publishes the bound release', async () => {
  const f = fixture();
  const changelog = await readFile(new URL('../CHANGELOG.md', import.meta.url), 'utf8');
  f.notes = releaseNotes(changelog, f.context, f.files);
  assert.ok(f.notes.includes(`\n## ${version} - `));
  assert.ok(f.notes.includes('### 中文') && f.notes.includes('### English'));
  assert.ok(!f.notes.includes('## Unreleased'));
  assert.equal(f.notes.match(/^## /gm)?.length, 1);
  assert.ok(releaseMarkers(f.context, f.files).every(marker => f.notes.includes(marker)));
  const api = new FakeGitHub(f);
  const result = await publish(f, api);
  assert.equal(result.publisher, 'github-actions[bot]');
  assert.ok(api.releases[0].body.includes(f.notes));
});

test('release notes accept plain and bracketed exact version headings and stop at the next release', () => {
  const f = fixture();
  for (const heading of [`## ${version} - 2026-11-24`, `## [${version}] - 2026-11-24`, `## v${version} - 2026-11-24`, `## ${version}`]) {
    for (const newline of ['\n', '\r\n']) {
      const changelog = ['# Synthetic changelog', '## Unreleased', 'Excluded future changes', heading, '### 中文', '合成变更。', '### English', 'Synthetic changes.', '## 1.2.3 - 2026-01-01', 'Excluded previous release'].join(newline);
      const notes = releaseNotes(changelog, f.context, f.files);
      assert.ok(notes.includes(heading) && notes.includes('Synthetic changes.'));
      assert.ok(!notes.includes('Excluded future changes') && !notes.includes('Excluded previous release'));
    }
  }
});

test('release notes contain only the version changelog and hidden provenance markers', () => {
  const f = fixture();
  const section = `## ${version} - 2026-11-24\n\n### 中文\n\n- 合成变更。[交付要求](docs/architecture/release-readiness.md)\n\n### English\n\n- Synthetic changes.\n`;
  const changelog = `# Changelog\n\n## Unreleased\n\nFuture changes.\n\n${section}\n## 1.2.3\n\nPrevious changes.\n`;
  const expectedSection = section.replace('](docs/architecture/', `](https://github.com/${repository}/blob/${sourceCommit}/docs/architecture/`);
  assert.equal(releaseNotes(changelog, f.context, f.files), `${releaseMarkers(f.context, f.files).join('\n')}\n\n${expectedSection}`);
});

test('release notes reject a missing exact version or bilingual content outside its section', () => {
  const f = fixture();
  for (const changelog of [
    `## ${version}0 - 2026-11-24\n中文 English`,
    `### ${version} - 2026-11-24\n中文 English`,
    `## Unreleased\n中文 English\n## ${version} - 2026-11-24\nOnly English content`,
    `## ${version} - 2026-11-24\n中文内容\n## 1.2.3\nEnglish`,
    `## ${version} - 2026-11-24\n\n## 1.2.3\n中文 English`,
  ]) assert.throws(() => releaseNotes(changelog, f.context, f.files), /changelog lacks|both languages/);
});

for (const [name, mutate] of [
  ['wrong architecture', f => { const candidate = JSON.parse(f.payload.get('candidate-manifest-windows-x64.json')); candidate.target = 'darwin-x64'; f.payload.set('candidate-manifest-windows-x64.json', jsonBytes(candidate)); }],
  ['different source', f => { const build = JSON.parse(f.payload.get('native-build-manifest-darwin-x64.json')); build.sourceCommit = 'e'.repeat(40); f.payload.set('native-build-manifest-darwin-x64.json', jsonBytes(build)); }],
  ['production claim', f => { const build = JSON.parse(f.payload.get('native-build-manifest-windows-x64.json')); build.productionReady = true; f.payload.set('native-build-manifest-windows-x64.json', jsonBytes(build)); }],
  ['different notices', f => f.payload.set('native-go-NOTICES.txt', Buffer.from('Different synthetic notices'))],
  ['missing platform', f => f.payload.delete('native-build-manifest-darwin-x64.json')],
  ['unexpected installer', f => f.payload.set('installer.msi', Buffer.from('Synthetic unexpected file'))],
  ['legacy SBOM', f => { const sbom = JSON.parse(f.payload.get('liberty-native.cdx.json')); sbom.metadata.component.name = 'Liberty'; f.payload.set('liberty-native.cdx.json', jsonBytes(sbom)); }],
]) test(`rejects release input: ${name}`, () => { const f = fixture(); mutate(f); assert.throws(() => assembleRelease(f.payload, f.context)); });

test('bot creates a draft, uploads without overwrites, checks bytes and publishes exact ID', async () => {
  const f = fixture();
  const api = new FakeGitHub(f);
  api.tagCommit = null;
  const result = await publish(f, api);
  assert.equal(result.publisher, 'github-actions[bot]');
  assert.equal(result.assetCount, 19);
  assert.equal(result.publicDownloadsVerified, true);
  assert.equal(api.tagCommit, sourceCommit);
  assert.deepEqual(api.stable, { id: 9, tag_name: 'v1.2.3', draft: false, prerelease: false });
  const create = api.mutations.find(item => item.method === 'POST');
  const patch = api.mutations.find(item => item.method === 'PATCH');
  assert.equal(create.body.draft, true);
  assert.equal(create.body.make_latest, 'false');
  assert.equal(patch.endpoint, `releases/${result.releaseId}`);
  assert.equal(patch.body.tag_name, tag);
  assert.equal(patch.body.target_commitish, sourceCommit);
  assert.equal(patch.body.prerelease, true);
  assert.equal(patch.body.make_latest, 'false');
  assert.equal(api.downloads.filter(item => item.authenticated).length, 19);
  assert.equal(api.downloads.filter(item => !item.authenticated).length, 19);
});

test('a published bot release is idempotent and is never overwritten', async () => {
  const f = fixture(); const api = new FakeGitHub(f);
  const first = await publish(f, api);
  const mutations = api.mutations.length;
  const next = await publish(f, api);
  assert.equal(next.releaseId, first.releaseId);
  assert.equal(api.mutations.length, mutations);
});

test('upload interruption retains draft and resumes only missing exact assets', async () => {
  const f = fixture(); const api = new FakeGitHub(f);
  api.failUploadAt = 4;
  await assert.rejects(publish(f, api), /interruption/);
  assert.equal(api.releases[0].draft, true);
  assert.equal(api.releases[0].assets.length, 3);
  const id = api.releases[0].id;
  const result = await publish(f, api);
  assert.equal(result.releaseId, id);
  assert.equal(api.mutations.filter(item => item.method === 'UPLOAD').length, 19);
});

for (const [label, change] of [
  ['different existing checksum', api => { api.releases[0].assets[0].digest = 'sha256:' + 'f'.repeat(64); }],
  ['partial starter asset', api => { api.releases[0].assets[0].state = 'starter'; }],
  ['different input marker', api => { api.releases[0].body = '<!-- unrelated synthetic draft -->'; }],
  ['another draft author', api => { api.releases[0].author = { login: 'other-bot', type: 'Bot' }; }],
  ['unexpected asset', api => { api.releases[0].assets[0].name = 'unexpected.zip'; }],
]) test(`resumption refuses ${label} without changing remote assets`, async () => {
  const f = fixture(); const api = new FakeGitHub(f); api.failUploadAt = 2;
  await assert.rejects(publish(f, api));
  change(api); const before = api.mutations.length;
  await assert.rejects(publish(f, api));
  assert.equal(api.mutations.length, before);
});

test('personal token cannot publish a newly created draft', async () => {
  const f = fixture(); const api = new FakeGitHub(f); api.humanToken = true;
  await assert.rejects(publish(f, api), /bot token/);
  assert.ok(api.releases.every(release => release.draft));
  assert.equal(api.mutations.filter(item => item.method === 'PATCH').length, 0);
});

test('immutable tag and stable latest mismatches fail before writes', async () => {
  for (const change of [api => { api.tagCommit = 'd'.repeat(40); }, api => { api.stable.id = 10; }]) {
    const f = fixture(); const api = new FakeGitHub(f); change(api);
    await assert.rejects(publish(f, api));
    assert.equal(api.mutations.length, 0);
  }
});

test('download corruption prevents draft publication and public success claims', async () => {
  const f = fixture(); const api = new FakeGitHub(f); api.badDraftDownload = true;
  await assert.rejects(publish(f, api), /Uploaded bytes differ/);
  assert.equal(api.releases[0].draft, true);
  api.badDraftDownload = false; api.badPublicDownload = true;
  const reports = [];
  await assert.rejects(publish(f, api, { record: async value => reports.push(value) }), /Public download differs/);
  assert.ok(!reports.some(report => report.status === 'published'));
});

test('manual author migration requires exact ID and preserved verified old bytes', async () => {
  const f = fixture(); const api = new FakeGitHub(f);
  const previous = api.addManual(f);
  api.releases[0].assets[0].download_count = 20;
  const result = await publish(f, api, previous);
  assert.equal(result.previousReleaseId, 99);
  assert.notEqual(result.releaseId, 99);
  assert.equal(result.publisher, 'github-actions[bot]');
  assert.deepEqual(api.mutations.filter(item => item.method === 'DELETE').map(item => item.endpoint), ['releases/99']);
  for (const [name, bytes] of f.payload) assert.ok(bytes.equals(previous.previousFiles.get(name)), `Unchanged original candidate payload: ${name}`);
});

for (const [label, change] of [
  ['no explicit ID', (f, api, previous) => { f.context.replaceReleaseId = null; }],
  ['wrong explicit ID', f => { f.context.replaceReleaseId = 98; }],
  ['missing preserved backup', (f, api, previous) => { previous.previousFiles = null; }],
  ['modified original notes', (f, api) => { api.releases[0].body += 'Unexpected edit'; }],
  ['changed original asset', (f, api) => { api.releases[0].assets[0].digest = 'sha256:' + 'f'.repeat(64); }],
  ['stable release', (f, api) => { api.releases[0].prerelease = false; }],
  ['immutable release', (f, api, previous) => { api.releases[0].immutable = true; previous.previousRelease.immutable = true; }],
]) test(`manual migration refuses ${label} before deleting anything`, async () => {
  const f = fixture(); const api = new FakeGitHub(f); const previous = api.addManual(f);
  change(f, api, previous);
  await assert.rejects(publish(f, api, previous));
  assert.equal(api.mutations.length, 0);
  assert.equal(api.releases[0].id, 99);
});

test('interruption after original deletion resumes from the immutable original bundle', async () => {
  const f = fixture(); const api = new FakeGitHub(f); const previous = api.addManual(f);
  api.failCreate = true;
  await assert.rejects(publish(f, api, previous), /create interruption/);
  assert.equal(api.releases.length, 0);
  assert.equal(api.tagCommit, sourceCommit);
  const result = await publish(f, api, previous);
  assert.equal(result.publisher, 'github-actions[bot]');
  assert.equal(api.mutations.filter(item => item.method === 'DELETE').length, 1);
});

test('stale release listings cannot authorize deleting a manual Release edited after staging', async () => {
  const f = fixture(); const api = new FakeGitHub(f); const previous = api.addManual(f);
  const originalJson = api.json.bind(api);
  api.json = async (endpoint, options) => {
    const response = await originalJson(endpoint, options);
    if (endpoint === 'releases/99' && !options) response.body += 'Synthetic edit visible only through exact-ID read';
    return response;
  };
  await assert.rejects(publish(f, api, previous), /changed after/);
  assert.equal(api.mutations.length, 0);
});

test('missing original without a backup never creates or adopts a replacement', async () => {
  const f = fixture(); f.context.replaceReleaseId = 99; f.notes = releaseMarkers(f.context, f.files).join('\n');
  const api = new FakeGitHub(f);
  await assert.rejects(publish(f, api), /resume|backup/i);
  assert.equal(api.mutations.length, 0);
});

test('annotated tags resolve only to exact commits and loops are rejected', async () => {
  const api = { json: async endpoint => endpoint.startsWith('git/ref/') ? { object: { type: 'tag', sha: 'c'.repeat(40) } } : { object: { type: 'commit', sha: sourceCommit } } };
  assert.equal(await resolveTag(api, tag), sourceCommit);
  api.json = async () => ({ object: { type: 'tag', sha: 'c'.repeat(40) } });
  await assert.rejects(resolveTag(api, tag), /tag chain/);
});

async function zipFixture(context, names, symlink = false) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'liberty-release-zip-test-'));
  context.after(() => rm(directory, { recursive: true, force: true }));
  const output = path.join(directory, 'fixture.zip');
  execFileSync('python3', ['-c', `import json, stat, sys, zipfile
with zipfile.ZipFile(sys.argv[1], 'w') as archive:
    for name in json.loads(sys.argv[2]):
        entry = zipfile.ZipInfo(name)
        if sys.argv[3] == 'true':
            entry.create_system = 3
            entry.external_attr = (stat.S_IFLNK | 0o777) << 16
        archive.writestr(entry, b'Synthetic artifact bytes')`, output, JSON.stringify(names), String(symlink)], { stdio: ['ignore', 'pipe', 'pipe'] });
  return readFile(output);
}

test('Actions artifact downloads negotiate the JSON API and return the verified ZIP', async context => {
  const bytes = await zipFixture(context, ['candidate-manifest.json']);
  context.mock.method(globalThis, 'fetch', async (url, options) => {
    assert.equal(url, `https://api.github.com/repos/${repository}/actions/artifacts/42/zip`);
    // The Actions REST endpoint rejects octet-stream negotiation with HTTP 415,
    // even though its successful redirect returns binary archive bytes.
    if (options.headers.Accept !== 'application/vnd.github+json') return new Response(null, { status: 415 });
    return new Response(bytes, { headers: { 'Content-Type': 'application/zip' } });
  });
  const api = new GitHubReleaseClient(repository, 'synthetic-auth');
  const archive = await api.artifact({ id: 42, size_in_bytes: bytes.length, digest: `sha256:${fileDigest(bytes)}` });
  assert.ok(archive.equals(bytes));
  const files = await extractWorkflowZip(archive, ['candidate-manifest.json']);
  assert.equal(files.get('candidate-manifest.json').toString(), 'Synthetic artifact bytes');
});

test('release asset downloads retain octet-stream negotiation and public reads omit credentials', async context => {
  const bytes = Buffer.from('Synthetic release asset bytes');
  const asset = { id: 42, name: 'synthetic.txt', browser_download_url: `https://github.com/${repository}/releases/download/${tag}/synthetic.txt` };
  const requests = [];
  context.mock.method(globalThis, 'fetch', async (url, options) => {
    assert.equal(options.headers.Accept, 'application/octet-stream');
    requests.push({ url: String(url), authenticated: Object.hasOwn(options.headers, 'Authorization') });
    return new Response(bytes);
  });
  const api = new GitHubReleaseClient(repository, 'synthetic-auth');
  assert.ok((await api.asset(asset, true)).equals(bytes));
  assert.ok((await api.asset(asset, false)).equals(bytes));
  assert.deepEqual(requests, [
    { url: `https://api.github.com/repos/${repository}/releases/assets/42`, authenticated: true },
    { url: asset.browser_download_url, authenticated: false },
  ]);
});

test('artifact downloads still reject a different archive size or digest', async context => {
  const bytes = Buffer.from('Synthetic archive bytes');
  context.mock.method(globalThis, 'fetch', async () => new Response(bytes));
  const api = new GitHubReleaseClient(repository, 'synthetic-auth');
  await assert.rejects(api.artifact({ id: 42, size_in_bytes: bytes.length + 1, digest: `sha256:${fileDigest(bytes)}` }), /ZIP size differs/);
  await assert.rejects(api.artifact({ id: 42, size_in_bytes: bytes.length, digest: `sha256:${fileDigest(Buffer.from('Different archive bytes'))}` }), /ZIP checksum differs/);
});

test('API rejection reports the reason and redacts the credential', async context => {
  context.mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify({ message: 'Resource not accessible by integration: synthetic-auth\n' }), { status: 403 }));
  const api = new GitHubReleaseClient(repository, 'synthetic-auth');
  await assert.rejects(api.json('releases'), error => error.message.includes('403: Resource not accessible by integration: [redacted]') && !error.message.includes('synthetic-auth') && !error.message.includes('\n'));
});

test('recovery ZIP permits only explicit safe nested paths while flat artifacts remain flat', async context => {
  const names = ['assets/synthetic.txt', 'previous/release.json', 'plan.json'];
  const bytes = await zipFixture(context, names);
  assert.deepEqual([...(await extractWorkflowZip(bytes, names, { nested: true })).keys()], names);
  await assert.rejects(extractWorkflowZip(bytes, names));
  for (const name of ['assets/../outside', '/absolute', 'assets//file', 'assets/./file']) await assert.rejects(extractWorkflowZip(await zipFixture(context, [name]), [name], { nested: true }));
  await assert.rejects(extractWorkflowZip(await zipFixture(context, ['assets/link'], true), ['assets/link'], { nested: true }));
});

test('workflow ZIP extraction verifies exact entries and rejects traversal, links and duplicates', async context => {
  const valid = await zipFixture(context, ['candidate-manifest.json']);
  assert.equal((await extractWorkflowZip(valid, ['candidate-manifest.json'])).get('candidate-manifest.json').toString(), 'Synthetic artifact bytes');
  await assert.rejects(extractWorkflowZip(valid, ['other.json']));
  for (const name of ['../outside', 'dir/file', 'C:file']) await assert.rejects(extractWorkflowZip(await zipFixture(context, [name]), [name]));
  await assert.rejects(extractWorkflowZip(await zipFixture(context, ['same', 'same']), ['same']));
  await assert.rejects(extractWorkflowZip(await zipFixture(context, ['link'], true), ['link']));
});
