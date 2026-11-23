import { execFileSync } from "node:child_process";
import { mkdir, readdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { assertGoLockHasIntegrity, parseGoModules, readPolicy } from "./lib/dependency-governance.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const cwd = path.join(root, "apps/native");
const env = { ...process.env, GOTOOLCHAIN: "go1.27.4", GOWORK: "off", GOFLAGS: "-mod=readonly" };
const go = (...args) => execFileSync("go", args, { cwd, env, encoding: "utf8" }).trim();
const declaration = await readFile(path.join(cwd, "go.mod"), "utf8");
const sums = await readFile(path.join(cwd, "go.sum"), "utf8");
const policy = await readPolicy(path.join(root, "config/dependency-governance.json"));
const reviewed = JSON.parse(await readFile(path.join(root, "config/native-module-licenses.json"), "utf8"));
assertGoLockHasIntegrity(declaration, sums);
const notices = [];

async function collect(directory, prefix = "") {
  const files = [];
  for (const entry of await readdir(path.join(directory, prefix), { withFileTypes: true })) {
    const name = path.posix.join(prefix, entry.name);
    if (entry.isDirectory()) files.push(...await collect(directory, name));
    else if (entry.isFile() && /^(?:licen[cs]e|copying|notice|copyright|patents|ofl)(?:[-._]|$)/i.test(entry.name)) files.push(name);
  }
  return files;
}

async function append(module, directory, files) {
  for (const file of files.sort()) {
    const text = await readFile(path.join(directory, file), "utf8");
    if (!text.trim() || text.includes("\0")) throw new Error(`Invalid notice: ${module}/${file}`);
    notices.push(`${module} — ${file}\n\n${text}`);
  }
}

const modules = parseGoModules(declaration);
for (const { name, version } of modules) {
  const id = `${name}@${version}`;
  const license = reviewed[id];
  if (!license || policy.deniedLicenses.includes(license)) throw new Error(`Native license review required: ${id}`);
  const metadata = JSON.parse(go("mod", "download", "-json", id));
  if (metadata.Path !== name || metadata.Version !== version || !sums.split("\n").includes(`${id.replace("@", " ")} ${metadata.Sum}`)) throw new Error(`Unexpected module checksum: ${id}`);
  const files = await collect(metadata.Dir);
  if (!files.includes("LICENSE")) throw new Error(`Missing module license: ${id}`);
  await append(`${id} (${license})`, metadata.Dir, files);
}
if (Object.keys(reviewed).length !== modules.length) throw new Error("Stale native license inventory");
go("mod", "verify");
const goroot = go("env", "GOROOT");
await append(go("env", "GOVERSION"), goroot, ["LICENSE", "PATENTS"]);
const output = path.join(root, "artifacts/sbom/native-go-NOTICES.txt");
await mkdir(path.dirname(output), { recursive: true });
const temporary = `${output}.${process.pid}.tmp`;
await writeFile(temporary, notices.join("\n\n"), { flag: "wx" });
await rename(temporary, output);
console.log(`Collected ${notices.length} notices for the Go toolchain and ${modules.length} locked native modules.`);
