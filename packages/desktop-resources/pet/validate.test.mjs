import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parseCatalog, parseStages } from './validate.mjs';

const catalogSource = await readFile(new URL('./catalog.json', import.meta.url), 'utf8');
const stageSource = await readFile(new URL('./stages.json', import.meta.url), 'utf8');
const stages = parseStages(stageSource);
const items = JSON.parse(catalogSource);

test('validates every catalog and stage field without changing array order', () => {
  assert.deepEqual(stages, JSON.parse(stageSource));
  assert.deepEqual(parseCatalog(catalogSource, stages), items);
  const reordered = items.map(item => Object.fromEntries(Object.entries(item).reverse()));
  assert.equal(JSON.stringify(parseCatalog(JSON.stringify(reordered), stages)), JSON.stringify(items));
});

test('rejects malformed, missing, extra and duplicate fields in catalog records', () => {
  const item = items[0];
  const { enabled, ...missingField } = item;
  for (const source of [
    '[]', 'null', '[{}', JSON.stringify([Object.values(item)]),
    JSON.stringify([missingField]), JSON.stringify([{ ...item, unknown: true }]),
    JSON.stringify([{ ...item, enabled: null }]), JSON.stringify([{ ...item, nameZh: '\ud800' }]),
    JSON.stringify([item]).replace('"enabled":true', '"enabled":false,"enabl\\u0065d":true'),
    ...['-0', '-1', '1.0', '1e1', '9007199254740992'].map(value => (
      JSON.stringify([item]).replace('"priceLp":0', `"priceLp":${value}`)
    )),
  ]) {
    assert.throws(() => parseCatalog(source, stages));
  }
});

test('rejects duplicate item keys and invalid catalog attributes or references', () => {
  const item = items[0];
  assert.throws(() => parseCatalog(JSON.stringify([item, item]), stages), /duplicate itemKey/);
  for (const fields of [
    { levelGate: 256 }, { levelGate: 0 }, { itemType: 'food' }, { slot: 'scene' },
    { rarity: 'unknown' }, { growthValue: 1 }, { nameEn: ' ' },
    { stageGate: 'baby' }, { stageGate: 'missing_stage' },
    { milestoneGate: 'tasks_created' }, { milestoneGate: 'tasks_created:01' },
    { milestoneGate: 'tasks_created:0' }, { milestoneGate: 'invented_counter:1' },
  ]) {
    assert.throws(() => parseCatalog(JSON.stringify([{ ...item, ...fields }]), stages), /catalog\.json/);
  }
});

test('rejects missing stages, duplicate keys and unordered or invalid thresholds', () => {
  assert.throws(() => parseStages(JSON.stringify(stages.slice(1))), /eight stages/);
  assert.throws(() => parseStages(JSON.stringify(stages.map(Object.values))), /object/);
  for (const fields of [
    { stage: stages[0].stage }, { stage: 'growing' }, { level: 1 }, { level: 256 },
    { level: '11' }, { labelZh: '' }, { level: undefined }, { threshold: 11 },
  ]) {
    const invalid = structuredClone(stages);
    Object.assign(invalid[1], fields);
    assert.throws(() => parseStages(JSON.stringify(invalid)), /stages\.json/);
  }
  const duplicate = stageSource.replace('"level": 11', '"level": 1, "level": 11');
  assert.throws(() => parseStages(duplicate), /duplicate field level/);
});
