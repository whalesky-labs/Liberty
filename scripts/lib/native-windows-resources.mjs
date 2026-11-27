// Deterministic icon-only COFF resources for Go's Windows linker. No SDK,
// downloaded resource compiler, or extra module is needed on the build host.
import assert from 'node:assert/strict';

export function windowsIconFrames(icon) {
  assert.ok(Buffer.isBuffer(icon) && icon.length >= 6, 'Invalid Windows icon');
  const count = icon.readUInt16LE(4);
  assert.ok(icon.readUInt32LE(0) === 0x10000 && count > 0 && count <= 256 && icon.length >= 6 + 16 * count, 'Invalid Windows icon directory');
  const frames = [];
  for (let i = 0; i < count; i++) {
    const offset = 6 + 16 * i;
    const size = icon.readUInt32LE(offset + 8), start = icon.readUInt32LE(offset + 12);
    assert.ok(size > 0 && start >= 6 + 16 * count && start + size <= icon.length, 'Invalid Windows icon frame');
    frames.push({ entry: icon.subarray(offset, offset + 12), image: icon.subarray(start, start + size) });
  }
  return frames;
}

export function windowsApplicationPNG(icon) {
  const frames = windowsIconFrames(icon).sort((a, b) => (b.entry[0] || 256) - (a.entry[0] || 256));
  const image = frames[0].image;
  assert.ok(image.subarray(0, 8).equals(Buffer.from('89504e470d0a1a0a', 'hex')), 'Application icon must contain a PNG frame');
  return image;
}

// Resource type -> ID -> language -> data, using icon group ID 1, which the
// pinned MyGo Windows backend loads when it registers its window classes.
export function windowsIconResourceSection(icon) {
  const frames = windowsIconFrames(icon);
  const group = Buffer.alloc(6 + 14 * frames.length);
  group.writeUInt16LE(1, 2);
  group.writeUInt16LE(frames.length, 4);
  frames.forEach((frame, index) => {
    frame.entry.copy(group, 6 + 14 * index);
    group.writeUInt16LE(index + 1, 18 + 14 * index);
  });
  const types = [[3, frames.map((frame, i) => [i + 1, frame.image])], [14, [[1, group]]]];
  const dirSize = count => 16 + 8 * count;
  const directoryBytes = dirSize(types.length) + types.reduce((sum, [, entries]) => sum + dirSize(entries.length) + entries.length * dirSize(1), 0);
  let next = dirSize(types.length), entry = directoryBytes, dataAt = directoryBytes + 16 * (frames.length + 1);
  const raw = Buffer.alloc(dataAt + types.flatMap(([, entries]) => entries).reduce((sum, [, data]) => sum + data.length + 7, 0));
  const relocations = [];
  raw.writeUInt16LE(types.length, 14);
  for (const [i, [type, entries]] of types.entries()) {
    raw.writeUInt32LE(type, 16 + 8 * i);
    raw.writeUInt32LE((next | 0x80000000) >>> 0, 20 + 8 * i);
    const idDirectory = next;
    raw.writeUInt16LE(entries.length, idDirectory + 14);
    next += dirSize(entries.length);
    for (const [j, [id, data]] of entries.entries()) {
      raw.writeUInt32LE(id, idDirectory + 16 + 8 * j);
      raw.writeUInt32LE((next | 0x80000000) >>> 0, idDirectory + 20 + 8 * j);
      raw.writeUInt16LE(1, next + 14);
      raw.writeUInt32LE(0x409, next + 16); // language independent icon pixels
      raw.writeUInt32LE(entry, next + 20);
      next += dirSize(1);
      dataAt = Math.ceil(dataAt / 8) * 8;
      raw.writeUInt32LE(dataAt, entry);
      raw.writeUInt32LE(data.length, entry + 4);
      relocations.push(entry);
      data.copy(raw, dataAt);
      dataAt += data.length;
      entry += 16;
    }
  }
  return { bytes: raw.subarray(0, Math.ceil(dataAt / 4) * 4), relocations };
}

