import { computed, reactive, toRefs } from "vue";
import { createMeetingApi } from "@/services/api";
import { createLocalMeetingService } from "@/services/localMeeting";
import { createMockJob, seedJobs } from "@/services/mockData";
import type {
  MeetingJob,
  MeetingSummary,
  NewMeetingJobInput,
  SettingsState,
} from "@/types/meeting";

const STORAGE_KEY = "liberty.settings";

const defaultSettings: SettingsState = {
  backendUrl: "",
  apiToken: "",
  defaultHotwords: "SeACo-Paraformer, FunASR, 会议纪要",
  summaryTemplate: "默认会议纪要模板",
  concurrency: 2,
  pythonPath: "",
  runnerScriptPath: "",
};

const state = reactive({
  jobs: seedJobs(),
  settings: loadSettings(),
});
let localPollingId: number | null = null;

function loadSettings(): SettingsState {
  if (typeof localStorage === "undefined") {
    return { ...defaultSettings };
  }

  const raw = localStorage.getItem(STORAGE_KEY);

  if (!raw) {
    return { ...defaultSettings };
  }

  try {
    return {
      ...defaultSettings,
      ...(JSON.parse(raw) as Partial<SettingsState>),
    };
  } catch {
    return { ...defaultSettings };
  }
}

function persistSettings() {
  if (typeof localStorage === "undefined") {
    return;
  }

  localStorage.setItem(STORAGE_KEY, JSON.stringify(state.settings));
}

function hasLocalRunnerSettings(settings: SettingsState) {
  return Boolean(settings.pythonPath.trim() && settings.runnerScriptPath.trim());
}

function getEmptySummary(): MeetingSummary {
  return {
    overview: "",
    topics: [],
    decisions: [],
    actionItems: [],
  };
}

async function refreshLocalJobs() {
  try {
    state.jobs = await createLocalMeetingService().listJobs();
  } catch {
    // Keep the last known local state when polling fails.
  }
}

function syncLocalPolling() {
  if (typeof window === "undefined") {
    return;
  }

  const shouldPoll = hasLocalRunnerSettings(state.settings);

  if (shouldPoll && localPollingId === null) {
    localPollingId = window.setInterval(() => {
      void refreshLocalJobs();
    }, 1500);
    return;
  }

  if (!shouldPoll && localPollingId !== null) {
    window.clearInterval(localPollingId);
    localPollingId = null;
  }
}

function simulatePipeline(jobId: string) {
  const stages: Array<MeetingJob["overallStatus"]> = [
    "queued",
    "transcribing",
    "speaker_processing",
    "summarizing",
    "completed",
  ];

  stages.forEach((stage, index) => {
    window.setTimeout(() => {
      const job = state.jobs.find((item) => item.id === jobId);

      if (!job || job.overallStatus === "failed") {
        return;
      }

      job.overallStatus = stage;
      job.uploadStatus = stage === "queued" ? "uploaded" : job.uploadStatus;
      job.asrStatus =
        stage === "queued"
          ? "queued"
          : ["transcribing", "speaker_processing", "summarizing", "completed"].includes(stage)
            ? stage === "summarizing" || stage === "completed"
              ? "completed"
              : stage
            : job.asrStatus;
      job.summaryStatus =
        stage === "summarizing"
          ? "summarizing"
          : stage === "completed"
            ? "completed"
            : "queued";
    }, 900 * (index + 1));
  });
}

function updateSummary(jobId: string, summary: MeetingSummary) {
  const job = state.jobs.find((item) => item.id === jobId);

  if (!job) {
    return;
  }

  job.summary = summary;
}

