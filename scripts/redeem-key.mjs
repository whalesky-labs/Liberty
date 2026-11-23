#!/usr/bin/env node
import { createHash, createHmac, createPrivateKey, generateKeyPairSync, randomBytes, sign, timingSafeEqual } from "node:crypto";
import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const DEFAULT_PRIVATE_KEY_PATH = ".liberty-secrets/redeem-private-key.pem";
const DEFAULT_PUBLIC_KEY_PATH = ".liberty-secrets/redeem-public-key.base64";
const DEFAULT_OUT_PATH = ".liberty-secrets/redeem-keys.csv";
const DEFAULT_REISSUED_OUT_PATH = ".liberty-secrets/reissued-redeem-keys.csv";
const KEY_PREFIX = "LIB2";
const SHORT_KEY_SECRET_B64 = "B+6+O1eix4RItBiakvkqdGDRWlVK8I1m0ABDHgmJCXE=";
const SHORT_PAYLOAD_BYTES = 20;
const SHORT_MAC_BYTES = 12;
const SHORT_TOKEN_BYTES = SHORT_PAYLOAD_BYTES + SHORT_MAC_BYTES;
const SHORT_TOKEN_CHUNK_SIZE = 13;
const SHORT_ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const ITEM_NAME_ZH = {
  "gem-ticket-tool": "补签票券",
  "gift-box-tool": "惊喜礼盒",
  "sheep-plush-scene": "小羊玩偶",
};
const COMPACT_PAYLOAD_BYTES = 28;
const COMPACT_SIGNATURE_BYTES = 64;
const COMPACT_KEY_LENGTH = 128;
const COMPACT_BASE_DATE = Date.UTC(2026, 0, 1);
const DAY_MS = 24 * 60 * 60 * 1000;
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

const command = process.argv[2] ?? "help";
const args = parseArgs(process.argv.slice(3));

if (command === "keypair") {
  generateKeyPair(args);
} else if (command === "generate") {
  generateRedeemKeys(args);
} else if (command === "reissue") {
  reissueShortKeys(args);
} else {
  printHelp();
}

function generateKeyPair(args) {
  const privateKeyPath = resolve(args.privateKey ?? DEFAULT_PRIVATE_KEY_PATH);
  const publicKeyPath = resolve(args.publicKey ?? DEFAULT_PUBLIC_KEY_PATH);
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const spki = publicKey.export({ type: "spki", format: "der" });
  const publicKeyRaw = spki.subarray(spki.length - 32);

  let privateKeyCreated = false;
  try {
    mkdirSync(dirname(privateKeyPath), { recursive: true });
    mkdirSync(dirname(publicKeyPath), { recursive: true });
    writeFileSync(privateKeyPath, privateKey.export({ type: "pkcs8", format: "pem" }), { mode: 0o600, flag: "wx" });
    privateKeyCreated = true;
    chmodSync(privateKeyPath, 0o600);
    writeFileSync(publicKeyPath, `${publicKeyRaw.toString("base64")}\n`, { flag: "wx" });
  } catch {
    if (privateKeyCreated) rmSync(privateKeyPath, { force: true });
    throw new Error("Unable to create a new issuer keypair without replacing existing files");
  }

  console.log(`Generated an Ed25519 keypair. Public key base64: ${publicKeyRaw.toString("base64")}`);
}

