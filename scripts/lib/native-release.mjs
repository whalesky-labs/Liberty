import assert from 'node:assert/strict';
import { assertNativeCandidatePayload, readNativeArchive } from '../check-native-release-policy.mjs';
import { assertNativeVersion, fileDigest, nativeTargets } from './native-package.mjs';
import { fullSuites } from '../ci/plan.mjs';

export const releaseTargets = Object.freeze(Object.keys(nativeTargets));
const sameNames = (actual, expected) => assert.deepEqual([...actual].sort(), [...expected].sort(), 'Native release file set differs');
const sha = value => assert.match(value ?? '', /^[a-f0-9]{40}$/, 'Exact source SHA required');
export const jsonBytes = value => Buffer.from(JSON.stringify(value, null, 2) + '\n');
export const describe = (name, bytes) => ({ name, bytes: bytes.length, sha256: fileDigest(bytes) });
export const checksums = files => [...files].sort(([a], [b]) => a.localeCompare(b)).map(([name, bytes]) => `${fileDigest(bytes)}  ${name}\n`).join('');

export function positiveId(value) {
  assert.match(String(value ?? ''), /^[1-9][0-9]*$/, 'A positive GitHub ID is required');
  const id = Number(value);
  assert.ok(Number.isSafeInteger(id), 'GitHub ID exceeds safe integer range');
  return id;
}

export function assertMainPublication(environment) {
  assert.equal(environment.GITHUB_ACTIONS, 'true', 'Publication must run inside GitHub Actions');
  assert.equal(environment.GITHUB_EVENT_NAME, 'workflow_dispatch', 'Publication requires explicit dispatch');
  assert.equal(environment.GITHUB_REF, 'refs/heads/main', 'Publication must be dispatched from main');
  assert.match(environment.GITHUB_REPOSITORY ?? '', /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9_][A-Za-z0-9_.-]*$/, 'Invalid repository');
  sha(environment.GITHUB_SHA);
  positiveId(environment.GITHUB_RUN_ID);
}

export function assertPrereleaseVersion(version, otherVersions = []) {
  assertNativeVersion(version, otherVersions);
  assert.match(version, /^2\.(0|[1-9]\d*)\.(0|[1-9]\d*)-[0-9A-Za-z.-]+$/, 'Native publication currently accepts only 2.x prereleases without build metadata');
  return `v${version}`;
}

export function assertCandidateRun(run, jobs, repository, runId) {
  assert.equal(run.id, positiveId(runId));
  assert.equal(run.repository?.full_name, repository, 'Candidate repository differs');
  assert.equal(run.head_repository?.full_name, repository, 'Fork candidates cannot be published');
  assert.equal(run.path, '.github/workflows/build-native.yml', 'Candidate run must use the reviewed Native builder');
  assert.equal(run.event, 'workflow_dispatch');
  assert.equal(run.status, 'completed');
  assert.equal(run.conclusion, 'success', 'Candidate run did not pass');
  positiveId(run.run_attempt);
  sha(run.head_sha);
  const names = ['Select candidate target', 'Required complete validation / full-gate', ...fullSuites.map(suite => `Required complete validation / Full (${suite}) / ${suite}`), ...releaseTargets.map(target => `Candidate (${target})`)];
  sameNames(jobs.map(job => job.name), names);
  assert.ok(jobs.every(job => job.status === 'completed' && job.conclusion === 'success'), 'Every candidate, platform and complete gate must succeed');
  return run.head_sha;
}

