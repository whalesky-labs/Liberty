import assert from "node:assert/strict";
import { createPrivateKey, createPublicKey, verify } from "node:crypto";
import { chmodSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const scriptPath = path.join(repositoryRoot, "scripts/redeem-key.mjs");
const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), "liberty-redeem-key-test-"));

try {
  const privateKeyPath = path.join(temporaryRoot, "issuer-private.pem");
  const publicKeyPath = path.join(temporaryRoot, "issuer-public.base64");
  run("keypair", "--privateKey", privateKeyPath, "--publicKey", publicKeyPath);
  const privateKey = createPrivateKey(readFileSync(privateKeyPath));
  const publicKey = createPublicKey(privateKey);
  if (process.platform !== "win32") assert.equal(statSync(privateKeyPath).mode & 0o777, 0o600);
  const originalPrivateKey = readFileSync(privateKeyPath);
  assert.notEqual(runResult("keypair", "--privateKey", privateKeyPath, "--publicKey", publicKeyPath).status, 0);
  assert.deepEqual(readFileSync(privateKeyPath), originalPrivateKey);

  const signedPath = path.join(temporaryRoot, "signed.csv");
  run("generate", "--campaign", "synthetic-campaign", "--lp", "40", "--growth", "7", "--item", "gem-ticket-tool:2", "--out", signedPath, "--privateKey", privateKeyPath);
  const signedCode = readCsv(signedPath)[1][1];
  const signedPayload = verifyLegacy(signedCode, publicKey);
  assert.equal(signedPayload.version, 1);
  assert.equal(signedPayload.campaignId, "synthetic-campaign");
  assert.equal(signedPayload.rewards.lp, 40);

  const compactPath = path.join(temporaryRoot, "compact.csv");
  run("generate", "--campaign", "synthetic-campaign", "--lp", "40", "--format", "compact", "--out", compactPath, "--privateKey", privateKeyPath);
  assert.match(readCsv(compactPath)[1][1], /^LIB2\./);

  const shortPath = path.join(temporaryRoot, "short.csv");
  run("generate", "--campaign", "synthetic-campaign", "--prefix", "SYNTH", "--lp", "40", "--growth", "7", "--item", "gem-ticket-tool:2", "--format", "short", "--out", shortPath);
  const shortCode = readCsv(shortPath)[1][1];
  chmodSync(shortPath, 0o600);
  const reissuedPath = path.join(temporaryRoot, "reissued.csv");
  const reissueResult = run("reissue", "--input", shortPath, "--privateKey", privateKeyPath, "--out", reissuedPath);
  assert.equal(reissueResult.stdout.includes(shortCode), false);
  const [sourceCode, reissuedCode] = readCsv(reissuedPath)[1];
  assert.equal(sourceCode, shortCode);
  const reissuedPayload = verifyLegacy(reissuedCode, publicKey);
  assert.equal(reissuedPayload.version, 3);
  assert.equal(reissuedPayload.campaignId, "SYNTH");
  assert.equal(reissuedPayload.nonce.length, 12);
  assert.deepEqual(reissuedPayload.rewards, {
    lp: 40,
    growthValue: 7,
    items: [{ itemKey: "gem-ticket-tool", quantity: 2 }],
  });
  if (process.platform !== "win32") assert.equal(statSync(reissuedPath).mode & 0o777, 0o600);

  const repeatedPath = path.join(temporaryRoot, "reissued-again.csv");
  run("reissue", "--input", shortPath, "--privateKey", privateKeyPath, "--out", repeatedPath);
  assert.equal(readCsv(repeatedPath)[1][1], reissuedCode);

  const listInput = path.join(temporaryRoot, "short-list.txt");
  writeFileSync(listInput, `${shortCode}\n`, { mode: 0o600 });
  const listOutput = path.join(temporaryRoot, "reissued-from-list.csv");
  run("reissue", "--input", listInput, "--privateKey", privateKeyPath, "--out", listOutput);
  assert.equal(readCsv(listOutput)[1][1], reissuedCode);

  const damagedCode = `${shortCode.slice(0, -1)}${shortCode.endsWith("0") ? "1" : "0"}`;
  const damagedInput = path.join(temporaryRoot, "damaged.txt");
  writeFileSync(damagedInput, `${damagedCode}\n`, { mode: 0o600 });
  const rejectedPath = path.join(temporaryRoot, "rejected.csv");
  const rejected = runResult("reissue", "--input", damagedInput, "--privateKey", privateKeyPath, "--out", rejectedPath);
  assert.notEqual(rejected.status, 0);
  assert.equal(rejected.stdout.includes(damagedCode), false);
  assert.match(rejected.stderr, /Invalid legacy short key/);

  const duplicateInput = path.join(temporaryRoot, "duplicate-list.txt");
  writeFileSync(duplicateInput, `${shortCode}\n ${shortCode} \n`, { mode: 0o600 });
  assert.notEqual(runResult("reissue", "--input", duplicateInput, "--privateKey", privateKeyPath, "--out", rejectedPath).status, 0);

  const expiredPath = path.join(temporaryRoot, "expired.csv");
  run("generate", "--campaign", "synthetic-campaign", "--prefix", "SYNTH", "--lp", "1", "--expires", "2026-01-02", "--format", "short", "--out", expiredPath);
  chmodSync(expiredPath, 0o600);
  const expiredOutput = path.join(temporaryRoot, "expired-reissue.csv");
  assert.notEqual(runResult("reissue", "--input", expiredPath, "--privateKey", privateKeyPath, "--out", expiredOutput).status, 0);

  assert.notEqual(runResult("reissue", "--input", shortPath, "--privateKey", privateKeyPath, "--out", reissuedPath).status, 0);

  console.log("Redemption signing, secure file modes, legacy reissue, idempotency, and rejection tests passed.");
} finally {
  rmSync(temporaryRoot, { recursive: true, force: true });
}

function run(...args) {
  const result = runResult(...args);
  if (result.status !== 0) throw new Error(`Redemption command failed (${args[0]}): ${result.stderr || "unknown error"}`);
  return result;
}

function runResult(...args) {
  return spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: repositoryRoot,
    encoding: "utf8",
    timeout: 30_000,
  });
}

function readCsv(filePath) {
  return readFileSync(filePath, "utf8").split("\n").filter(Boolean).map((line) =>
    [...line.matchAll(/"((?:[^"]|"")*)"/g)].map((match) => match[1].replaceAll('""', '"')),
  );
}

function verifyLegacy(code, publicKey) {
  const [prefix, encodedPayload, encodedSignature, extra] = code.split(".");
  assert.equal(prefix, "LIB1");
  assert.equal(extra, undefined);
  assert.ok(verify(null, Buffer.from(encodedPayload), publicKey, Buffer.from(encodedSignature, "base64url")));
  return JSON.parse(Buffer.from(encodedPayload, "base64url").toString("utf8"));
}