function generateRedeemKeys(args) {
  const campaignId = required(args.campaign, "--campaign");
  const count = positiveInteger(args.count ?? "1", "--count");
  const rewards = parseRewards(args);
  const format = args.format ?? "signed";
  if (!["signed", "compact", "short"].includes(format)) {
    throw new Error("--format must be signed, compact, or short");
  }
  const displayPrefix = format === "short" ? normalizeDisplayPrefix(args.prefix ?? campaignId) : "";
  const privateKey = format === "short" ? null : readSigningKey(args.privateKey);
  const outPath = resolve(args.out ?? DEFAULT_OUT_PATH);
  const rows = [["活动ID", "兑换Key", "奖励明细"]];

  for (let index = 0; index < count; index += 1) {
    const key = format === "short"
      ? encodeShortKey({ displayPrefix, rewards, expiresAt: args.expires ?? null })
      : format === "compact"
        ? encodeCompactKey({ campaignId, rewards, expiresAt: args.expires ?? null, privateKey })
        : encodeSignedKey({ campaignId, rewards, expiresAt: args.expires ?? null, privateKey });
    rows.push([
      campaignId,
      key,
      rewardSummary(rewards),
    ]);
  }

  writePrivateCsv(outPath, rows);
  console.log(`Generated ${count} key(s).`);
}

function reissueShortKeys(args) {
  const inputPath = resolve(required(args.input, "--input"));
  let input;
  try {
    input = readFileSync(inputPath, "utf8");
  } catch {
    throw new Error("Unable to read the legacy redemption input file");
  }
  const keys = parseLegacyKeyInput(input).map((value) => value.replace(/\s/gu, ""));
  if (keys.length === 0) throw new Error("The legacy redemption input file contains no keys");
  if (new Set(keys).size !== keys.length) throw new Error("The legacy redemption input file contains duplicate keys");
  const privateKey = readSigningKey(args.privateKey);
  const outPath = resolve(args.out ?? DEFAULT_REISSUED_OUT_PATH);
  const rows = [["旧兑换Key", "新签名兑换Key"]];
  const itemKeys = loadCompactItemKeys();
  for (const normalized of keys) {
    const { displayPrefix, payload } = verifyShortKey(normalized);
    const redemption = signedPayloadFromShort(displayPrefix, payload, itemKeys);
    rows.push([normalized, encodeSignedPayload(redemption, privateKey)]);
  }
  writePrivateCsv(outPath, rows);
  console.log(`Reissued ${keys.length} key(s).`);
}

function readSigningKey(filePath) {
  let key;
  try {
    key = createPrivateKey(readFileSync(resolve(filePath ?? DEFAULT_PRIVATE_KEY_PATH)));
  } catch {
    throw new Error("Unable to load the Ed25519 signing key");
  }
  if (key.asymmetricKeyType !== "ed25519") {
    throw new Error("The redemption signing key must use Ed25519");
  }
  return key;
}

function writePrivateCsv(outPath, rows) {
  try {
    mkdirSync(dirname(outPath), { recursive: true });
    writeFileSync(outPath, rows.map(csvRow).join("\n"), { mode: 0o600, flag: "wx" });
    chmodSync(outPath, 0o600);
  } catch {
    throw new Error("Unable to create the private redemption CSV without overwriting an existing file");
  }
}

function parseLegacyKeyInput(content) {
  const lines = content.split(/\r?\n/u).filter((line) => line.trim());
  if (lines.length === 0) return [];
  const rows = lines.map((line) => line.startsWith('"') ? parseCsvRow(line) : [line.trim()]);
  const keyColumn = rows[0].indexOf("兑换Key");
  if (keyColumn >= 0) {
    return rows.slice(1).map((row) => row[keyColumn] ?? "").filter(Boolean);
  }
  if (rows.some((row) => row.length !== 1)) {
    throw new Error("Legacy redemption input must be a key list or generated key CSV");
  }
  return rows.map(([value]) => value);
}

function parseCsvRow(line) {
  const fields = [];
  let index = 0;
  while (index < line.length) {
    if (line[index] !== '"') throw new Error("Legacy redemption CSV is malformed");
    index += 1;
    let field = "";
    let closed = false;
    while (index < line.length) {
      if (line[index] !== '"') {
        field += line[index];
        index += 1;
      } else if (line[index + 1] === '"') {
        field += '"';
        index += 2;
      } else {
        index += 1;
        closed = true;
        break;
      }
    }
    if (!closed) throw new Error("Legacy redemption CSV is malformed");
    fields.push(field);
    if (index < line.length) {
      if (line[index] !== ",") throw new Error("Legacy redemption CSV is malformed");
      index += 1;
    }
  }
  return fields;
}

