<script setup lang="ts">
import { open } from "@tauri-apps/plugin-dialog";
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import { useMeetingStore } from "@/composables/useMeetingStore";
import type { MeetingSourceFile } from "@/types/meeting";

const router = useRouter();
const store = useMeetingStore();

const title = ref("");
const hotwordsText = ref(store.settings.value.defaultHotwords);
const lang = ref("zh-CN");
const enableSpeaker = ref(true);
const summaryTemplate = ref(store.settings.value.summaryTemplate);
const files = ref<MeetingSourceFile[]>([]);
const fileInput = ref<HTMLInputElement | null>(null);
const isSubmitting = ref(false);
const submitError = ref("");
const isLocalMode = computed(() => store.localMode.value);

const recentJobs = computed(() => store.jobs.value.slice(0, 4));
const activeJobs = computed(() =>
  store.jobs.value.filter((job) =>
    ["queued", "transcribing", "speaker_processing", "summarizing"].includes(
      job.overallStatus,
    ),
  ).length,
);
const failedJobs = computed(
  () => store.jobs.value.filter((job) => job.overallStatus === "failed").length,
);

function inferKind(fileName: string): "audio" | "video" {
  return /\.(mp4|mov|mkv)$/i.test(fileName) ? "video" : "audio";
}

function humanSize(size?: number) {
  if (!size) {
    return "未知大小";
  }

  const mb = size / 1024 / 1024;
  return `${mb.toFixed(1)} MB`;
}

function fileToSource(file: File): MeetingSourceFile {
  return {
    id: crypto.randomUUID(),
    name: file.name,
    sizeLabel: humanSize(file.size),
    kind: inferKind(file.name),
  };
}

function addFiles(next: MeetingSourceFile[]) {
  if (isLocalMode.value) {
    const lastFile = next.at(-1);
    files.value = lastFile ? [lastFile] : files.value;
    return;
  }

  files.value = [...files.value, ...next];
}

async function pickFiles() {
  try {
    const selected = await open({
      multiple: !isLocalMode.value,
      directory: false,
      filters: [
        {
          name: "Meeting Media",
          extensions: ["m4a", "mp3", "wav", "aac", "flac", "mp4", "mov", "mkv"],
        },
      ],
    });

    if (!selected) {
      return;
    }

    const normalized = Array.isArray(selected) ? selected : [selected];

    addFiles(
      normalized.map((path) => {
        const name = path.split("/").pop() ?? path;

        return {
          id: crypto.randomUUID(),
          name,
          path,
          sizeLabel: "本地路径",
          kind: inferKind(name),
        };
      }),
    );
  } catch {
    fileInput.value?.click();
  }
}

function onNativeFileChange(event: Event) {
  const target = event.target as HTMLInputElement;
  const selected = Array.from(target.files ?? []).map(fileToSource);
  addFiles(selected);
  target.value = "";
}

function removeFile(id: string) {
  files.value = files.value.filter((file) => file.id !== id);
}

async function submit() {
  submitError.value = "";

  if (!files.value.length || !title.value.trim()) {
    return;
  }

  if (isLocalMode.value && files.value.some((file) => !file.path)) {
    submitError.value = "本地 Python 模式需要通过桌面原生文件选择器获取可读路径。";
    return;
  }

  isSubmitting.value = true;

  try {
    const job = await store.createJob({
      title: title.value.trim(),
      files: files.value,
      hotwords: hotwordsText.value
        .split(",")
        .map((item) => item.trim())
        .filter(Boolean),
      lang: lang.value,
      enableSpeaker: enableSpeaker.value,
      summaryTemplate: summaryTemplate.value.trim() || "默认会议纪要模板",
    });

    await router.push(`/jobs/${job.id}`);
  } catch (error) {
    submitError.value =
      error instanceof Error ? error.message : "创建任务失败，请检查本地运行环境配置。";
  } finally {
    isSubmitting.value = false;
  }
}
</script>

