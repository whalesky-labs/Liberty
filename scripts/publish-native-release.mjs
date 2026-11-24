import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { appendFile, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { fileDigest } from './lib/native-package.mjs';
import { assertOsvExceptionsAreGoverned, OSV_EXCEPTION_CONFIG } from './lib/dependency-governance.mjs';
import { assembleRelease, assertAsset, assertCandidateRun, assertFreshSourceValidation, assertMainPublication, assertPrereleaseVersion, assertWorkflowArtifacts, describe, findRelease, jsonBytes, latestAttemptJobs, positiveId, publishRelease, releaseAssetNames, releaseMarkers, releaseNotes, releaseTargets, resolveTag, verifyPreviousRelease, verifyReleaseBundle } from './lib/native-release.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const bundleDirectory = path.join(root, 'artifacts/native-release');
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const sourceText = (commit, file) => execFileSync('git', ['show', `${commit}:${file}`], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 16 * 1024 * 1024 });

export class GitHubReleaseClient {
  constructor(repository, token) {
    assert.match(repository, /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9_][A-Za-z0-9_.-]*$/);
    assert.ok(token, 'Actions token is required');
    this.repository = repository;
    this.token = token;
  }
  async request(url, { method = 'GET', body, binary = false, authenticated = true, accept } = {}) {
    const headers = { Accept: accept ?? (binary ? 'application/octet-stream' : 'application/vnd.github+json'), 'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'liberty-native-publication' };
    if (authenticated) headers.Authorization = `Bearer ${this.token}`;
    if (body !== undefined) headers['Content-Type'] = Buffer.isBuffer(body) ? 'application/octet-stream' : 'application/json';
    let response;
    try { response = await fetch(url, { method, headers, body: body === undefined ? undefined : Buffer.isBuffer(body) ? body : JSON.stringify(body), signal: AbortSignal.timeout(90_000) }); }
    catch { throw new Error('GitHub release request failed'); }
    if (response.status === 404 && !binary && method === 'GET') return null;
    assert.ok(response.ok, `GitHub release request failed with status ${response.status}`);
    if (response.status === 204) return null;
    if (!binary) return response.json();
    const chunks = [];
    let length = 0;
    for await (const chunk of response.body) {
      length += chunk.length;
      assert.ok(length <= 512 * 1024 * 1024, 'GitHub download exceeds the verified input size limit');
      chunks.push(chunk);
    }
    return Buffer.concat(chunks);
  }
  json(endpoint, options) { return this.request(`https://api.github.com/repos/${this.repository}/${endpoint}`, options); }
  async list(endpoint, key) {
    const result = [];
    for (let page = 1; page <= 100; page++) {
      const url = new URL(`https://api.github.com/repos/${this.repository}/${endpoint}`);
      url.searchParams.set('per_page', '100');
      url.searchParams.set('page', String(page));
      const response = await this.request(url);
      const batch = key ? response?.[key] : response;
      assert.ok(Array.isArray(batch), 'Invalid GitHub list response');
      result.push(...batch);
      if (batch.length < 100) return result;
    }
    throw new Error('GitHub list exceeds the release verification bound');
  }
  async artifact(artifact) {
    // Actions negotiates its JSON REST API before redirecting to a binary ZIP.
    const bytes = await this.request(`https://api.github.com/repos/${this.repository}/actions/artifacts/${positiveId(artifact.id)}/zip`, { binary: true, accept: 'application/vnd.github+json' });
    assert.equal(bytes.length, artifact.size_in_bytes, 'Artifact ZIP size differs');
    assert.equal(`sha256:${fileDigest(bytes)}`, artifact.digest, 'Artifact ZIP checksum differs');
    return bytes;
  }
  async asset(asset, authenticated) {
    if (authenticated) return this.request(`https://api.github.com/repos/${this.repository}/releases/assets/${positiveId(asset.id)}`, { binary: true });
    const url = new URL(asset.browser_download_url);
    assert.equal(url.protocol, 'https:');
    assert.equal(url.hostname, 'github.com');
    assert.ok(url.pathname.startsWith(`/${this.repository}/releases/download/`));
    assert.equal(decodeURIComponent(url.pathname.split('/').at(-1)), asset.name);
    assert.ok(!url.username && !url.password && !url.search && !url.hash);
    return this.request(url, { binary: true, authenticated: false });
  }
  upload(releaseId, name, bytes) {
    assert.match(name, /^[A-Za-z0-9_.-]+$/, 'Unsafe release asset name');
    return this.request(`https://uploads.github.com/repos/${this.repository}/releases/${positiveId(releaseId)}/assets?name=${encodeURIComponent(name)}`, { method: 'POST', body: bytes });
  }
}