export function windowsIconObject(icon) {
  const { bytes, relocations } = windowsIconResourceSection(icon);
  const dataAt = 60, relocationAt = dataAt + bytes.length, symbolsAt = relocationAt + 10 * relocations.length;
  const object = Buffer.alloc(symbolsAt + 18 + 4);
  object.writeUInt16LE(0x8664, 0); // IMAGE_FILE_MACHINE_AMD64
  object.writeUInt16LE(1, 2);
  object.writeUInt32LE(symbolsAt, 8);
  object.writeUInt32LE(1, 12);
  object.write('.rsrc', 20);
  object.writeUInt32LE(bytes.length, 36);
  object.writeUInt32LE(dataAt, 40);
  object.writeUInt32LE(relocationAt, 44);
  object.writeUInt16LE(relocations.length, 52);
  object.writeUInt32LE(0x40000040, 56); // initialized, readable data
  bytes.copy(object, dataAt);
  relocations.forEach((offset, i) => {
    object.writeUInt32LE(offset, relocationAt + 10 * i);
    object.writeUInt16LE(3, relocationAt + 10 * i + 8); // IMAGE_REL_AMD64_ADDR32NB, symbol 0
  });
  object.write('.rsrc', symbolsAt);
  object.writeUInt16LE(1, symbolsAt + 12);
  object[symbolsAt + 16] = 3; // static section symbol
  object.writeUInt32LE(4, symbolsAt + 18); // empty string table
  return object;
}

// Verify linked resources rather than merely finding icon bytes somewhere in
// the executable. Group ID, language entries and every image must match.
export function assertWindowsApplicationIcon(executable, icon) {
  const invalid = () => { throw new Error('Windows executable application icon differs or is missing'); };
  const range = (at, size) => { if (!Number.isSafeInteger(at) || at < 0 || at + size > executable.length) invalid(); return at; };
  const u16 = at => executable.readUInt16LE(range(at, 2));
  const u32 = at => executable.readUInt32LE(range(at, 4));
  const pe = u32(0x3c), optional = pe + 24;
  if (executable.toString('ascii', 0, 2) !== 'MZ' || u32(pe) !== 0x4550 || u16(pe + 4) !== 0x8664 || u16(optional) !== 0x20b || u16(pe + 20) < 136 || u32(optional + 108) < 3) invalid();
  const sections = [];
  for (let i = 0; i < u16(pe + 6); i++) {
    const at = optional + u16(pe + 20) + i * 40;
    sections.push({ rva: u32(at + 12), size: u32(at + 16), raw: u32(at + 20) });
  }
  const fileOffset = (rva, size) => {
    const section = sections.find(section => rva >= section.rva && rva + size <= section.rva + section.size);
    if (!section) invalid();
    return range(section.raw + rva - section.rva, size);
  };
  const resourceSize = u32(optional + 132);
  if (resourceSize < 16) invalid();
  const root = fileOffset(u32(optional + 128), resourceSize);
  const resourceRange = (at, size) => { if (at < root || at + size > root + resourceSize) invalid(); };
  const child = (directory, id, isDirectory) => {
    resourceRange(directory, 16);
    const count = u16(directory + 12) + u16(directory + 14);
    if (count > 512) invalid();
    resourceRange(directory + 16, count * 8);
    for (let i = 0; i < count; i++) {
      const at = directory + 16 + i * 8;
      if (id !== undefined && u32(at) !== id) continue;
      const offset = u32(at + 4);
      if (Boolean(offset & 0x80000000) !== isDirectory) invalid();
      return root + (offset & 0x7fffffff);
    }
    invalid();
  };
  const resource = (type, id) => {
    const language = child(child(root, type, true), id, true);
    const data = child(language, undefined, false);
    resourceRange(data, 16);
    const size = u32(data + 4), offset = fileOffset(u32(data), size);
    resourceRange(offset, size);
    return executable.subarray(offset, offset + size);
  };
  const frames = windowsIconFrames(icon), group = resource(14, 1);
  if (group.length !== 6 + 14 * frames.length || group.readUInt32LE(0) !== 0x10000 || group.readUInt16LE(4) !== frames.length) invalid();
  frames.forEach((frame, i) => {
    const at = 6 + 14 * i;
    if (!group.subarray(at, at + 12).equals(frame.entry) || !resource(3, group.readUInt16LE(at + 12)).equals(frame.image)) invalid();
  });
}