export function assertWorkflowArtifacts(artifacts, run) {
  const names = [...releaseTargets.map(target => `liberty-native-candidate-${target}-${run.head_sha}`), `liberty-inventories-${run.head_sha}`];
  sameNames(artifacts.map(artifact => artifact.name), names);
  for (const artifact of artifacts) {
    positiveId(artifact.id);
    assert.equal(artifact.expired, false, 'Candidate artifacts have expired');
    assert.match(artifact.digest ?? '', /^sha256:[a-f0-9]{64}$/, 'GitHub artifact digest is required');
    assert.ok(Number.isSafeInteger(artifact.size_in_bytes) && artifact.size_in_bytes > 0 && artifact.size_in_bytes <= 256 * 1024 * 1024, 'Invalid artifact size');
    assert.equal(artifact.workflow_run?.id, run.id);
    assert.equal(artifact.workflow_run?.head_sha, run.head_sha);
    assert.equal(artifact.workflow_run?.repository_id, run.repository.id);
    assert.equal(artifact.workflow_run?.head_repository_id, run.repository.id);
  }
}

export function assertFreshSourceValidation(jobs, now = new Date()) {
  const names = ['Required source validation / full-gate', ...fullSuites.map(suite => `Required source validation / Full (${suite}) / ${suite}`)];
  for (const name of names) {
    const matches = jobs.filter(job => job.name === name);
    assert.equal(matches.length, 1, 'Exact source revalidation job is missing or duplicated');
    const job = matches[0];
    assert.ok(job.status === 'completed' && job.conclusion === 'success', 'Source revalidation did not pass');
    const age = now.getTime() - new Date(job.completed_at).getTime();
    assert.ok(Number.isFinite(age) && age >= -5 * 60_000 && age <= 24 * 60 * 60_000, 'Source revalidation is older than 24 hours; refresh the full source gate before publication');
  }
}

export async function latestAttemptJobs(api, runId, attempt) {
  positiveId(runId);
  positiveId(attempt);
  assert.ok(attempt <= 20, 'Workflow retry history exceeds the verification bound');
  const latest = new Map();
  // Failed-job reruns need not repeat successful source gates. Read each
  // attempt explicitly instead of mistaking an incomplete latest attempt for
  // the complete workflow, and retain the newest outcome for each job name.
  for (let current = attempt; current >= 1; current--) {
    const jobs = await api.list(`actions/runs/${runId}/attempts/${current}/jobs`, 'jobs');
    assert.equal(new Set(jobs.map(job => job.name)).size, jobs.length, 'Duplicate job names in one workflow attempt');
    for (const job of jobs) if (!latest.has(job.name)) latest.set(job.name, job);
  }
  return [...latest.values()];
}

export function releaseAssetNames(version) {
  return [...releaseTargets.flatMap(target => [`liberty-native-candidate-${version}-${target}.tar.gz`, `candidate-manifest-${target}.json`, `native-build-manifest-${target}.json`]), 'liberty-native.cdx.json', 'native-go-NOTICES.txt', 'release-manifest.json', 'SHA256SUMS'].sort();
}

