import path from "node:path";
import { fileURLToPath } from "node:url";
import { assertPlainDirectory, loadRuntimeInputManifest, parseRuntimeLock, productionRuntimeProfile, publishRuntimeInputs, readPlainFile, runtimeInputPins, securityRuntimeProfile, vendorRuntimeFiles } from "./lib/native-runtime-inputs.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const options = new Map();
for (let index = 2; index < process.argv.length; index += 2) {
  const name = process.argv[index];
  const value = process.argv[index + 1];
  if (options.has(name) || !value || (name === "--profile" ? ![productionRuntimeProfile, securityRuntimeProfile].includes(value) : !["--locks-directory", "--vendor-directory", "--output-directory"].includes(name) || !path.isAbsolute(value))) throw new Error("Expected optional --profile production|security-candidate and explicit absolute locks, vendor and new output directories");
  options.set(name, name === "--profile" ? value : path.resolve(value));
}
if (!["--locks-directory", "--vendor-directory", "--output-directory"].every((name) => options.has(name))) throw new Error("Expected --locks-directory, --vendor-directory and --output-directory");
const profileID = options.get("--profile") ?? securityRuntimeProfile;
const pins = runtimeInputPins(await loadRuntimeInputManifest(root), profileID);
const files = new Map();
await assertPlainDirectory(options.get("--locks-directory"));
await assertPlainDirectory(options.get("--vendor-directory"));
for (const pin of pins) {
  const vendor = vendorRuntimeFiles.includes(pin.fileName);
  const bytes = await readPlainFile(options.get(vendor ? "--vendor-directory" : "--locks-directory"), pin.fileName);
  files.set(pin.fileName, vendor ? bytes : parseRuntimeLock(bytes.toString("utf8")).bytes);
}
await publishRuntimeInputs(options.get("--output-directory"), files, pins);
console.log(`Verified ${profileID} runtime inputs: ${options.get("--output-directory")}`);
