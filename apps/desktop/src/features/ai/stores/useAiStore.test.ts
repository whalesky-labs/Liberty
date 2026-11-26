import { beforeEach, expect, it, vi } from "vitest";
import type { AiModelConfig, AiModelSaveInput } from "@/shared/types/meeting";

const service = vi.hoisted(() => ({ listModels: vi.fn(), saveModel: vi.fn() }));
vi.mock("react", () => ({ useSyncExternalStore: (_subscribe: unknown, snapshot: () => unknown) => snapshot() }));
vi.mock("@/shared/services/tauri/ai", () => ({ createLocalAiService: () => service }));
vi.mock("@/shared/services/ui/statusNotifications", () => ({ runAppStatusAction: (_name: string, action: () => unknown) => action() }));

beforeEach(() => { vi.resetModules(); vi.clearAllMocks(); });

it.each([false, true])("honors explicit default selection regardless of target-first order %s", async (targetFirst) => {
  let models: AiModelConfig[] = ["old-default", "target"].map((id) => ({
    id, name: id, baseUrl: "https://example.test/v1", model: "synthetic", enabled: true,
    isDefault: id === "old-default", credentialPresent: true, createdAt: "2026-11-25T00:00:00Z", updatedAt: "2026-11-25T00:00:00Z",
  }));
  if (targetFirst) models.reverse();
  service.listModels.mockImplementation(async () => models);
  service.saveModel.mockImplementation(async ({ credential: _credential, ...input }: AiModelSaveInput) => {
    models = models.map((model) => model.id === input.id
      ? { ...input, credentialPresent: true }
      : { ...model, isDefault: input.isDefault ? false : model.isDefault });
  });
  const { useAiStore } = await import("./useAiStore");
  await useAiStore().reloadModels();
  const { credentialPresent: _presence, ...target } = models.find((model) => model.id === "target")!;
  await useAiStore().saveModel({ ...target, isDefault: true, credential: { action: "keep" } });
  expect(service.saveModel).toHaveBeenLastCalledWith(expect.objectContaining({ id: "target", isDefault: true }));
  expect(useAiStore().getDefaultModel()?.id).toBe("target");
  await useAiStore().saveModel({ ...target, enabled: false, isDefault: true, credential: { action: "keep" } });
  expect(service.saveModel).toHaveBeenLastCalledWith(expect.objectContaining({ id: "target", isDefault: false }));
});