function encodeSignedKey({ campaignId, rewards, expiresAt, privateKey }) {
  const payload = {
    version: 1,
    campaignId,
    nonce: randomBytes(12).toString("hex"),
    issuedAt: new Date().toISOString().slice(0, 10),
    expiresAt,
    rewards,
  };
  return encodeSignedPayload(payload, privateKey);
}

function encodeSignedPayload(payload, privateKey) {
  const encodedPayload = base64UrlEncode(Buffer.from(JSON.stringify(payload)));
  return `LIB1.${encodedPayload}.${base64UrlEncode(sign(null, Buffer.from(encodedPayload), privateKey))}`;
}

function encodeCompactKey({ campaignId, rewards, expiresAt, privateKey }) {
  return signCompactPayload(encodeCompactPayload({ campaignId, rewards, expiresAt }), privateKey);
}

function signCompactPayload(payload, privateKey) {
  const signature = sign(null, payload, privateKey);
  return `LIB2.${base64UrlEncode(Buffer.concat([payload, signature]))}`;
}

function verifyShortKey(key) {
  const parts = key.split("-");
  const displayPrefix = parts.shift()?.toUpperCase() ?? "";
  if (!/^[A-Z0-9]{1,8}$/.test(displayPrefix) || parts.length === 0) {
    throw new Error("Invalid legacy short key");
  }
  const token = decodeCrockfordBase32(parts.join(""));
  if (token.length !== SHORT_TOKEN_BYTES) throw new Error("Invalid legacy short key");
  const payload = token.subarray(0, SHORT_PAYLOAD_BYTES);
  const suppliedMac = token.subarray(SHORT_PAYLOAD_BYTES);
  const expectedMac = createHmac("sha256", Buffer.from(SHORT_KEY_SECRET_B64, "base64"))
    .update(displayPrefix)
    .update(".")
    .update(payload)
    .digest()
    .subarray(0, SHORT_MAC_BYTES);
  if (!timingSafeEqual(suppliedMac, expectedMac)) throw new Error("Invalid legacy short key");
  if (payload[0] !== 3 || payload[1] > 2) throw new Error("Unsupported legacy short key");
  const expiration = payload.readUInt16BE(8);
  const expiresAt = expiration === 0xffff ? null : formatCompactDate(expiration);
  if (expiresAt && expiresAt < new Date().toISOString().slice(0, 10)) {
    throw new Error("Expired legacy short key");
  }
  const itemCount = payload[1];
  const rewards = {
    lp: payload.readUInt16BE(10),
    growthValue: payload.readUInt16BE(12),
    items: [],
  };
  for (let index = 0; index < itemCount; index += 1) {
    const offset = index === 0 ? 14 : 17;
    if (payload.readUInt16BE(offset) === 0 || payload[offset + 2] === 0) {
      throw new Error("Invalid legacy short key rewards");
    }
    rewards.items.push({ code: Buffer.from(payload.subarray(offset, offset + 3)) });
  }
  if (rewards.lp === 0 && rewards.growthValue === 0 && rewards.items.length === 0) {
    throw new Error("Legacy short key has no rewards");
  }
  return { displayPrefix, payload, expiresAt, rewards };
}

