export type JobStage =
  | "uploaded"
  | "queued"
  | "transcribing"
  | "speaker_processing"
  | "summarizing"
  | "completed"
  | "failed";

export interface TranscriptSegment {
  id: string;
  startMs: number;
  endMs: number;
  speaker?: string;
  text: string;
}

export interface MeetingSummary {
  overview: string;
  topics: string[];
  decisions: string[];
  actionItems: string[];
}

export interface MeetingJob {
  id: string;
  title: string;
  sourceFiles: MeetingSourceFile[];
  durationMinutes: number;
  createdAt: string;
  hotwords: string[];
  lang: string;
  enableSpeaker: boolean;
  summaryTemplate: string;
  uploadStatus: JobStage;
  asrStatus: JobStage;
  summaryStatus: JobStage;
  overallStatus: JobStage;
  failureReason?: string;
  transcriptSegments: TranscriptSegment[];
  speakerSegments: TranscriptSegment[];
  summary: MeetingSummary;
  exportFormats: string[];
  lastExportedAt?: string;
  processLog?: string;
}

export interface MeetingSourceFile {
  id: string;
  name: string;
  path?: string;
  sizeLabel: string;
  kind: "audio" | "video";
}

export interface NewMeetingJobInput {
  title: string;
  files: MeetingSourceFile[];
  hotwords: string[];
  lang: string;
  enableSpeaker: boolean;
  summaryTemplate: string;
}

export interface SettingsState {
  backendUrl: string;
  apiToken: string;
  defaultHotwords: string;
  summaryTemplate: string;
  concurrency: number;
  pythonPath: string;
  runnerScriptPath: string;
}