const zipExtractor = `import json, os, pathlib, stat, sys, zipfile
source, destination, expected = sys.argv[1], pathlib.Path(sys.argv[2]), set(json.loads(sys.argv[3]))
with zipfile.ZipFile(source) as archive:
    members = archive.infolist()
    names = [member.filename for member in members]
    if len(names) != len(set(names)) or set(names) != expected:
        raise ValueError('Workflow ZIP file set differs')
    if sum(member.file_size for member in members) > 512 * 1024 * 1024:
        raise ValueError('Workflow ZIP is too large')
    for member in members:
        name = member.filename
        mode = stat.S_IFMT(member.external_attr >> 16)
        if member.is_dir() or mode not in (0, stat.S_IFREG) or member.flag_bits & 1 or '/' in name or '\\\\' in name or ':' in name or name in ('.', '..') or '\\x00' in name:
            raise ValueError('Unsafe workflow ZIP entry')
        with open(destination / name, 'xb') as output:
            output.write(archive.read(member))
`;

export async function extractWorkflowZip(bytes, expectedNames) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'liberty-native-artifact-'));
  try {
    const zip = path.join(directory, 'input.zip');
    const output = path.join(directory, 'files');
    await mkdir(output);
    await writeFile(zip, bytes, { flag: 'wx', mode: 0o600 });
    execFileSync('python3', ['-c', zipExtractor, zip, output, JSON.stringify(expectedNames)], { stdio: ['ignore', 'pipe', 'pipe'], timeout: 120_000 });
    return new Map(await Promise.all(expectedNames.map(async name => [name, await readFile(path.join(output, name))])));
  } finally { await rm(directory, { recursive: true, force: true }); }
}

async function prepare(api, environment) {
  assertMainPublication(environment);
  assert.equal(git('rev-parse', 'HEAD'), environment.GITHUB_SHA, 'Workflow checkout differs');
  const runId = positiveId(environment.NATIVE_CANDIDATE_RUN_ID);
  const run = await api.json(`actions/runs/${runId}`);
  const jobs = await latestAttemptJobs(api, runId, run.run_attempt);
  const sourceCommit = assertCandidateRun(run, jobs, environment.GITHUB_REPOSITORY, runId);
  assertOsvExceptionsAreGoverned(sourceText(sourceCommit, OSV_EXCEPTION_CONFIG));
  git('merge-base', '--is-ancestor', sourceCommit, environment.GITHUB_SHA);
  const comparison = await api.json(`compare/${sourceCommit}...main`);
  assert.equal(comparison?.merge_base_commit?.sha, sourceCommit, 'Candidate source is not in remote main history');
  const workspace = JSON.parse(sourceText(sourceCommit, 'package.json'));
  assert.equal(workspace.packageManager, 'pnpm@10.30.3');
  const version = workspace.version;
  const cargoLock = sourceText(sourceCommit, 'Cargo.lock').split(/\n(?=\[\[package\]\]\n)/).find(block => /^name = "liberty"$/m.test(block));
  const tag = assertPrereleaseVersion(version, [
    JSON.parse(sourceText(sourceCommit, 'apps/desktop/package.json')).version,
    JSON.parse(sourceText(sourceCommit, 'apps/desktop/src-tauri/tauri.conf.json')).version,
    sourceText(sourceCommit, 'apps/desktop/src-tauri/Cargo.toml').match(/^version = "([^"]+)"$/m)?.[1],
    cargoLock?.match(/^version = "([^"]+)"$/m)?.[1],
    sourceText(sourceCommit, 'apps/native/internal/host/profile.go').match(/^const Version = "([^"]+)"$/m)?.[1],
  ]);
  const artifacts = await api.list(`actions/runs/${runId}/artifacts`, 'artifacts');
  assertWorkflowArtifacts(artifacts, run);
  const latest = await api.json('releases/latest');
  if (latest) assert.ok(latest.draft === false && latest.prerelease === false, 'Existing latest must be stable');
  const replaceReleaseId = environment.NATIVE_REPLACE_RELEASE_ID ? positiveId(environment.NATIVE_REPLACE_RELEASE_ID) : null;
  if (environment.NATIVE_EXPECTED_SOURCE_COMMIT) assert.equal(sourceCommit, environment.NATIVE_EXPECTED_SOURCE_COMMIT, 'Candidate source changed after authorization');
  return { repository: environment.GITHUB_REPOSITORY, sourceCommit, version, tag, candidateRunId: runId, candidateRunAttempt: run.run_attempt, workflowSourceCommit: environment.GITHUB_SHA, publicationRunId: positiveId(environment.GITHUB_RUN_ID), artifacts, replaceReleaseId, stableLatest: latest ? { id: latest.id, tag: latest.tag_name } : null };
}