export function useMeetingStore() {
  syncLocalPolling();

  const api = computed(() =>
    state.settings.backendUrl
      ? createMeetingApi(state.settings.backendUrl, state.settings.apiToken)
      : null,
  );
  const localMode = computed(() => hasLocalRunnerSettings(state.settings));

  async function refreshJobs() {
    if (localMode.value) {
      state.jobs = await createLocalMeetingService().listJobs();
      return state.jobs;
    }

    if (!api.value) {
      return state.jobs;
    }

    try {
      state.jobs = await api.value.listJobs();
      return state.jobs;
    } catch {
      return state.jobs;
    }
  }

  async function createJob(input: NewMeetingJobInput) {
    if (localMode.value) {
      const firstFile = input.files[0];

      if (!firstFile?.path) {
        throw new Error("本地模式只支持带本地路径的单个文件。");
      }

      const created = await createLocalMeetingService().createJob({
        ...input,
        files: [firstFile],
      }, state.settings);

      state.jobs = [created, ...state.jobs.filter((job) => job.id !== created.id)];
      syncLocalPolling();
      return created;
    }

    if (api.value) {
      try {
        const created = await api.value.createJob(input);
        state.jobs.unshift(created);
        return created;
      } catch {
        // Fall through to mock mode when the backend is unavailable.
      }
    }

    const mockJob = createMockJob({
      title: input.title,
      sourceFiles: input.files,
      hotwords: input.hotwords,
      lang: input.lang,
      enableSpeaker: input.enableSpeaker,
      summaryTemplate: input.summaryTemplate,
      overallStatus: "uploaded",
      uploadStatus: "uploaded",
      asrStatus: "queued",
      summaryStatus: "queued",
      summary: getEmptySummary(),
    });

    state.jobs.unshift(mockJob);
    simulatePipeline(mockJob.id);
    return mockJob;
  }

  async function retryJob(id: string) {
    const job = state.jobs.find((item) => item.id === id);

    if (!job) {
      return;
    }

    if (localMode.value) {
      const updated = await createLocalMeetingService().retryJob(id, state.settings);
      Object.assign(job, updated);
      syncLocalPolling();
      return;
    }

    job.failureReason = undefined;
    job.overallStatus = "queued";
    job.asrStatus = "queued";
    job.summaryStatus = "queued";

    if (api.value) {
      try {
        const updated = await api.value.retryJob(id);
        Object.assign(job, updated);
        return;
      } catch {
        // Fallback to mock progression.
      }
    }

    simulatePipeline(id);
  }

  async function deleteJob(id: string) {
    if (localMode.value) {
      await createLocalMeetingService().deleteJob(id);
      state.jobs = state.jobs.filter((job) => job.id !== id);
      return;
    }

    state.jobs = state.jobs.filter((job) => job.id !== id);
  }

  async function regenerateSummary(id: string) {
    const job = state.jobs.find((item) => item.id === id);

    if (!job) {
      return;
    }

    job.summaryStatus = "summarizing";
    job.overallStatus = "summarizing";

    if (api.value) {
      try {
        const updated = await api.value.regenerateSummary(id);
        Object.assign(job, updated);
        return;
      } catch {
        // Fallback to local mock behavior.
      }
    }

    window.setTimeout(() => {
      job.summaryStatus = "completed";
      job.overallStatus = "completed";
      updateSummary(id, {
        overview: `${job.title} 的纪要已按最新逐字稿重新生成。`,
        topics: [
          "逐字稿修订后的主题摘要",
          "重新聚合的议题列表",
          "等待确认的后续事项",
        ],
        decisions: ["保留人工编辑结果，只重建系统生成内容。"],
        actionItems: ["检查新的纪要条目是否需要人工微调。"],
      });
    }, 1200);
  }

  function saveSettings(next: SettingsState) {
    state.settings = { ...next };
    persistSettings();
    syncLocalPolling();

    if (hasLocalRunnerSettings(next)) {
      void refreshLocalJobs();
    }
  }

  function getJobById(id: string) {
    return state.jobs.find((job) => job.id === id);
  }

  return {
    ...toRefs(state),
    api,
    localMode,
    refreshJobs,
    createJob,
    deleteJob,
    retryJob,
    regenerateSummary,
    saveSettings,
    getJobById,
  };
}