export function verifyReleasePayload(files, { version, sourceCommit, license }) {
  assertPrereleaseVersion(version);
  sha(sourceCommit);
  sameNames(files.keys(), releaseAssetNames(version).filter(name => !['release-manifest.json', 'SHA256SUMS'].includes(name)));
  const notices = files.get('native-go-NOTICES.txt');
  assert.ok(notices.length && !notices.includes(0), 'Invalid Native notices');
  for (const target of releaseTargets) {
    const name = `liberty-native-candidate-${version}-${target}`;
    const archiveName = `${name}.tar.gz`;
    const archive = files.get(archiveName);
    const manifestBytes = files.get(`candidate-manifest-${target}.json`);
    const archived = readNativeArchive(archive);
    assert.ok([...archived.keys()].every(file => file.startsWith(`${name}/`)), 'Archive root differs');
    const payload = new Map([...archived].map(([file, bytes]) => [file.slice(name.length + 1), bytes]));
    assert.ok(payload.get('candidate-manifest.json')?.equals(manifestBytes), 'Embedded candidate manifest differs');
    const { resources } = assertNativeCandidatePayload({ files: payload, version, target });
    assert.ok(payload.get(`${resources}THIRD_PARTY_NOTICES.txt`)?.equals(notices), 'Packaged Native notices differ');
    assert.equal(payload.get(`${resources}LICENSE`)?.toString().replaceAll('\r\n', '\n'), license.toString().replaceAll('\r\n', '\n'), 'Packaged project license differs from source');
    const build = JSON.parse(files.get(`native-build-manifest-${target}.json`));
    assert.deepEqual(build, {
      format: 1, sourceCommit, version, target, channel: 'candidate', productionReady: false, signed: false, notarized: false, productionSecurityClearance: false,
      files: [describe(archiveName, archive), describe('candidate-manifest.json', manifestBytes)],
    }, 'Native build provenance differs');
  }
  const sbom = JSON.parse(files.get('liberty-native.cdx.json'));
  assert.equal(sbom.bomFormat, 'CycloneDX');
  assert.equal(sbom.specVersion, '1.5');
  assert.equal(sbom.metadata?.component?.name, 'Liberty Native');
  assert.equal(sbom.metadata?.component?.version, version);
  assert.ok(Array.isArray(sbom.components) && sbom.components.length > 0, 'Native SBOM is empty');
  assert.equal(new Set(sbom.components.map(component => component['bom-ref'])).size, sbom.components.length, 'Duplicate SBOM components');
  assert.ok(sbom.components.every(component => typeof component.purl === 'string' && !component.purl.startsWith('pkg:cargo/')), 'Native SBOM scope differs');
}

export function assembleRelease(payload, context) {
  verifyReleasePayload(payload, context);
  const manifest = {
    format: 2, repository: context.repository, version: context.version, tag: assertPrereleaseVersion(context.version), sourceCommit: context.sourceCommit,
    channel: 'candidate', prerelease: true, makeLatest: false, productionReady: false, signed: false, notarized: false, productionSecurityClearance: false,
    validation: { workflowRunId: context.candidateRunId, headSha: context.sourceCommit, conclusion: 'success', fullGate: 'success', securityPolicyScan: 'success', targetsExecutedByCI: releaseTargets },
    originalWorkflowArtifacts: context.artifacts.map(({ id, name, size_in_bytes, digest }) => ({ id, name, bytes: size_in_bytes, digest })).sort((a, b) => a.name.localeCompare(b.name)),
    assets: [...payload].sort(([a], [b]) => a.localeCompare(b)).map(([name, bytes]) => describe(name, bytes)),
    metadataAssets: ['release-manifest.json', 'SHA256SUMS'],
  };
  // A portable manifest is independent of a draft/public Release ID and of the
  // current publication attempt, so an interrupted transaction can be resumed.
  const files = new Map(payload);
  files.set('release-manifest.json', jsonBytes(manifest));
  files.set('SHA256SUMS', Buffer.from(checksums(files)));
  return files;
}

export function verifyReleaseBundle(files, context) {
  sameNames(files.keys(), releaseAssetNames(context.version));
  const payload = new Map([...files].filter(([name]) => !['release-manifest.json', 'SHA256SUMS'].includes(name)));
  const expected = assembleRelease(payload, context);
  for (const [name, bytes] of files) assert.ok(bytes.equals(expected.get(name)), `Release bundle bytes differ: ${name}`);
  return [...files].sort(([a], [b]) => a.localeCompare(b)).map(([name, bytes]) => describe(name, bytes));
}

export function assertAsset(asset, expected) {
  positiveId(asset.id);
  assert.equal(asset.name, expected.name);
  assert.equal(asset.state, 'uploaded', 'Incomplete remote asset cannot be overwritten');
  assert.equal(asset.size, expected.bytes, 'Remote asset size differs');
  assert.equal(asset.digest, `sha256:${expected.sha256}`, 'Remote asset checksum differs');
}

