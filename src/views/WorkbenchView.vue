<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute } from "vue-router";
import MeetingNotesPanel from "@/components/MeetingNotesPanel.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import TranscriptTimeline from "@/components/TranscriptTimeline.vue";
import { useMeetingStore } from "@/composables/useMeetingStore";
import { exportJob } from "@/services/export";
import { getPrimaryTranscriptSegments } from "@/services/transcript";

const route = useRoute();
const store = useMeetingStore();

const job = computed(() => store.getJobById(route.params.id as string));
const query = ref("");
const isExporting = ref(false);
const transcriptSegments = computed(() =>
  job.value ? getPrimaryTranscriptSegments(job.value) : [],
);

async function doExport(kind: "transcript" | "notes" | "bundle") {
  if (!job.value) {
    return;
  }

  isExporting.value = true;

  try {
    const done = await exportJob(job.value, kind);

    if (done) {
      job.value.lastExportedAt = new Date().toISOString();
    }
  } finally {
    isExporting.value = false;
  }
}
</script>

<template>
  <section class="view-stack">
    <div v-if="job" class="workbench-grid">
      <article class="surface full-span">
        <div class="job-title-line">
          <div>
            <h3>{{ job.title }}</h3>
          </div>
          <StatusBadge :status="job.overallStatus" />
        </div>
        <p class="section-copy">
          左侧逐字稿保留时间戳和说话人信息，中间聚焦纪要内容，右侧固定操作与任务上下文。长时间使用优先保证清晰和不疲劳。
        </p>
      </article>

      <article class="surface transcript-column">
        <div class="section-heading">
          <h3>逐字稿</h3>
        </div>
        <div class="field">
          <label for="transcript-search">搜索逐字段落</label>
          <input
            id="transcript-search"
            v-model="query"
            placeholder="输入关键词过滤说话人或正文"
          />
        </div>
        <TranscriptTimeline :segments="transcriptSegments" :query="query" />
      </article>

      <article class="surface notes-column">
        <div class="section-heading">
          <h3>会议纪要</h3>
          <StatusBadge :status="job.summaryStatus" />
        </div>
        <MeetingNotesPanel :summary="job.summary" />
      </article>

      <article class="surface sidebar-column">
        <div class="section-heading">
          <h3>任务上下文</h3>
        </div>

        <div class="summary-list">
          <div class="file-pill">
            <div>
              <strong>文件</strong>
              <div class="job-meta-line">
                {{ job.sourceFiles.length }} 个 · {{ job.durationMinutes }} 分钟
              </div>
            </div>
          </div>

          <div class="file-pill">
            <div>
              <strong>热词</strong>
              <div class="job-meta-line">
                {{ job.hotwords.join("、") || "未配置" }}
              </div>
            </div>
          </div>

          <div class="file-pill">
            <div>
              <strong>模板</strong>
              <div class="job-meta-line">
                {{ job.summaryTemplate }}
              </div>
            </div>
          </div>
        </div>

        <div class="sidebar-actions">
          <button class="primary-button" type="button" @click="doExport('bundle')">
            {{ isExporting ? "导出中..." : "导出完整结果" }}
          </button>
          <button class="secondary-button" type="button" @click="doExport('transcript')">
            导出逐字稿
          </button>
          <button class="secondary-button" type="button" @click="doExport('notes')">
            导出纪要
          </button>
          <button class="text-button" type="button" @click="store.regenerateSummary(job.id)">
            重新生成纪要
          </button>
        </div>
      </article>
    </div>

    <div v-else class="empty-state">
      没有找到这个任务结果。
    </div>
  </section>
</template>