async function stage(api, context) {
  assert.equal(await resolveTag(api, context.tag) ?? context.sourceCommit, context.sourceCommit, 'Existing tag points elsewhere');
  const payload = new Map();
  for (const artifact of context.artifacts) {
    const target = releaseTargets.find(target => artifact.name === `liberty-native-candidate-${target}-${context.sourceCommit}`);
    const archiveName = target ? `liberty-native-candidate-${context.version}-${target}.tar.gz` : null;
    const expected = target ? [archiveName, 'candidate-manifest.json', 'native-build-manifest.json', 'SHA256SUMS'] : ['liberty.cdx.json', 'liberty-native.cdx.json', 'native-go-NOTICES.txt'];
    const files = await extractWorkflowZip(await api.artifact(artifact), expected);
    if (target) {
      assert.equal(files.get('SHA256SUMS').toString(), [archiveName, 'candidate-manifest.json', 'native-build-manifest.json'].map(name => `${fileDigest(files.get(name))}  ${name}\n`).join(''), 'Platform checksums differ');
      payload.set(archiveName, files.get(archiveName));
      payload.set(`candidate-manifest-${target}.json`, files.get('candidate-manifest.json'));
      payload.set(`native-build-manifest-${target}.json`, files.get('native-build-manifest.json'));
    } else {
      payload.set('liberty-native.cdx.json', files.get('liberty-native.cdx.json'));
      payload.set('native-go-NOTICES.txt', files.get('native-go-NOTICES.txt'));
    }
    console.log(`Authenticated workflow artifact ${artifact.id}: ${artifact.name}`);
  }
  const verificationContext = { ...context, license: Buffer.from(sourceText(context.sourceCommit, 'LICENSE')) };
  const files = assembleRelease(payload, verificationContext);
  const existing = await findRelease(api, context.tag);
  let previousRelease = null;
  let previousFiles = null;
  if (context.replaceReleaseId && existing?.author?.type === 'User') {
    previousRelease = existing;
    assert.equal(existing.id, context.replaceReleaseId, 'Replacement authorization belongs to another Release');
    previousFiles = new Map();
    for (const asset of existing.assets) {
      assert.ok(releaseAssetNames(context.version).includes(asset.name), 'Unexpected original release asset');
      const bytes = await api.asset(asset, false);
      assertAsset(asset, describe(asset.name, bytes));
      previousFiles.set(asset.name, bytes);
    }
    verifyPreviousRelease(previousRelease, previousFiles, files, verificationContext);
  } else if (context.replaceReleaseId) {
    assert.ok(existing?.author?.login === 'github-actions[bot]' && releaseMarkers(context, files).every(marker => existing.body?.includes(marker)), 'Original Release is missing; rerun the failed publish job with its preserved bundle');
  } else if (existing?.author?.type === 'User') {
    throw new Error('Manual prerelease exists; its exact replacement ID must be explicitly supplied');
  }
  await mkdir(bundleDirectory, { recursive: true });
  assert.equal((await readdir(bundleDirectory)).length, 0, 'Release staging directory must be empty');
  await mkdir(path.join(bundleDirectory, 'assets'));
  for (const [name, bytes] of files) await writeFile(path.join(bundleDirectory, 'assets', name), bytes, { flag: 'wx', mode: 0o600 });
  if (previousRelease) {
    await mkdir(path.join(bundleDirectory, 'previous'));
    await writeFile(path.join(bundleDirectory, 'previous/release.json'), jsonBytes(previousRelease), { flag: 'wx', mode: 0o600 });
    // Eleven unchanged payloads are already in assets/. Only the two original
    // metadata files need additional backup bytes.
    for (const name of ['release-manifest.json', 'SHA256SUMS']) await writeFile(path.join(bundleDirectory, 'previous', name), previousFiles.get(name), { flag: 'wx', mode: 0o600 });
  }
  await writeFile(path.join(bundleDirectory, 'plan.json'), jsonBytes({ ...context, assets: verifyReleaseBundle(files, verificationContext), hasPreviousRelease: Boolean(previousRelease) }), { flag: 'wx', mode: 0o600 });
  await writeFile(path.join(bundleDirectory, 'notes.md'), releaseNotes(sourceText(context.sourceCommit, 'CHANGELOG.md'), context, files), { flag: 'wx', mode: 0o600 });
  if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `bundle_attempt=${positiveId(process.env.GITHUB_RUN_ATTEMPT)}\n`);
  console.log(`Staged 13 verified prerelease assets for ${context.sourceCommit}; no Release or tag has been changed.`);
}

