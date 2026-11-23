const maxLevel = 255;
const slots = new Map([
  ['pet', 'pet'], ['cosmetic', 'accessory'], ['theme', 'scene'], ['tool', 'consumable'],
  ['food', 'consumable'], ['seed', 'consumable'], ['badge', 'badge'],
]);
const rarities = new Set(['first_meet', 'familiar', 'grow_together', 'deep_bond', 'bond_forever']);
const counters = new Set([
  'tasks_created', 'transcriptions_completed', 'summaries_completed', 'exports_completed',
  'active_days', 'check_in_streak', 'dark_theme_days',
]);
const catalogFields = {
  itemKey: 'string', itemType: 'string', slot: 'string', nameZh: 'string', nameEn: 'string',
  descriptionZh: 'string', descriptionEn: 'string', rarity: 'string', priceLp: 'number',
  levelGate: 'number', stageGate: 'string', milestoneGate: 'string', assetKey: 'string',
  growthValue: 'number', sortOrder: 'number', enabled: 'boolean',
};
const stageFields = { stage: 'string', labelZh: 'string', labelEn: 'string', level: 'number' };

function records(source, filename, fields) {
  const data = JSON.parse(source, (key, value, context) => {
    if (typeof value === 'number' && (!Number.isSafeInteger(value) || !/^(0|[1-9]\d*)$/.test(context.source))) {
      throw new Error(`${filename}: ${key} must be a non-negative safe integer`);
    }
    if (typeof value === 'string' && !value.isWellFormed()) {
      throw new Error(`${filename}: ${key} must be valid Unicode`);
    }
    return value;
  });
  // JSON.parse keeps only the last value for a repeated member. Inspect keys in the validated text.
  const objects = [];
  for (const [, literal, colon, brace] of source.matchAll(/("(?:[^"\\]|\\.)*")\s*(:)?|([{}])/gsu)) {
    if (brace === '{') objects.push(new Set());
    if (brace === '}') objects.pop();
    if (colon) {
      const key = JSON.parse(literal);
      const keys = objects.at(-1);
      if (keys.has(key)) throw new Error(`${filename}: duplicate field ${key}`);
      keys.add(key);
    }
  }
  if (!Array.isArray(data) || data.length === 0) throw new Error(`${filename}: expected a nonempty array`);
  return data.map((record, index) => {
    const location = `${filename}[${index}]`;
    if (!record || typeof record !== 'object' || Array.isArray(record)) throw new Error(`${location}: expected an object`);
    for (const key of Object.keys(record)) {
      if (!Object.hasOwn(fields, key)) throw new Error(`${location}: unknown field ${key}`);
    }
    return Object.fromEntries(Object.entries(fields).map(([key, type]) => {
      if (!Object.hasOwn(record, key) || typeof record[key] !== type) {
        throw new Error(`${location}: ${key} must be a ${type}`);
      }
      return [key, record[key]];
    }));
  });
}

export function parseStages(source) {
  const stages = records(source, 'stages.json', stageFields);
  if (stages.length !== 8 || stages[0].level !== 1) throw new Error('stages.json: expected eight stages starting at level 1');
  const keys = new Set();
  let previous = 0;
  for (const { stage, labelZh, labelEn, level } of stages) {
    if (!/^[a-z0-9]+(?:_[a-z0-9]+)*$/.test(stage) || ['baby', 'growing', 'mature'].includes(stage) || keys.has(stage)) {
      throw new Error(`stages.json: invalid or duplicate stage ${stage}`);
    }
    if (!labelZh.trim() || !labelEn.trim() || level <= previous || level > maxLevel) {
      throw new Error(`stages.json: invalid labels or threshold for ${stage}`);
    }
    keys.add(stage);
    previous = level;
  }
  return stages;
}

export function parseCatalog(source, stages) {
  const items = records(source, 'catalog.json', catalogFields);
  const keys = new Set();
  const stageKeys = new Set(stages.map(({ stage }) => stage));
  for (const item of items) {
    const { itemKey, itemType, slot, rarity, stageGate, milestoneGate, levelGate, growthValue } = item;
    if (!itemKey.trim() || keys.has(itemKey)) throw new Error(`catalog.json: invalid or duplicate itemKey ${itemKey}`);
    keys.add(itemKey);
    if (slots.get(itemType) !== slot || !rarities.has(rarity) || levelGate < 1 || levelGate > maxLevel
        || (itemType !== 'food' && growthValue !== 0)
        || [item.nameZh, item.nameEn, item.descriptionZh, item.descriptionEn, item.assetKey].some(value => !value.trim())) {
      throw new Error(`catalog.json: invalid attributes for ${itemKey}`);
    }
    if (stageGate !== '' && !stageKeys.has(stageGate)) throw new Error(`catalog.json: invalid stageGate for ${itemKey}`);
    if (milestoneGate !== '') {
      const [counter, threshold, extra] = milestoneGate.split(':');
      if (!counters.has(counter) || extra !== undefined || !/^[1-9]\d*$/.test(threshold) || !Number.isSafeInteger(Number(threshold))) {
        throw new Error(`catalog.json: invalid milestoneGate for ${itemKey}`);
      }
    }
  }
  return items;
}
