import { invoke } from "@tauri-apps/api/core";
import type { MeetingJob, NewMeetingJobInput, SettingsState } from "@/types/meeting";

interface LocalCreateJobInput extends NewMeetingJobInput {
  createdAt: string;
  pythonPath: string;
  runnerScriptPath: string;
}

interface LocalRuntimeConfig {
  pythonPath: SettingsState["pythonPath"];
  runnerScriptPath: SettingsState["runnerScriptPath"];
}

export function createLocalMeetingService() {
  return {
    createJob: (payload: NewMeetingJobInput, runtime: LocalRuntimeConfig) =>
      invoke<MeetingJob>("create_job", {
        input: {
          ...payload,
          createdAt: new Date().toISOString(),
          pythonPath: runtime.pythonPath,
          runnerScriptPath: runtime.runnerScriptPath,
        } satisfies LocalCreateJobInput,
      }),
    listJobs: () => invoke<MeetingJob[]>("list_jobs"),
    deleteJob: (id: string) => invoke<void>("delete_job", { id }),
    getJob: (id: string) => invoke<MeetingJob>("get_job", { id }),
    getJobResult: (id: string) => invoke<MeetingJob>("get_job_result", { id }),
    retryJob: (id: string, runtime: LocalRuntimeConfig) =>
      invoke<MeetingJob>("retry_job", {
        id,
        pythonPath: runtime.pythonPath,
        runnerScriptPath: runtime.runnerScriptPath,
      }),
  };
}