export function assertManualReplacement(release, context, expectedId) {
  assert.equal(release.id, positiveId(expectedId), 'Only the explicitly requested Release ID may be replaced');
  assert.equal(release.tag_name, context.tag);
  assert.equal(release.target_commitish, context.sourceCommit);
  assert.equal(release.name, `Liberty ${context.tag}`);
  assert.equal(release.draft, false);
  assert.equal(release.prerelease, true, 'Stable releases cannot be replaced');
  assert.ok(release.published_at);
  assert.notEqual(release.immutable, true, 'Immutable releases cannot be replaced');
  assert.equal(release.author?.type, 'User', 'Only a manually created prerelease can be migrated');
  assert.ok(release.body?.includes(`<!-- liberty-release-commit:${context.sourceCommit} -->`));
  sameNames(release.assets.map(asset => asset.name), releaseAssetNames(context.version));
}

export function verifyPreviousRelease(release, previousFiles, currentFiles, context) {
  assertManualReplacement(release, context, context.replaceReleaseId);
  sameNames(previousFiles.keys(), releaseAssetNames(context.version));
  const before = JSON.parse(previousFiles.get('release-manifest.json'));
  assert.equal(before.format, 1);
  assert.equal(before.repository, context.repository);
  assert.equal(before.releaseId, release.id);
  assert.equal(before.sourceCommit, context.sourceCommit);
  assert.equal(before.version, context.version);
  assert.equal(before.tag, context.tag);
  for (const [name, value] of Object.entries({ prerelease: true, makeLatest: false, productionReady: false, signed: false, notarized: false, productionSecurityClearance: false })) assert.equal(before[name], value);
  assert.equal(before.validation?.workflowRunId, context.candidateRunId);
  for (const [name, value] of Object.entries({ headSha: context.sourceCommit, conclusion: 'success', fullGate: 'success', securityPolicyScan: 'success' })) assert.equal(before.validation[name], value);
  assert.deepEqual([...before.originalWorkflowArtifacts].sort((a, b) => a.name.localeCompare(b.name)), context.artifacts.map(({ id, name, size_in_bytes, digest }) => ({ id, name, bytes: size_in_bytes, digest })).sort((a, b) => a.name.localeCompare(b.name)));
  assert.deepEqual(before.metadataAssets, ['release-manifest.json', 'SHA256SUMS']);
  const expectedPayload = [...currentFiles].filter(([name]) => !['release-manifest.json', 'SHA256SUMS'].includes(name));
  sameNames(before.assets.map(asset => asset.name), expectedPayload.map(([name]) => name));
  for (const [name, bytes] of expectedPayload) {
    assert.ok(bytes.equals(previousFiles.get(name)), `Manual prerelease payload differs from verified CI: ${name}`);
    const record = before.assets.find(asset => asset.name === name);
    assert.equal(record.bytes, bytes.length);
    assert.equal(record.sha256, fileDigest(bytes));
  }
  assert.equal(previousFiles.get('SHA256SUMS').toString(), checksums(new Map([...previousFiles].filter(([name]) => name !== 'SHA256SUMS'))));
  for (const asset of release.assets) assertAsset(asset, describe(asset.name, previousFiles.get(asset.name)));
}

const stableIdentity = release => release ? { id: release.id, tag: release.tag_name } : null;
const manualIdentity = release => ({ id: release.id, tag_name: release.tag_name, target_commitish: release.target_commitish, name: release.name, body: release.body, draft: release.draft, prerelease: release.prerelease, published_at: release.published_at, author: release.author, assets: release.assets.map(({ id, name, size, digest, state }) => ({ id, name, size, digest, state })).sort((a, b) => a.name.localeCompare(b.name)) });
export function releaseMarkers(context, files) {
  return [`<!-- liberty-release-commit:${context.sourceCommit} -->`, `<!-- liberty-native-build-run:${context.candidateRunId} -->`, `<!-- liberty-native-bundle:${fileDigest(files.get('release-manifest.json'))} -->`, ...(context.replaceReleaseId ? [`<!-- liberty-native-replaced-release:${context.replaceReleaseId} -->`] : [])];
}

