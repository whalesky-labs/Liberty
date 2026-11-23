const roles = ["model", "vad", "punc", "spk"];

export const runtimeModelPinsPath = "packages/desktop-resources/runtime/models.json";

function isModelID(value) {
  if (typeof value !== "string") return false;
  const parts = value.split("/");
  return parts.length === 2 && parts.every((part) =>
    part.length > 0 && part !== "." && part !== ".." && !/[^a-zA-Z0-9_.-]/.test(part),
  );
}

export function parseRuntimeModelPins(contents) {
  const pins = JSON.parse(contents);
  if (!Array.isArray(pins) || pins.length !== roles.length || pins.some((pin, index) =>
    !pin || pin.role !== roles[index] || Object.keys(pin).length !== 3 ||
    !isModelID(pin.modelId) || typeof pin.revision !== "string" ||
    pin.revision.length !== 40 || /[^a-f0-9]/.test(pin.revision),
  ) || new Set(pins.map(({ revision }) => revision)).size !== roles.length) {
    throw new Error("Expected four ordered FunASR models with distinct immutable 40-character commits");
  }
  return pins.map(({ role, modelId, revision }) => ({ role, modelId, revision }));
}
