import { readFile } from "node:fs/promises";

export const CARGO_DENY_SECURITY_CHECKS = Object.freeze(["advisories", "bans", "sources"]);

export const OSV_LOCK_FILES = Object.freeze([
  "pnpm-lock.yaml",
  "apps/native/go.mod",
  "python/funasr-runner/requirements-lock-darwin-aarch64.txt",
  "python/funasr-runner/requirements-lock-darwin-x64.txt",
  "python/funasr-runner/requirements-lock-windows-x64.txt",
]);

export const OSV_EXCEPTION_CONFIG = "python/funasr-runner/osv-scanner.toml";

export const PYTHON_LOCK_FILES = Object.freeze([
  "python/funasr-runner/requirements-dev-lock.txt",
  ...OSV_LOCK_FILES.filter((file) => file.startsWith("python/")),
]);

export function dependencyGovernanceOptions(args) {
  const native = args.includes("--native");
  const modes = args.filter((argument) => argument !== "--native");
  if (args.filter((argument) => argument === "--native").length > 1 || modes.length > 1 || (modes.length && !["locks", "licenses", "security"].includes(modes[0]))) {
    throw new Error("Expected [locks|licenses|security] [--native]");
  }
  return { mode: modes[0] ?? "security", native };
}

// Read the pnpm v9 importer/snapshot subset used by the root build tools.
// Unknown or unresolved sources fail instead of silently omitting dependencies.
export function nativeNodeDependencies(content, rootPackage) {
  const scalar = (value) => value.startsWith("'") && value.endsWith("'") ? value.slice(1, -1).replaceAll("''", "'") : value;
  const importers = content.match(/^importers:\n([\s\S]*?)(?=^\S|$(?![\s\S]))/m)?.[1];
  const importer = importers?.match(/^  \.:\n([\s\S]*?)(?=^  \S|$(?![\s\S]))/m)?.[1];
  const snapshots = content.match(/^snapshots:\n([\s\S]*)$/m)?.[1];
  if (!importer || !snapshots) throw new Error("Native SBOM requires the root pnpm importer and snapshots");
  const packageSection = content.match(/^packages:\n([\s\S]*?)(?=^\S|$(?![\s\S]))/m)?.[1];
  const packages = new Set([...(packageSection ?? "").matchAll(/^  (\S.*):\n    resolution:/gm)].map((entry) => scalar(entry[1])));
  const locked = new Map();
  for (const entry of importer.matchAll(/^      (.+):\n        specifier: (.+)\n        version: (.+)$/gm)) {
    locked.set(scalar(entry[1]), { specifier: scalar(entry[2]), version: scalar(entry[3]) });
  }
  const graph = new Map();
  let current;
  let dependencies = false;
  for (const line of snapshots.split("\n")) {
    const header = line.match(/^  (\S.*):(?: \{\})?$/);
    if (header) {
      current = scalar(header[1]);
      if (graph.has(current)) throw new Error("Duplicate pnpm snapshot");
      graph.set(current, []);
      dependencies = false;
    } else if (/^    \S/.test(line)) {
      dependencies = /^    (?:dependencies|optionalDependencies):$/.test(line);
    } else if (dependencies && line.trim()) {
      const dependency = line.match(/^      (.+): (.+)$/);
      if (!dependency || !current) throw new Error("Unsupported pnpm dependency snapshot");
      graph.get(current).push([scalar(dependency[1]), scalar(dependency[2])]);
    }
  }
  const pending = [];
  for (const [name, specifier] of Object.entries({ ...rootPackage.dependencies, ...rootPackage.devDependencies, ...rootPackage.optionalDependencies })) {
    const pin = locked.get(name);
    if (!pin || pin.specifier !== specifier) throw new Error(`Native build dependency differs from the root lock: ${name}`);
    pending.push([name, pin.version]);
  }
  if (!pending.length) throw new Error("Native SBOM contains no root build dependencies");
  const visited = new Set();
  const result = new Map();
  while (pending.length) {
    const [name, resolved] = pending.pop();
    const key = `${name}@${resolved}`;
    if (visited.has(key)) continue;
    visited.add(key);
    const version = resolved.split("(")[0];
    if (!/^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$/.test(version) || !graph.has(key) || !packages.has(`${name}@${version}`)) throw new Error(`Unresolved Native build dependency: ${key}`);
    result.set(`${name}@${version}`, { name, version });
    pending.push(...graph.get(key));
  }
  return [...result.values()];
}

export function parseGoModules(content) {
  if (/^\s*(?:replace|exclude)\b/m.test(content)) {
    throw new Error("Native Go dependencies must not bypass the locked module source");
  }
  const modules = [];
  let block = false;
  for (const raw of content.split("\n")) {
    const line = raw.replace(/\/\/.*$/, "").trim();
    if (line === "require (") { block = true; continue; }
    if (line === ")") { block = false; continue; }
    if (!line || (!block && !line.startsWith("require "))) continue;
    const match = line.replace(/^require\s+/, "").match(/^(\S+)\s+(v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?(?:\+incompatible)?)$/);
    if (!match) throw new Error("Unpinned Go dependency");
    modules.push({ name: match[1], version: match[2] });
  }
  if (!modules.length) throw new Error("Native Go module contains no locked dependencies");
  return modules;
}