function assertBotRelease(release, context, files) {
  assert.equal(release.tag_name, context.tag);
  assert.equal(release.target_commitish, context.sourceCommit);
  assert.equal(release.name, `Liberty ${context.tag}`);
  assert.equal(release.prerelease, true);
  assert.equal(release.author?.login, 'github-actions[bot]', 'Publication must use the GitHub Actions bot token');
  assert.equal(release.author?.type, 'Bot');
  assert.ok(releaseMarkers(context, files).every(marker => release.body?.includes(marker)), 'Existing bot draft belongs to different inputs');
  positiveId(release.id);
}

export async function resolveTag(api, tag) {
  let target = (await api.json(`git/ref/tags/${encodeURIComponent(tag)}`))?.object;
  if (!target) return null;
  const seen = new Set();
  while (target.type === 'tag') {
    assert.ok(!seen.has(target.sha) && seen.size < 10, 'Invalid annotated tag chain');
    seen.add(target.sha);
    target = (await api.json(`git/tags/${target.sha}`))?.object;
    assert.ok(target, 'Annotated tag is missing');
  }
  assert.equal(target.type, 'commit');
  sha(target.sha);
  return target.sha;
}

export async function findRelease(api, tag) {
  const matching = (await api.list('releases')).filter(release => release.tag_name === tag);
  const direct = await api.json(`releases/tags/${encodeURIComponent(tag)}`);
  if (direct) {
    const index = matching.findIndex(release => release.id === direct.id);
    if (index < 0) matching.push(direct);
    else matching[index] = direct;
  }
  assert.ok(matching.length <= 1, 'Duplicate releases for one tag');
  return matching[0] ?? null;
}