function loadCompactItemKeys() {
  const catalogPath = resolve(repositoryRoot, "apps/native/internal/domain/pet_generated.go");
  let catalogSource;
  try {
    catalogSource = readFileSync(catalogPath, "utf8");
  } catch {
    throw new Error("Native pet catalog is unavailable; run pnpm native:pet:generate");
  }
  const encodedCatalog = catalogSource.match(/const petCatalogJSON = ("(?:[^"\\]|\\.)*")/)?.[1];
  if (!encodedCatalog) throw new Error("Native pet catalog is invalid; run pnpm native:pet:generate");
  const catalog = JSON.parse(JSON.parse(encodedCatalog));
  const itemKeys = new Map();
  for (const item of catalog) {
    const code = hashUInt16(item.itemKey);
    if (itemKeys.has(code)) throw new Error("Native pet catalog has ambiguous compact item codes");
    itemKeys.set(code, item.itemKey);
  }
  return itemKeys;
}

function signedPayloadFromShort(displayPrefix, bytes, itemKeys) {
  const items = [];
  for (let index = 0; index < bytes[1]; index += 1) {
    const offset = index === 0 ? 14 : 17;
    const itemCode = bytes.readUInt16BE(offset);
    const itemKey = itemKeys.get(itemCode);
    if (!itemKey) throw new Error("Legacy key contains an unsupported reward item");
    items.push({ itemKey, quantity: bytes[offset + 2] });
  }
  return {
    version: 3,
    campaignId: displayPrefix,
    nonce: bytes.subarray(2, 8).toString("hex"),
    issuedAt: null,
    expiresAt: bytes.readUInt16BE(8) === 0xffff ? null : formatCompactDate(bytes.readUInt16BE(8)),
    rewards: {
      lp: bytes.readUInt16BE(10),
      growthValue: bytes.readUInt16BE(12),
      items,
    },
  };
}

function formatCompactDate(daysSinceBaseValue) {
  const date = new Date(COMPACT_BASE_DATE + daysSinceBaseValue * DAY_MS);
  if (!Number.isFinite(date.getTime())) throw new Error("Invalid legacy short key date");
  return date.toISOString().slice(0, 10);
}

function parseRewards(args) {
  const lp = nonNegativeInteger(args.lp ?? "0", "--lp");
  const growthValue = nonNegativeInteger(args.growth ?? "0", "--growth");
  const items = values(args.item).map((value) => {
    const [itemKey, quantityValue = "1"] = value.split(":");
    if (!itemKey?.trim()) {
      throw new Error("--item must be itemKey or itemKey:quantity");
    }
    return {
      itemKey: itemKey.trim(),
      quantity: positiveInteger(quantityValue, "--item quantity"),
    };
  });
  if (lp <= 0 && growthValue <= 0 && items.length === 0) {
    throw new Error("At least one reward is required: --lp, --growth, or --item");
  }
  return { lp, growthValue, items };
}

function encodeShortKey({ displayPrefix, rewards, expiresAt }) {
  const payload = encodeShortPayload({ rewards, expiresAt });
  const mac = createHmac("sha256", Buffer.from(SHORT_KEY_SECRET_B64, "base64"))
    .update(displayPrefix)
    .update(".")
    .update(payload)
    .digest()
    .subarray(0, SHORT_MAC_BYTES);
  const token = encodeCrockfordBase32(Buffer.concat([payload, mac]));
  const key = `${displayPrefix}-${chunkText(token, SHORT_TOKEN_CHUNK_SIZE).join("-")}`;
  if (key.length > 64) {
    throw new Error(`Unexpected short redeem key length: ${key.length}`);
  }
  return key;
}

function encodeShortPayload({ rewards, expiresAt }) {
  if (rewards.lp > 65_535) {
    throw new Error("--lp must be <= 65535 for short keys");
  }
  if (rewards.growthValue > 65_535) {
    throw new Error("--growth must be <= 65535 for short keys");
  }
  if (rewards.items.length > 2) {
    throw new Error("Short keys support at most 2 item reward types");
  }

  const buffer = Buffer.alloc(SHORT_PAYLOAD_BYTES);
  buffer.writeUInt8(3, 0);
  buffer.writeUInt8(rewards.items.length, 1);
  randomBytes(6).copy(buffer, 2);
  buffer.writeUInt16BE(expiresAt ? daysSinceBase(expiresAt, "--expires") : 0xffff, 8);
  buffer.writeUInt16BE(rewards.lp, 10);
  buffer.writeUInt16BE(rewards.growthValue, 12);

  rewards.items.forEach((item, index) => {
    if (item.quantity > 255) {
      throw new Error(`--item quantity for ${item.itemKey} must be <= 255 for short keys`);
    }
    const offset = index === 0 ? 14 : 17;
    buffer.writeUInt16BE(hashUInt16(item.itemKey), offset);
    buffer.writeUInt8(item.quantity, offset + 2);
  });

  return buffer;
}

