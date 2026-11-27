import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { buildInstallerArtifacts } from './lib/native-installers.mjs';

try {
  const args = process.argv.slice(2);
  assert.ok(args.length === 6 && args[0] === '--target' && args[2] === '--commit' && args[4] === '--input', 'Expected --target <target> --commit <sha> --input <verified-artifacts>');
  const git = (...args) => execFileSync('git', args, { encoding: 'utf8' }).trim();
  assert.equal(git('rev-parse', 'HEAD'), args[3], 'Installer source differs from checkout');
  assert.equal(git('status', '--porcelain=v1', '--untracked-files=all', '--', 'apps/native', 'packages', 'scripts', 'python', 'package.json', 'pnpm-lock.yaml', '.github/workflows/build-native.yml'), '', 'Installer source inputs must be clean');
  const manifest = await buildInstallerArtifacts({ target: args[1], sourceCommit: args[3], input: args[5] });
  console.log(`Verified ${manifest.installer.name}; signature=${manifest.signature}, notarized=false, updates=${manifest.updatesURL}`);
} catch (error) { console.error(error.message); process.exitCode = 1; }
