<script setup lang="ts">
import { reactive } from "vue";
import { useMeetingStore } from "@/composables/useMeetingStore";

const store = useMeetingStore();

const form = reactive({
  backendUrl: store.settings.value.backendUrl,
  apiToken: store.settings.value.apiToken,
  defaultHotwords: store.settings.value.defaultHotwords,
  summaryTemplate: store.settings.value.summaryTemplate,
  concurrency: store.settings.value.concurrency,
  pythonPath: store.settings.value.pythonPath,
  runnerScriptPath: store.settings.value.runnerScriptPath,
});

function save() {
  store.saveSettings({
    backendUrl: form.backendUrl.trim(),
    apiToken: form.apiToken.trim(),
    defaultHotwords: form.defaultHotwords.trim(),
    summaryTemplate: form.summaryTemplate.trim(),
    concurrency: Number(form.concurrency) || 1,
    pythonPath: form.pythonPath.trim(),
    runnerScriptPath: form.runnerScriptPath.trim(),
  });
}
</script>

<template>
  <section class="view-stack">
    <article class="surface">
      <h3>本地 FunASR 开发配置</h3>
      <p class="section-copy">
        当 Python 路径和 Runner 脚本路径都已配置时，桌面端会优先走本地 Tauri + Python 处理链路。未配置完整时，仍可继续使用远端接口或本地 Mock。
      </p>
    </article>

    <article class="surface">
      <div class="section-heading">
        <h3>运行环境与默认参数</h3>
      </div>

      <div class="field-grid">
        <div class="field-grid two-col">
          <div class="field">
            <label for="python-path">Python 可执行文件路径</label>
            <input
              id="python-path"
              v-model="form.pythonPath"
              placeholder="例如：/opt/homebrew/bin/python3 或 C:\\Python311\\python.exe"
            />
          </div>

          <div class="field">
            <label for="runner-script-path">Runner 脚本路径</label>
            <input
              id="runner-script-path"
              v-model="form.runnerScriptPath"
              placeholder="例如：/Users/west/funasr/runner.py"
            />
          </div>
        </div>

        <div class="field">
          <label for="backend-url">服务地址</label>
          <input
            id="backend-url"
            v-model="form.backendUrl"
            placeholder="例如：http://127.0.0.1:8000"
          />
        </div>

        <div class="field">
          <label for="api-token">API Token</label>
          <input
            id="api-token"
            v-model="form.apiToken"
            placeholder="可选"
          />
        </div>

        <div class="field">
          <label for="default-hotwords">默认热词</label>
          <textarea
            id="default-hotwords"
            v-model="form.defaultHotwords"
          />
        </div>

        <div class="field-grid two-col">
          <div class="field">
            <label for="summary-template">默认纪要模板</label>
            <input
              id="summary-template"
              v-model="form.summaryTemplate"
            />
          </div>

          <div class="field">
            <label for="concurrency">并发上传数</label>
            <input
              id="concurrency"
              v-model.number="form.concurrency"
              type="number"
              min="1"
              max="8"
            />
          </div>
        </div>
      </div>

      <div class="button-row">
        <button class="primary-button" type="button" @click="save">
          保存设置
        </button>
      </div>
    </article>
  </section>
</template>