function encodeCompactPayload({ campaignId, rewards, expiresAt }) {
  if (rewards.lp > 65_535) {
    throw new Error("--lp must be <= 65535 for compact keys");
  }
  if (rewards.growthValue > 65_535) {
    throw new Error("--growth must be <= 65535 for compact keys");
  }
  if (rewards.items.length > 2) {
    throw new Error("Compact keys support at most 2 item reward types");
  }

  const buffer = Buffer.alloc(COMPACT_PAYLOAD_BYTES);
  buffer.writeUInt8(2, 0);
  buffer.writeUInt8(rewards.items.length, 1);
  buffer.writeUInt32BE(hashUInt32(campaignId), 2);
  randomBytes(8).copy(buffer, 6);
  buffer.writeUInt16BE(daysSinceBase(new Date().toISOString().slice(0, 10), "--issuedAt"), 14);
  buffer.writeUInt16BE(expiresAt ? daysSinceBase(expiresAt, "--expires") : 0xffff, 16);
  buffer.writeUInt16BE(rewards.lp, 18);
  buffer.writeUInt16BE(rewards.growthValue, 20);

  rewards.items.forEach((item, index) => {
    if (item.quantity > 255) {
      throw new Error(`--item quantity for ${item.itemKey} must be <= 255 for compact keys`);
    }
    const offset = index === 0 ? 22 : 25;
    buffer.writeUInt16BE(hashUInt16(item.itemKey), offset);
    buffer.writeUInt8(item.quantity, offset + 2);
  });

  return buffer;
}

function normalizeDisplayPrefix(value) {
  const normalized = String(value).toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 8);
  if (!normalized) {
    throw new Error("--prefix must contain at least one letter or number");
  }
  return normalized;
}

function chunkText(value, size) {
  const chunks = [];
  for (let index = 0; index < value.length; index += size) {
    chunks.push(value.slice(index, index + size));
  }
  return chunks;
}

function encodeCrockfordBase32(buffer) {
  let output = "";
  let value = 0;
  let bits = 0;
  for (const byte of buffer) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      output += SHORT_ALPHABET[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) {
    output += SHORT_ALPHABET[(value << (5 - bits)) & 31];
  }
  return output;
}

function decodeCrockfordBase32(value) {
  const bytes = [];
  let buffer = 0;
  let bits = 0;
  for (const character of value) {
    const digit = crockfordValue(character);
    if (digit === null) throw new Error("Invalid legacy short key");
    buffer = (buffer << 5) | digit;
    bits += 5;
    while (bits >= 8) {
      bits -= 8;
      bytes.push((buffer >>> bits) & 0xff);
    }
  }
  if (bits > 0 && (buffer & ((1 << bits) - 1)) !== 0) {
    throw new Error("Invalid legacy short key");
  }
  return Buffer.from(bytes);
}

function crockfordValue(character) {
  const normalized = character.toUpperCase();
  if (normalized === "O") return 0;
  if (["I", "L"].includes(normalized)) return 1;
  return "0123456789ABCDEFGHJKMNPQRSTVWXYZ".indexOf(normalized) >= 0
    ? "0123456789ABCDEFGHJKMNPQRSTVWXYZ".indexOf(normalized)
    : null;
}

function hashUInt32(value) {
  return createHash("sha256").update(value).digest().readUInt32BE(0);
}

