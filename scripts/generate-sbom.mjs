import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { assertGoLockHasIntegrity, assertNodeLockHasIntegrity, assertPythonLockHasHashes, nativeNodeDependencies, parseGoModules, PYTHON_LOCK_FILES } from "./lib/dependency-governance.mjs";
import { loadRuntimeInputManifest, readProductionRuntimeScanInputs } from "./lib/native-runtime-inputs.mjs";
import { translationComponents } from "./lib/live-translation-inputs.mjs";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
let native = false;
let outputPath;
const args = process.argv.slice(2);
for (let index = 0; index < args.length; index++) {
  if (args[index] === "--native" && !native) native = true;
  else if (args[index] === "--output" && !outputPath && args[index + 1] && !args[index + 1].startsWith("--")) outputPath = path.resolve(process.cwd(), args[++index]);
  else throw new Error("Expected [--native] [--output <file>]");
}
outputPath ??= path.join(repositoryRoot, `artifacts/sbom/${native ? "liberty-native" : "liberty"}.cdx.json`);

const components = new Map();
const rootPackage = JSON.parse(await readFile(path.join(repositoryRoot, "package.json"), "utf8"));
const productionSources = await readProductionRuntimeScanInputs(repositoryRoot, await loadRuntimeInputManifest(repositoryRoot));
const add = (ecosystem, name, version, source, role) => {
  if (!name || !version) return;
  const type = ecosystem;
  const key = `${type}:${name}@${version}`;
  const existing = components.get(key);
  const properties = native ? [...(existing?.properties ?? [])] : undefined;
  if (native) {
    for (const [name, value] of [["liberty:source-lock", source], ["liberty:dependency-role", role]]) {
      if (!properties.some((property) => property.name === name && property.value === value)) properties.push({ name, value });
    }
  }
  components.set(key, {
    type: "library",
    name,
    version,
    purl: `pkg:${type}/${encodePurlName(type, name)}@${encodeURIComponent(version)}`,
    "bom-ref": `pkg:${type}/${encodePurlName(type, name)}@${encodeURIComponent(version)}`,
    ...(native ? { properties } : {}),
  });
};

if (!native) {
  const cargoLock = await readFile(path.join(repositoryRoot, "Cargo.lock"), "utf8");
  for (const block of cargoLock.split("[[package]]").slice(1)) {
    add("cargo", block.match(/\nname = "([^"]+)"/)?.[1], block.match(/\nversion = "([^"]+)"/)?.[1]);
  }
}

const goModule = await readFile(path.join(repositoryRoot, "apps/native/go.mod"), "utf8");
if (native) assertGoLockHasIntegrity(goModule, await readFile(path.join(repositoryRoot, "apps/native/go.sum"), "utf8"));
for (const { name, version } of parseGoModules(goModule)) add("golang", name, version, "apps/native/go.mod", "native-runtime");

const pnpmLock = await readFile(path.join(repositoryRoot, "pnpm-lock.yaml"), "utf8");
if (native) {
  assertNodeLockHasIntegrity(pnpmLock);
  for (const { name, version } of nativeNodeDependencies(pnpmLock, rootPackage)) add("npm", name, version, "pnpm-lock.yaml", "root-build-tools");
} else {
  for (const match of pnpmLock.matchAll(/^  (?:'([^']+)'|([^:\s]+)):\n    resolution:/gm)) {
    const key = match[1] ?? match[2];
    const separator = key.lastIndexOf("@");
    if (separator > 0) add("npm", key.slice(0, separator), key.slice(separator + 1));
  }
}

