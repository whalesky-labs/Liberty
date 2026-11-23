import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const files = execFileSync('git', ['ls-files', '-z', '--', '*.md'], { cwd: root, encoding: 'utf8' }).split('\0').filter(Boolean);
const errors = [];
for (const file of files) {
  if (!existsSync(path.join(root, file))) continue; // A deleted tracked document is no longer an input.
  const lines = readFileSync(path.join(root, file), 'utf8').split('\n');
  let fence = null;
  for (const [index, line] of lines.entries()) {
    const marker = line.match(/^\s*(`{3,}|~{3,})/);
    if (marker) {
      if (fence === null) fence = marker[1];
      else if (marker[1][0] === fence[0] && marker[1].length >= fence.length) fence = null;
      continue;
    }
    if (fence) continue;
    for (const match of line.matchAll(/\]\(([^\s)]+)(?:\s+"[^"]*")?\)/g)) {
      const href = match[1];
      if (/^(?:[a-z][a-z0-9+.-]*:|#)/i.test(href)) continue;
      let target;
      try { target = decodeURIComponent(href.split('#')[0]); } catch { errors.push(`${file}:${index + 1}: malformed link`); continue; }
      if (target && !existsSync(path.resolve(root, path.dirname(file), target))) errors.push(`${file}:${index + 1}: missing relative link ${target}`);
    }
  }
  if (fence !== null) errors.push(`${file}: unclosed code fence`);
}
if (errors.length) { console.error(errors.join('\n')); process.exitCode = 1; }
else console.log(`Documentation links and fences passed (${files.length} tracked documents).`);