function hashUInt16(value) {
  const code = createHash("sha256").update(value).digest().readUInt16BE(0);
  if (code === 0) {
    throw new Error(`Item key ${value} produced a reserved compact code`);
  }
  return code;
}

function daysSinceBase(value, flag) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    throw new Error(`${flag} must use YYYY-MM-DD`);
  }
  const date = Date.parse(`${value}T00:00:00.000Z`);
  if (!Number.isFinite(date)) {
    throw new Error(`${flag} is invalid`);
  }
  const days = Math.floor((date - COMPACT_BASE_DATE) / DAY_MS);
  if (days < 0 || days > 65_534) {
    throw new Error(`${flag} must be between 2026-01-01 and 2205-06-05`);
  }
  return days;
}

function parseArgs(values) {
  const result = {};
  for (let index = 0; index < values.length; index += 1) {
    const current = values[index];
    if (!current.startsWith("--")) {
      continue;
    }
    const key = current.slice(2);
    const next = values[index + 1];
    const value = next && !next.startsWith("--") ? next : "true";
    if (Object.prototype.hasOwnProperty.call(result, key)) {
      result[key] = Array.isArray(result[key]) ? [...result[key], value] : [result[key], value];
    } else {
      result[key] = value;
    }
    if (value !== "true") {
      index += 1;
    }
  }
  return result;
}

function values(value) {
  if (!value) {
    return [];
  }
  return Array.isArray(value) ? value : [value];
}

function required(value, flag) {
  if (!value || value === "true") {
    throw new Error(`${flag} is required`);
  }
  return value;
}

function positiveInteger(value, flag) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${flag} must be a positive integer`);
  }
  return parsed;
}

function nonNegativeInteger(value, flag) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) {
    throw new Error(`${flag} must be a non-negative integer`);
  }
  return parsed;
}

function base64UrlEncode(buffer) {
  return buffer.toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

function rewardSummary(rewards) {
  const parts = [
    rewards.lp > 0 ? `LP +${rewards.lp}` : "",
    rewards.growthValue > 0 ? `成长值 +${rewards.growthValue}` : "",
    ...rewards.items.map((item) => `${itemNameZh(item.itemKey)} x${item.quantity}`),
  ].filter(Boolean);
  return parts.join("、");
}

function itemNameZh(itemKey) {
  return ITEM_NAME_ZH[itemKey] ?? itemKey;
}

function csvRow(values) {
  return values.map((value) => `"${String(value).replace(/"/g, '""')}"`).join(",");
}

function printHelp() {
  console.log(`
Usage:
  node scripts/redeem-key.mjs keypair
  node scripts/redeem-key.mjs generate --campaign 2026-gift --prefix LP2026 --lp 120 --growth 20 --item gem-ticket-tool:2 --count 10 --expires 2026-12-31
  node scripts/redeem-key.mjs reissue --input <legacy-short-key.csv-or-list.txt>

New keys default to Ed25519-signed LIB1. Use --format compact for LIB2. Legacy short keys are only for existing clients.
The reissue command verifies legacy short keys locally and produces equivalent Ed25519-signed LIB1 keys.
Keep the input and generated CSV private; the output refuses to overwrite an existing file.

Options:
  --privateKey <path>   Private key PEM path. Default: ${DEFAULT_PRIVATE_KEY_PATH}
  --publicKey <path>    Public key output path for keypair. Default: ${DEFAULT_PUBLIC_KEY_PATH}
  --format <format>     New key format: signed (default), compact, or short
  --input <path>        Private UTF-8 file with generated key CSV or one key per line
  --prefix <text>       User-facing key prefix, max 8 letters/numbers. Example: LP2026
  --out <path>          CSV output path. Default: ${DEFAULT_OUT_PATH}
                        Reissue output defaults to ${DEFAULT_REISSUED_OUT_PATH}
`);
}
