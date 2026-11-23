// Deliberately accept a small, unambiguous YAML subset. Anchors, aliases, tags,
// flow collections and duplicate keys cannot hide policy overrides.
export function parseWorkflow(source) {
  const lines = source.replaceAll('\r\n', '\n').split('\n');
  let index = 0;
  const indent = line => line.match(/^ */)[0].length;
  const skip = () => { while (index < lines.length && /^\s*(?:#.*)?$/.test(lines[index])) index++; };
  function scalar(text) {
    if (text.startsWith('"')) return JSON.parse(text);
    if (text.startsWith("'")) {
      if (!/^'(?:[^']|'')*'$/.test(text)) throw new Error('Invalid workflow quote');
      return text.slice(1, -1).replaceAll("''", "'");
    }
    if (/^[&*!\[\]{},]|\s#/.test(text)) throw new Error('Unsupported workflow YAML construct');
    if (text === 'true' || text === 'false') return text === 'true';
    if (/^\d+$/.test(text)) return Number(text);
    return text;
  }
  function entry(text) {
    const match = text.match(/^([A-Za-z][A-Za-z0-9_-]*):(?: +(.*))?$/);
    if (!match) throw new Error('Unsupported workflow mapping');
    return [match[1], match[2]];
  }
  function value(text, level) {
    if (text === '|') {
      const result = [];
      while (index < lines.length && (!lines[index].trim() || indent(lines[index]) > level)) {
        if (lines[index].trim() && indent(lines[index]) < level + 2) throw new Error('Invalid workflow block indentation');
        result.push(lines[index].slice(level + 2));
        index++;
      }
      return result.join('\n').trimEnd();
    }
    if (text !== undefined) return scalar(text);
    skip();
    return index < lines.length && indent(lines[index]) > level ? block(level + 2) : null;
  }
  function mapping(level, initial) {
    const result = {};
    if (initial) result[initial[0]] = value(initial[1], level);
    while (true) {
      skip();
      if (index >= lines.length || indent(lines[index]) < level) break;
      if (indent(lines[index]) !== level || lines[index].trimStart().startsWith('- ')) throw new Error('Invalid workflow mapping indentation');
      const [key, text] = entry(lines[index].slice(level));
      if (Object.hasOwn(result, key)) throw new Error(`Duplicate workflow key: ${key}`);
      index++;
      result[key] = value(text, level);
    }
    return result;
  }
  function block(level) {
    skip();
    if (indent(lines[index]) !== level) throw new Error('Invalid workflow indentation');
    if (!lines[index].slice(level).startsWith('- ')) return mapping(level);
    const result = [];
    while (true) {
      skip();
      if (index >= lines.length || indent(lines[index]) < level) break;
      if (indent(lines[index]) !== level || !lines[index].slice(level).startsWith('- ')) throw new Error('Invalid workflow sequence');
      const text = lines[index].slice(level + 2);
      index++;
      if (/^[A-Za-z][A-Za-z0-9_-]*:/.test(text)) result.push(mapping(level + 2, entry(text)));
      else result.push(scalar(text));
    }
    return result;
  }
  if (source.includes('\t')) throw new Error('Workflow tabs are prohibited');
  const result = block(0);
  skip();
  if (index !== lines.length) throw new Error('Unexpected workflow content');
  return result;
}