<template>
  <section class="view-stack">
    <div class="workspace-grid workspace-grid-home">
      <article class="surface workspace-primary">
        <div class="section-heading">
          <div>
            <h3>创建会议任务</h3>
            <p class="section-copy">
              在一个连续工作区里完成参数配置和文件输入，不把主流程切碎。
            </p>
          </div>
          <button class="secondary-button" type="button" @click="router.push('/jobs')">
            查看任务列表
          </button>
        </div>

        <div class="summary-inline">
          <span>全部任务 {{ store.jobs.value.length }}</span>
          <span>处理中 {{ activeJobs }}</span>
          <span>失败 {{ failedJobs }}</span>
          <span>
            {{
              store.localMode.value
                ? "本地 FunASR"
                : store.settings.value.backendUrl
                  ? "远端服务"
                  : "Mock 模式"
            }}
          </span>
        </div>

        <div class="field task-title-row">
          <div class="field">
            <label for="job-title">任务标题</label>
            <input
              id="job-title"
              v-model="title"
              placeholder="例如：产品周会 2026-03-25"
            />
          </div>
        </div>

        <div class="field-grid two-col task-form-grid">
          <div class="field">
            <label for="job-lang">语言</label>
            <select id="job-lang" v-model="lang">
              <option value="zh-CN">中文</option>
              <option value="en-US">英文</option>
              <option value="ja-JP">日文</option>
            </select>
          </div>
        </div>

        <div class="field-grid two-col task-form-secondary">
          <div class="field field-panel">
            <label for="job-hotwords">热词</label>
            <div class="inline-panel">
              <textarea
                id="job-hotwords"
                v-model="hotwordsText"
                placeholder="使用英文逗号分隔，例如：SeACo-Paraformer, FunASR, 招投标"
              />
              <p class="inline-hint">
                建议只保留专有名词、项目名和行业术语，减少普通词带来的噪声。
              </p>
            </div>
          </div>

          <div class="field field-panel">
            <label for="job-template">纪要模板</label>
            <div class="inline-panel">
              <input
                id="job-template"
                v-model="summaryTemplate"
                placeholder="默认会议纪要模板"
              />
              <p class="inline-hint">
                这里决定右侧纪要区域的默认结构，第一阶段先保持模板命名即可。
              </p>
            </div>
          </div>
        </div>

        <div class="surface surface-subtle upload-pane">
          <div class="section-heading">
            <div>
              <h3>输入文件</h3>
            </div>
          </div>

          <button class="drop-zone drop-zone-button" type="button" @click="pickFiles">
            <div class="drop-zone-copy">
              <strong>添加文件</strong>
              <p class="muted">支持音频与视频文件</p>
            </div>
          </button>

          <input
            ref="fileInput"
            type="file"
            accept=".m4a,.mp3,.wav,.aac,.flac,.mp4,.mov,.mkv"
            :multiple="!isLocalMode"
            hidden
            @change="onNativeFileChange"
          />

          <div v-if="files.length" class="file-list">
            <div v-for="file in files" :key="file.id" class="file-pill">
              <div>
                <strong>{{ file.name }}</strong>
                <div class="job-meta-line">
                  {{ file.kind === "audio" ? "音频" : "视频" }} · {{ file.sizeLabel }}
                </div>
              </div>
              <button class="text-button danger-text" type="button" @click="removeFile(file.id)">
                移除
              </button>
            </div>
          </div>

        </div>

        <div v-if="submitError" class="note-block error-block">
          {{ submitError }}
        </div>

        <div class="button-row align-end task-actions">
          <button class="secondary-button" type="button" @click="router.push('/settings')">
            配置处理环境
          </button>
          <button
            class="primary-button"
            type="button"
            :disabled="isSubmitting || !title.trim() || !files.length"
            @click="submit"
          >
            {{ isSubmitting ? "正在创建..." : "创建会议任务" }}
          </button>
        </div>
      </article>

      <div class="side-stack">
        <article class="surface side-panel">
          <div class="section-heading">
            <h3>最近任务</h3>
            <button class="text-button" type="button" @click="router.push('/jobs')">
              查看全部
            </button>
          </div>

          <div v-if="recentJobs.length" class="job-list compact-list">
            <div
              v-for="job in recentJobs"
              :key="job.id"
              class="job-item compact-job"
            >
              <div class="job-title-line">
                <div>
                  <h4>{{ job.title }}</h4>
                  <div class="job-meta-line">
                    {{ job.sourceFiles.length }} 个文件 · {{ job.durationMinutes }} 分钟
                  </div>
                </div>
                <button class="text-button" type="button" @click="router.push(`/jobs/${job.id}`)">
                  打开
                </button>
              </div>
            </div>
          </div>

          <div v-else class="empty-state compact-empty">
            还没有任务记录。
          </div>
        </article>

        <article class="surface side-panel">
          <div class="section-heading">
            <h3>当前说明</h3>
          </div>

          <div class="summary-list">
            <div class="file-pill">
              <div>
                <strong>热词建议</strong>
                <div class="job-meta-line">只放项目名、术语、品牌名，避免堆太多普通词。</div>
              </div>
            </div>

            <div class="file-pill">
              <div>
                <strong>推荐流程</strong>
                <div class="job-meta-line">先校正逐字稿，再从结果工作台重新生成会议纪要。</div>
              </div>
            </div>

            <div class="file-pill">
              <div>
                <strong>服务状态</strong>
                <div class="job-meta-line">
                  {{
                    store.localMode.value
                      ? "本地 Python Runner 已启用"
                      : store.settings.value.backendUrl || "未配置服务地址，当前使用本地 Mock 数据"
                  }}
                </div>
              </div>
            </div>
          </div>
        </article>
      </div>
    </div>
  </section>
</template>