const pythonLocks = native ? PYTHON_LOCK_FILES : PYTHON_LOCK_FILES.slice(1);
for (const lock of pythonLocks) {
  const content = await readFile(path.join(repositoryRoot, lock), "utf8");
  if (native) assertPythonLockHasHashes(content, lock);
  for (const match of content.matchAll(/^([A-Za-z0-9_.-]+)==([^\s\\]+)/gm)) {
    add("pypi", match[1], match[2], lock, lock === PYTHON_LOCK_FILES[0] ? "python-dev-tools" : "production-python-runtime");
  }
}
const sourceSBOMPath = "python/funasr-runner/vendor/source.cdx.json";
const upstreamSBOM = JSON.parse(productionSources.get("source.cdx.json").toString("utf8"));
for (const upstream of upstreamSBOM.components) {
  add("pypi", upstream.name, upstream.version, sourceSBOMPath, "production-python-runtime-upstream");
  Object.assign(components.get(`pypi:${upstream.name}@${upstream.version}`), { hashes: upstream.hashes, licenses: upstream.licenses, externalReferences: upstream.externalReferences });
}
const vendorRecipe = JSON.parse(productionSources.get("recipe.json").toString("utf8"));
const localPurl = `pkg:pypi/funasr@${encodeURIComponent(vendorRecipe.output.version)}`;
components.get(`pypi:funasr@${vendorRecipe.output.version}`).hashes = [{ alg: "SHA-256", content: vendorRecipe.output.sha256 }];
for (const component of translationComponents(await loadRuntimeInputManifest(repositoryRoot))) components.set(`pypi:${component.name}@${component.version}`, component);
const bloubSourcePath = "packages/desktop-resources/presentation/bloub/source.json";
const bloub = JSON.parse(await readFile(path.join(repositoryRoot, bloubSourcePath), "utf8"));
components.set(`generic:bloub@${bloub.commit}`, {
  type: "library", name: "bloub", version: bloub.commit,
  purl: `pkg:generic/bloub@${bloub.commit}`,
  "bom-ref": `pkg:generic/bloub@${bloub.commit}`,
  licenses: [{ license: { id: "MIT" } }],
  externalReferences: [{ type: "vcs", url: `${bloub.repository}/tree/${bloub.commit}` }],
  properties: [{ name: "liberty:source-lock", value: bloubSourcePath }, { name: "liberty:dependency-role", value: "native-animation-build-source" }],
});

const document = {
  bomFormat: "CycloneDX",
  specVersion: "1.5",
  serialNumber: `urn:uuid:${crypto.randomUUID()}`,
  version: 1,
  metadata: {
    timestamp: new Date().toISOString(),
    tools: [{ vendor: "Liberty", name: "scripts/generate-sbom.mjs", version: "1" }],
    component: {
      type: "application",
      name: native ? "Liberty Native" : "Liberty",
      version: rootPackage.version,
      purl: `pkg:generic/${native ? "liberty-native" : "liberty"}@${encodeURIComponent(rootPackage.version)}`,
    },
    properties: [
      { name: "liberty:sbom:node-version", value: process.version },
      { name: "liberty:sbom:source-locks", value: native ? ["pnpm-lock.yaml", "apps/native/go.mod", "apps/native/go.sum", ...pythonLocks, sourceSBOMPath].join(",") : `Cargo.lock,pnpm-lock.yaml,requirements-lock-*,apps/native/go.mod,apps/native/go.sum,${sourceSBOMPath}` },
      ...(native ? [{ name: "liberty:sbom:scope", value: "Lock inventory: Native Go modules, production Python dependencies across three runtime targets including the local FunASR wheel and its authenticated upstream identity, optional CTranslate2 wheels, Python development tools, and transitive root pnpm build tools. Excludes other pnpm importers, Rust, candidate runtime inputs, interpreter/toolchain binaries, FFmpeg, native libraries embedded in Python wheels and model assets. Not an installed or packaged inventory." }] : []),
    ],
  },
  components: [...components.values()].sort((left, right) => left.purl.localeCompare(right.purl)),
  dependencies: [{ ref: localPurl, dependsOn: [vendorRecipe.upstream.purl] }],
};
const serialized = `${JSON.stringify(document, null, 2)}\n`;
if (serialized.includes(repositoryRoot) || containsAbsolutePath(document) || /api[_-]?key|bearer\s/i.test(serialized)) {
  throw new Error("SBOM contains a local path or credential-like text");
}
await mkdir(path.dirname(outputPath), { recursive: true });
await writeFile(outputPath, serialized, "utf8");
console.log(`Generated ${path.relative(repositoryRoot, outputPath)} with ${document.components.length} components.`);

function encodePurlName(type, name) {
  if (type === "golang") return name.split("/").map(encodeURIComponent).join("/");
  if (type === "npm" && name.startsWith("@") && name.includes("/")) {
    const [scope, packageName] = name.split("/", 2);
    return `${encodeURIComponent(scope)}/${encodeURIComponent(packageName)}`;
  }
  return encodeURIComponent(name);
}

function containsAbsolutePath(value) {
  if (typeof value === "string") return path.isAbsolute(value) || /^[A-Za-z]:[\\/]/.test(value);
  if (Array.isArray(value)) return value.some(containsAbsolutePath);
  return value && typeof value === "object" && Object.values(value).some(containsAbsolutePath);
}
