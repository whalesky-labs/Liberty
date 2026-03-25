import type {
  MeetingJob,
  MeetingSourceFile,
  MeetingSummary,
  TranscriptSegment,
} from "@/types/meeting";

function makeSegment(
  id: string,
  startMs: number,
  endMs: number,
  speaker: string,
  text: string,
): TranscriptSegment {
  return { id, startMs, endMs, speaker, text };
}

function buildSummary(title: string): MeetingSummary {
  return {
    overview: `${title} 的核心结论已经形成，纪要建议重点跟进预算确认、交付节奏与风险沟通。`,
    topics: [
      "确认项目范围与第一阶段交付边界",
      "梳理接口依赖与技术风险",
      "约定评审时间与下次同步节点",
    ],
    decisions: [
      "先完成上传式会议处理工作流，不在第一阶段接入实时录音。",
      "以逐字稿为事实源，结构化纪要允许单独编辑并可重新生成。",
    ],
    actionItems: [
      "产品负责人在本周内确认导出模板字段。",
      "研发在接口冻结前给出 Python 服务契约草案。",
      "项目经理安排一次使用场景复盘，验证纪要字段是否足够。",
    ],
  };
}

export function createMockSourceFile(name: string, kind: "audio" | "video"): MeetingSourceFile {
  return {
    id: crypto.randomUUID(),
    name,
    kind,
    sizeLabel: kind === "audio" ? "148 MB" : "402 MB",
    path: undefined,
  };
}

export function createMockJob(seed?: Partial<MeetingJob>): MeetingJob {
  const title = seed?.title ?? "产品周会 03/25";
  const transcriptSegments =
    seed?.transcriptSegments ??
    [
      makeSegment(
        crypto.randomUUID(),
        0,
        36_000,
        "主持人",
        "今天主要同步桌面端会议处理项目的第一阶段范围，确认先以文件上传和异步处理为主。",
      ),
      makeSegment(
        crypto.randomUUID(),
        36_000,
        81_000,
        "研发",
        "服务端会先接入 FunASR 和 SeACo-Paraformer 做转写，逐字稿出来后再进入纪要和行动项生成。",
      ),
      makeSegment(
        crypto.randomUUID(),
        81_000,
        118_000,
        "产品",
        "工作台需要并列显示逐字稿和会议纪要，用户编辑逐字稿后可以选择重新生成纪要，但不能覆盖人工修改。",
      ),
    ];

  return {
    id: seed?.id ?? crypto.randomUUID(),
    title,
    sourceFiles:
      seed?.sourceFiles ?? [createMockSourceFile("team-sync-20260325.m4a", "audio")],
    durationMinutes: seed?.durationMinutes ?? 42,
    createdAt: seed?.createdAt ?? new Date().toISOString(),
    hotwords: seed?.hotwords ?? ["SeACo-Paraformer", "FunASR", "会议纪要"],
    lang: seed?.lang ?? "zh-CN",
    enableSpeaker: seed?.enableSpeaker ?? true,
    summaryTemplate: seed?.summaryTemplate ?? "默认会议纪要模板",
    uploadStatus: seed?.uploadStatus ?? "uploaded",
    asrStatus: seed?.asrStatus ?? "completed",
    summaryStatus: seed?.summaryStatus ?? "completed",
    overallStatus: seed?.overallStatus ?? "completed",
    failureReason: seed?.failureReason,
    transcriptSegments,
    speakerSegments: seed?.speakerSegments ?? transcriptSegments,
    summary: seed?.summary ?? buildSummary(title),
    exportFormats: seed?.exportFormats ?? ["txt", "md", "srt", "docx"],
    lastExportedAt: seed?.lastExportedAt,
  };
}

export function seedJobs(): MeetingJob[] {
  return [
    createMockJob(),
    createMockJob({
      title: "客户需求澄清会",
      sourceFiles: [createMockSourceFile("client-discovery.mp4", "video")],
      durationMinutes: 58,
      overallStatus: "summarizing",
      asrStatus: "completed",
      summaryStatus: "summarizing",
      summary: {
        overview: "纪要正在生成中。",
        topics: [],
        decisions: [],
        actionItems: [],
      },
    }),
    createMockJob({
      title: "供应商评审会",
      sourceFiles: [createMockSourceFile("vendor-review.wav", "audio")],
      durationMinutes: 33,
      overallStatus: "failed",
      asrStatus: "failed",
      summaryStatus: "queued",
      failureReason: "远端服务返回模型推理超时，请稍后重试。",
      summary: {
        overview: "任务失败，暂无可用纪要。",
        topics: [],
        decisions: [],
        actionItems: [],
      },
      transcriptSegments: [],
      speakerSegments: [],
    }),
  ];
}