export function assertGoLockHasIntegrity(module, sum) {
  const hashes = new Set(sum.split("\n").filter((line) => / h1:[A-Za-z0-9+/]{43}=$/.test(line)).map((line) => line.split(" ").slice(0, 2).join(" ")));
  for (const { name, version } of parseGoModules(module)) {
    for (const suffix of ["", "/go.mod"]) {
      if (!hashes.has(`${name} ${version}${suffix}`)) throw new Error(`Go dependency lacks checksum: ${name}@${version}${suffix}`);
    }
  }
}

export function evaluateDependencyFindings(policy, findings) {
  const deniedLicenses = new Set(policy.deniedLicenses);
  const allowedSources = new Set(policy.allowedSourceKinds);
  const blockedSeverities = new Set(policy.vulnerabilitySeverityBlocklist);
  const violations = [];
  for (const finding of findings) {
    if (finding.kind === "license" && deniedLicenses.has(finding.license)) {
      violations.push(`denied license ${finding.license}: ${finding.package}`);
    }
    if (finding.kind === "source" && !allowedSources.has(finding.sourceKind)) {
      violations.push(`unlocked source ${finding.sourceKind}: ${finding.package}`);
    }
    if (finding.kind === "vulnerability" && blockedSeverities.has(finding.severity)) {
      violations.push(`known vulnerability ${finding.id}: ${finding.package}`);
    }
  }
  return violations;
}

export function assertPythonLockHasHashes(content, fileName) {
  const entries = content
    .split("\n")
    .filter((line) => /^[A-Za-z0-9_.-]+==[^\s]+/.test(line));
  if (entries.length === 0) {
    throw new Error(`${fileName} contains no pinned packages`);
  }
  for (const entry of entries) {
    const start = content.indexOf(entry);
    const nextEntry = content.slice(start + entry.length).search(/\n[A-Za-z0-9_.-]+==/);
    const block = nextEntry < 0
      ? content.slice(start)
      : content.slice(start, start + entry.length + nextEntry);
    if (!block.includes("--hash=sha256:")) {
      throw new Error(`${fileName} has an unhashed entry: ${entry.split(" ")[0]}`);
    }
  }
}

export function assertNodeLockHasIntegrity(content) {
  const packageSection = content.split("\nsnapshots:")[0] ?? content;
  const packageEntries = packageSection.match(/^  [^\s].*:\n(?:    .*\n)*/gm) ?? [];
  const missing = packageEntries
    .filter((entry) => /resolution:/.test(entry))
    .filter((entry) => !/integrity:/.test(entry) && !/tarball:/.test(entry));
  if (missing.length > 0) {
    throw new Error(`pnpm lock has ${missing.length} resolved package(s) without integrity`);
  }
}

export function assertOsvExceptionsAreGoverned(content, now = new Date()) {
  if (/^\s*(?:ignore|vulnerability\.ignore)\s*=\s*true\s*$/m.test(content)) {
    throw new Error("OSV policy must not ignore an entire package");
  }

  const entries = content.split(/^\[\[IgnoredVulns\]\]\s*$/m).slice(1);
  if (entries.length === 0) {
    throw new Error("OSV policy contains no vulnerability-specific exceptions");
  }

  const seenIds = new Set();
  for (const entry of entries) {
    const id = entry.match(/^id\s*=\s*"([^"]+)"\s*$/m)?.[1];
    const reason = entry.match(/^reason\s*=\s*"([^"]+)"\s*$/m)?.[1];
    const expiry = entry.match(/^ignoreUntil\s*=\s*(\d{4}-\d{2}-\d{2})\s*$/m)?.[1];
    if (!id || !/^(?:GHSA|PYSEC)-[A-Za-z0-9-]+$/.test(id)) {
      throw new Error("Every OSV exception must identify one GHSA or PYSEC vulnerability");
    }
    if (seenIds.has(id)) {
      throw new Error(`OSV policy contains duplicate exception ${id}`);
    }
    seenIds.add(id);
    if (!reason || reason.trim().length < 20) {
      throw new Error(`OSV exception ${id} must include a meaningful reason`);
    }
    if (!expiry) {
      throw new Error(`OSV exception ${id} must include ignoreUntil`);
    }
    const expiryInstant = new Date(`${expiry}T23:59:59Z`);
    if (Number.isNaN(expiryInstant.getTime()) || expiryInstant <= now) {
      throw new Error(`OSV exception ${id} expired on ${expiry}`);
    }
  }
}

export async function readPolicy(path) {
  return JSON.parse(await readFile(path, "utf8"));
}