async function publish(api, currentContext) {
  const reportPath = path.join(root, 'artifacts/native-publication.json');
  await mkdir(path.dirname(reportPath), { recursive: true });
  const record = value => writeFile(reportPath, jsonBytes(value), { mode: 0o600 });
  await record({ status: 'validating', tag: currentContext.tag, sourceCommit: currentContext.sourceCommit });
  const plan = JSON.parse(await readFile(path.join(bundleDirectory, 'plan.json'), 'utf8'));
  for (const key of ['repository', 'sourceCommit', 'version', 'tag', 'candidateRunId', 'candidateRunAttempt', 'workflowSourceCommit', 'publicationRunId', 'replaceReleaseId']) assert.equal(plan[key], currentContext[key], `Preserved publication plan differs: ${key}`);
  assert.deepEqual(plan.artifacts, currentContext.artifacts, 'Candidate artifact identities changed');
  const files = new Map(await Promise.all(releaseAssetNames(plan.version).map(async name => [name, await readFile(path.join(bundleDirectory, 'assets', name))])));
  assert.deepEqual((await readdir(path.join(bundleDirectory, 'assets'))).sort(), releaseAssetNames(plan.version));
  const context = { ...plan, license: Buffer.from(sourceText(plan.sourceCommit, 'LICENSE')) };
  const publication = await api.json(`actions/runs/${context.publicationRunId}`);
  assertFreshSourceValidation(await latestAttemptJobs(api, context.publicationRunId, publication.run_attempt));
  assert.deepEqual(verifyReleaseBundle(files, context), plan.assets);
  let previousRelease = null;
  let previousFiles = null;
  if (plan.hasPreviousRelease) {
    previousRelease = JSON.parse(await readFile(path.join(bundleDirectory, 'previous/release.json'), 'utf8'));
    previousFiles = new Map(files);
    for (const name of ['release-manifest.json', 'SHA256SUMS']) previousFiles.set(name, await readFile(path.join(bundleDirectory, 'previous', name)));
  }
  const result = await publishRelease({ api, context, files, previousRelease, previousFiles, notes: await readFile(path.join(bundleDirectory, 'notes.md'), 'utf8'), record });
  if (process.env.GITHUB_STEP_SUMMARY) await appendFile(process.env.GITHUB_STEP_SUMMARY, `Published [${result.tag}](https://github.com/${result.repository}/releases/tag/${result.tag}) as **${result.publisher}**. Release ID **${result.releaseId}**, source \`${result.sourceCommit}\`, 13 public downloads verified. Stable latest retained.\n`);
  console.log(JSON.stringify(result, null, 2));
}

async function main() {
  assert.equal(process.argv.length, 3, 'Expected prepare, stage or publish');
  const command = process.argv[2];
  assert.ok(['prepare', 'stage', 'publish'].includes(command));
  assertMainPublication(process.env);
  const api = new GitHubReleaseClient(process.env.GITHUB_REPOSITORY, process.env.GH_TOKEN);
  const context = await prepare(api, process.env);
  if (command === 'prepare') {
    assert.ok(process.env.GITHUB_OUTPUT);
    await appendFile(process.env.GITHUB_OUTPUT, `source_commit=${context.sourceCommit}\n`);
    console.log(`Authorized ${context.tag} at ${context.sourceCommit}, candidate run ${context.candidateRunId}, in main history.`);
  } else if (command === 'stage') await stage(api, context);
  else await publish(api, context);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(async error => {
    if (process.argv[2] === 'publish') {
      const reportPath = path.join(root, 'artifacts/native-publication.json');
      let prior = {};
      try { prior = JSON.parse(await readFile(reportPath, 'utf8')); } catch { /* No write has happened yet. */ }
      await mkdir(path.dirname(reportPath), { recursive: true });
      await writeFile(reportPath, jsonBytes({ ...prior, status: 'failed', error: error.message }), { mode: 0o600 });
    }
    console.error(error.message);
    process.exitCode = 1;
  });
}