export async function publishRelease({ api, context, files, previousRelease = null, previousFiles = null, notes, record = async () => {} }) {
  const assets = verifyReleaseBundle(files, context);
  assert.ok(releaseMarkers(context, files).every(marker => notes.includes(marker)), 'Release notes do not bind the verified bundle');
  assert.deepEqual(stableIdentity(await api.json('releases/latest')), context.stableLatest, 'Stable latest changed before publication');
  const tagCommit = await resolveTag(api, context.tag);
  if (tagCommit) assert.equal(tagCommit, context.sourceCommit, 'Refusing to move the immutable tag');
  let release = await findRelease(api, context.tag);
  if (release?.author?.type === 'User') {
    release = await api.json(`releases/${positiveId(release.id)}`);
    assert.ok(release, 'Original Release disappeared before replacement');
    assertManualReplacement(release, context, context.replaceReleaseId);
    assert.ok(previousRelease && previousFiles, 'Existing manual Release requires explicit ID and a verified backup');
    verifyPreviousRelease(previousRelease, previousFiles, files, context);
    assert.deepEqual(manualIdentity(release), manualIdentity(previousRelease), 'Manual Release changed after it was backed up');
    assert.equal(tagCommit, context.sourceCommit, 'Manual Release must retain its existing tag');
    // All bytes and the original identity are already in an immutable workflow
    // artifact before this job receives write permission. No other Release or
    // asset deletion exists in this transaction.
    await record({ status: 'replacing', previousReleaseId: release.id });
    await api.json(`releases/${positiveId(context.replaceReleaseId)}`, { method: 'DELETE' });
    release = null;
  } else if (!release && context.replaceReleaseId) {
    // A failed publish job can resume its preserved original bundle after the
    // old Release was deleted but before the create request completed.
    assert.ok(previousRelease && previousFiles, 'Missing original Release; resume the prior failed publish job with its backup');
    verifyPreviousRelease(previousRelease, previousFiles, files, context);
    assert.equal(tagCommit, context.sourceCommit);
  }
  if (!release) {
    release = await api.json('releases', { method: 'POST', body: { tag_name: context.tag, target_commitish: context.sourceCommit, name: `Liberty ${context.tag}`, body: notes, draft: true, prerelease: true, make_latest: 'false' } });
    assertBotRelease(release, context, files);
    assert.equal(release.draft, true);
    await record({ status: 'draft', releaseId: release.id, previousReleaseId: context.replaceReleaseId ?? null });
  }
  assertBotRelease(release, context, files);
  const releaseId = release.id;
  const inspect = async (complete, draft) => {
    const current = await api.json(`releases/${releaseId}`);
    assert.equal(current?.id, releaseId, 'Release ID binding was lost');
    assertBotRelease(current, context, files);
    if (draft !== undefined) assert.equal(current.draft, draft, 'Release draft state changed unexpectedly');
    if (complete) sameNames(current.assets.map(asset => asset.name), assets.map(asset => asset.name));
    else assert.equal(new Set(current.assets.map(asset => asset.name)).size, current.assets.length, 'Duplicate remote assets');
    for (const asset of current.assets) {
      const expected = assets.find(expected => expected.name === asset.name);
      assert.ok(expected, 'Unexpected remote asset cannot be deleted');
      assertAsset(asset, expected);
    }
    return current;
  };
  if (release.draft === false) {
    release = await inspect(true, false);
    assert.ok(release.published_at);
  } else {
    release = await inspect(false, true);
    for (const expected of assets) {
      if (release.assets.some(asset => asset.name === expected.name)) continue;
      const uploaded = await api.upload(releaseId, expected.name, files.get(expected.name));
      assertAsset(uploaded, expected);
      release.assets.push(uploaded);
    }
    release = await inspect(true, true);
    for (const asset of release.assets) {
      const bytes = await api.asset(asset, true);
      assert.ok(bytes.equals(files.get(asset.name)), `Uploaded bytes differ: ${asset.name}`);
    }
    const beforePublishTag = await resolveTag(api, context.tag);
    if (tagCommit) assert.ok(beforePublishTag, 'Original tag disappeared before publication');
    if (beforePublishTag) assert.equal(beforePublishTag, context.sourceCommit, 'Tag changed before publication');
    assert.deepEqual(stableIdentity(await api.json('releases/latest')), context.stableLatest);
    // Include all identity fields on every PATCH: an omitted tag_name can cause
    // GitHub to assign a temporary untagged identity to a draft.
    await api.json(`releases/${releaseId}`, { method: 'PATCH', body: { tag_name: context.tag, target_commitish: context.sourceCommit, name: `Liberty ${context.tag}`, body: `${notes}\n\nRelease ID: \`${releaseId}\` · Source commit: \`${context.sourceCommit}\` · Assets: **13**.\n`, draft: false, prerelease: true, make_latest: 'false' } });
    release = await inspect(true, false);
    assert.ok(release.published_at);
  }
  assert.equal(await resolveTag(api, context.tag), context.sourceCommit, 'Published tag does not bind the source commit');
  assert.deepEqual(stableIdentity(await api.json('releases/latest')), context.stableLatest, 'Prerelease changed stable latest');
  for (const asset of release.assets) {
    const bytes = await api.asset(asset, false);
    assert.ok(bytes.equals(files.get(asset.name)), `Public download differs: ${asset.name}`);
  }
  const result = { status: 'published', repository: context.repository, tag: context.tag, sourceCommit: context.sourceCommit, workflowSourceCommit: context.workflowSourceCommit, releaseId, previousReleaseId: context.replaceReleaseId ?? null, publisher: release.author.login, draft: false, prerelease: true, stableLatest: context.stableLatest, candidateRunId: context.candidateRunId, assetCount: assets.length, publicDownloadsVerified: true, assets: release.assets.map(({ id, name, size, digest }) => ({ id, name, size, digest })) };
  await record(result);
  return result;
}
