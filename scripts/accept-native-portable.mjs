import { spawn } from 'node:child_process';
import { chmod, lstat, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertNativeCandidatePayload, readNativeArchive } from './check-native-release-policy.mjs';
import { assertRunnableTarget, fileDigest } from './lib/native-package.mjs';
import { runningNativeCandidatePaths } from './run-native.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));

// Interactive acceptance only: every install/profile path is newly allocated.
// Existing applications, candidate profiles and production data are never inputs.
async function input(file, { allowLegacyNotices = false } = {}) {
  if (!path.isAbsolute(file) || !(await lstat(file)).isFile()) throw new Error('A regular absolute candidate archive is required');
  const compressed = await readFile(file);
  const archive = readNativeArchive(compressed);
  const roots = new Set([...archive.keys()].map(name => name.split('/')[0]));
  if (roots.size !== 1) throw new Error('Expected one candidate archive root');
  const prefix = [...roots][0] + '/';
  const files = new Map([...archive].map(([name, bytes]) => [name.slice(prefix.length), bytes]));
  const manifest = JSON.parse(files.get('candidate-manifest.json')?.toString() ?? 'null');
  if (!manifest || prefix !== `liberty-native-candidate-${manifest.version}-${manifest.target}/`) throw new Error('Candidate archive identity differs');
  assertNativeCandidatePayload({ files, version: manifest.version, target: manifest.target, allowLegacyNotices });
  assertRunnableTarget(manifest.target);
  return { files, manifest, sha256: fileDigest(compressed) };
}

async function stage(directory, candidate) {
  await mkdir(directory);
  for (const [relative, bytes] of candidate.files) {
    const file = path.join(directory, relative);
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, bytes, { flag: 'wx', mode: 0o600 });
    if (relative.endsWith('/liberty-native') || relative === 'Liberty Native Candidate.exe') await chmod(file, 0o700);
  }
}

async function accept() {
  const args = process.argv.slice(2);
  if (args.length !== 4 || args[0] !== '--previous-archive' || args[2] !== '--candidate-archive') throw new Error('Expected --previous-archive <absolute archive> --candidate-archive <absolute archive>');
  const previous = await input(args[1], { allowLegacyNotices: true });
  const candidate = await input(args[3]);
  if (previous.manifest.target !== candidate.manifest.target || previous.manifest.version === candidate.manifest.version) throw new Error('Acceptance requires two different versions for the same host target');
  if ((await runningNativeCandidatePaths(process.platform)).length !== 0) throw new Error('Quit the running Native client normally before portable acceptance; no existing process was stopped');
  const work = await realpath(await mkdtemp(path.join(os.tmpdir(), 'liberty-portable-acceptance-')));
  let passed = false;
  try {
    await stage(path.join(work, 'previous'), previous);
    await stage(path.join(work, 'candidate'), candidate);
    await writeFile(path.join(work, '.native-portable-acceptance'), 'isolated-portable-acceptance-v1\n', { flag: 'wx', mode: 0o600 });
    console.log(`Isolated portable acceptance: ${work}`);
    console.log(`Previous ${previous.manifest.version} archive SHA-256: ${previous.sha256}`);
    console.log(`Candidate ${candidate.manifest.version} archive SHA-256: ${candidate.sha256}`);
    await new Promise((resolve, reject) => {
      const child = spawn('go', ['test', '-tags', 'portable_acceptance', './internal/host', '-run', '^TestPortableUpgradeRollbackAcceptance$', '-count=1', '-timeout=45m', '-v'], {
        cwd: path.join(root, 'apps/native'),
        env: { ...process.env, GOTOOLCHAIN: 'go1.27.4', GOFLAGS: '-mod=readonly', LIBERTY_NATIVE_PORTABLE_ACCEPTANCE_DIR: work },
        stdio: 'inherit',
      });
      child.once('error', reject);
      child.once('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`Portable acceptance failed (${signal ?? code}); isolated artifacts retained at ${work}`)));
    });
    passed = true;
    console.log('Portable application replacement, rollback, profile restore and retained safety snapshot passed on this host. This is not MSI, Gatekeeper, other-device or production acceptance.');
  } finally {
    // On failure retain artifacts for diagnosis; never remove a possibly live GUI.
    if (passed) await rm(work, { recursive: true, force: true });
  }
}

accept().catch(error => { console.error(error.message); process.exitCode = 1; });
