import { useSyncExternalStore } from "react";
import { createDraftModelConfig, createDraftTemplate } from "@/shared/services/ai/storage";
import { createLocalAiService } from "@/shared/services/tauri/ai";
import { runAppStatusAction } from "@/shared/services/ui/statusNotifications";
import type { AiModelConfig, AiModelSaveInput, AiSummaryTemplate } from "@/shared/types/meeting";

type AiState = {
  models: AiModelConfig[];
  templates: AiSummaryTemplate[];
};

let state: AiState = {
  models: [],
  templates: [],
};

const aiService = createLocalAiService();
const listeners = new Set<() => void>();
let modelsLoaded = false;
let templatesLoaded = false;

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function getSnapshot() {
  return state;
}

function setState(patch: Partial<AiState>) {
  state = { ...state, ...patch };
  for (const listener of listeners) {
    listener();
  }
}

async function reloadState() {
  await Promise.all([reloadModels(), reloadTemplates()]);
}

async function reloadModels() {
  const models = await aiService.listModels();
  modelsLoaded = true;
  setState({ models });
}

async function reloadTemplates() {
  const templates = await aiService.listTemplates();
  templatesLoaded = true;
  setState({ templates });
}

async function ensureLoaded() {
  await Promise.all([ensureModelsLoaded(), ensureTemplatesLoaded()]);
}

async function ensureModelsLoaded() {
  if (!modelsLoaded) await reloadModels();
}

async function ensureTemplatesLoaded() {
  if (!templatesLoaded) await reloadTemplates();
}

function createModel() {
  return createDraftModelConfig();
}

async function saveModelOperation(input: AiModelSaveInput) {
  const { credential, ...model } = input;
  const nextModel = { ...model, updatedAt: new Date().toISOString() };
  const current = state.models.some((item) => item.id === model.id)
    ? state.models.map((item) => (item.id === model.id ? nextModel : item))
    : [nextModel, ...state.models];
  const enabled = current.filter((item) => item.enabled);
  const defaultModel = nextModel.enabled && nextModel.isDefault
    ? nextModel
    : enabled.find((item) => item.isDefault) ?? enabled[0];

  await aiService.saveModel({ ...nextModel, isDefault: defaultModel?.id === nextModel.id, credential });
  await reloadModels();
}

async function deleteModelOperation(id: string) {
  await aiService.deleteModel(id);
  await reloadModels();
}

function createTemplate() {
  return createDraftTemplate();
}

function duplicateTemplate(templateId: string) {
  const source = state.templates.find((item) => item.id === templateId);

  if (!source) {
    return null;
  }

  const time = new Date().toISOString();
  return {
    ...source,
    id: crypto.randomUUID(),
    name: `${source.name} - 副本`,
    builtin: false,
    createdAt: time,
    updatedAt: time,
  } satisfies AiSummaryTemplate;
}

async function saveTemplateOperation(template: AiSummaryTemplate) {
  const nextTemplate = {
    ...template,
    builtin: false,
    updatedAt: new Date().toISOString(),
  };

  await aiService.saveTemplate(nextTemplate);
  await reloadTemplates();
}

async function deleteTemplateOperation(id: string) {
  await aiService.deleteTemplate(id);
  await reloadTemplates();
}

async function insertTemplate(template: AiSummaryTemplate) {
  await aiService.saveTemplate(template);
  await reloadTemplates();
}

function saveModel(model: AiModelSaveInput) {
  return runAppStatusAction("saveModel", () => saveModelOperation(model));
}

function deleteModel(id: string) {
  return runAppStatusAction("deleteModel", () => deleteModelOperation(id));
}

function saveTemplate(template: AiSummaryTemplate) {
  return runAppStatusAction("saveTemplate", () => saveTemplateOperation(template));
}

function deleteTemplate(id: string) {
  return runAppStatusAction("deleteTemplate", () => deleteTemplateOperation(id));
}

function getDefaultModel() {
  return state.models.find((model) => model.isDefault && model.enabled)
    ?? state.models.find((model) => model.enabled);
}

function getTemplateById(id: string) {
  return state.templates.find((template) => template.id === id);
}

function getModelById(id: string) {
  return state.models.find((model) => model.id === id);
}

const actions = {
  ensureLoaded,
  ensureModelsLoaded,
  ensureTemplatesLoaded,
  createModel,
  saveModel,
  deleteModel,
  createTemplate,
  insertTemplate,
  duplicateTemplate,
  saveTemplate,
  deleteTemplate,
  getDefaultModel,
  getTemplateById,
  getModelById,
  reloadModels,
  reloadTemplates,
  reloadState,
};

export function useAiStore() {
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  return {
    ...snapshot,
    ...actions,
  };
}

export type AiStore = ReturnType<typeof useAiStore>;
