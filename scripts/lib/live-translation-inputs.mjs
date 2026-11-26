// Optional local translation assets are pinned independently of the base Python environment.
export function translationComponents(manifest, platformID) {
  const t = manifest.liveTranslation;
  if (!t || t.engineVersion !== "4.9.1" || t.engineLicense !== "MIT" || t.modelLicense !== "CC-BY-4.0" ||
      t.sourceLanguage !== "zh" || t.targetLanguage !== "en" || !Array.isArray(t.engines) || t.engines.length !== 3) {
    throw new Error("Local translation manifest is incomplete");
  }
  const platforms = new Set();
  if (t.reverse?.source !== "en" || t.reverse?.target !== "zh" || t.reverse?.modelLicense !== "CC-BY-4.0" || !/^[A-Za-z0-9_.-]+$/.test(t.reverse?.modelDirectory ?? "")) throw new Error("Reverse local translation manifest is incomplete");
  for (const engine of t.engines) {
    if (!["darwin-aarch64", "darwin-x64", "windows-x64"].includes(engine.platformId) || platforms.has(engine.platformId)) throw new Error("Local translation platform inventory is invalid");
    platforms.add(engine.platformId);
  }
  for (const archive of [t.archive, t.reverse.archive, ...t.engines]) {
    const url = new URL(archive.url);
    if (url.protocol !== "https:" || url.username || url.password || url.hash || !/^[a-f0-9]{64}$/.test(archive.sha256) ||
        !Number.isSafeInteger(archive.size) || archive.size <= 0 || archive.size > 256 * 1024 ** 2 ||
        !Array.isArray(archive.files) || !archive.files.length || archive.files.length > 256) throw new Error("Local translation archive must be hash pinned");
    const paths = new Set();
    for (const file of archive.files) {
      if (!/^[A-Za-z0-9_./+-]+$/.test(file.path) || file.path.startsWith("/") || file.path.split("/").some(p => !p || p === "." || p === "..") ||
          paths.has(file.path.toLowerCase()) || !/^[a-f0-9]{64}$/.test(file.sha256) || !Number.isSafeInteger(file.size) || file.size < 0) throw new Error("Local translation payload must have an exact file inventory");
      paths.add(file.path.toLowerCase());
    }
  }
  const engines = platformID ? t.engines.filter(e => e.platformId === platformID) : t.engines;
  if (!engines.length) throw new Error("Local translation engine unavailable for platform");
  const purl = `pkg:pypi/ctranslate2@${t.engineVersion}`;
  return [{
    type: "library", name: "ctranslate2", version: t.engineVersion, purl, "bom-ref": purl,
    hashes: engines.map(e => ({alg: "SHA-256", content: e.sha256})),
    licenses: [{license: {id: t.engineLicense}}],
    externalReferences: [{type: "vcs", url: `https://github.com/OpenNMT/CTranslate2/tree/v${t.engineVersion}`}],
    properties: [{name: "liberty:dependency-role", value: "optional-local-translation"},
      {name: "liberty:source-lock", value: "packages/desktop-resources/runtime/runtime-manifest.json"},
      {name: "liberty:platforms", value: engines.map(e => e.platformId).join(",")}],
  }];
}
