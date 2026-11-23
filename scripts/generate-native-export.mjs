import { readFile, writeFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const source = await readFile(path.join(root, 'packages/desktop-resources/templates/meeting-minutes.docx'));
const destination = path.join(root, 'apps/native/internal/infrastructure/minutesdocx/meeting-minutes.docx');
if (process.argv.includes('--check')) {
  if (!(await readFile(destination)).equals(source)) throw new Error('Native Word template drifted; run pnpm native:export:generate');
} else {
  await mkdir(path.dirname(destination), { recursive: true });
  await writeFile(destination, source);
}
console.log('Native Word template matches the original template.');
