import { readFileSync } from 'node:fs';
import { windowsIconResourceSection } from '../lib/native-windows-resources.mjs';

export const windowsIconFixture = readFileSync(new URL('../../packages/desktop-resources/presentation/icon.ico', import.meta.url));

// Synthetic PE container used only by byte-policy tests; never executed.
export function windowsExecutableFixture(icon = windowsIconFixture) {
  const { bytes, relocations } = windowsIconResourceSection(icon);
  const raw = 512, rva = 0x2000, pe = 128, optional = pe + 24, section = optional + 240;
  const program = Buffer.alloc(raw + bytes.length);
  program.write('MZ');
  program.writeUInt32LE(pe, 0x3c);
  program.writeUInt32LE(0x4550, pe);
  program.writeUInt16LE(0x8664, pe + 4);
  program.writeUInt16LE(1, pe + 6);
  program.writeUInt16LE(240, pe + 20);
  program.writeUInt16LE(0x20b, optional);
  program.writeUInt32LE(16, optional + 108);
  program.writeUInt32LE(rva, optional + 128);
  program.writeUInt32LE(bytes.length, optional + 132);
  program.write('.rsrc', section);
  program.writeUInt32LE(bytes.length, section + 8);
  program.writeUInt32LE(rva, section + 12);
  program.writeUInt32LE(bytes.length, section + 16);
  program.writeUInt32LE(raw, section + 20);
  bytes.copy(program, raw);
  for (const offset of relocations) program.writeUInt32LE(bytes.readUInt32LE(offset) + rva, raw + offset);
  return program;
}
