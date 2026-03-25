<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute, useRouter } from "vue-router";
import { useMeetingStore } from "@/composables/useMeetingStore";

const route = useRoute();
const router = useRouter();
const store = useMeetingStore();
const routeHistory = ref<string[]>([]);
const routeHistoryIndex = ref(-1);

const navItems = [
  { label: "新建任务", to: "/" },
  { label: "任务列表", to: "/jobs" },
  { label: "系统设置", to: "/settings" },
];

const activeJobCount = computed(() =>
  store.jobs.value.filter((job) =>
    ["queued", "transcribing", "speaker_processing", "summarizing"].includes(
      job.overallStatus,
    ),
  ).length,
);

const canGoBack = computed(() => routeHistoryIndex.value > 0);
const canGoForward = computed(
  () => routeHistoryIndex.value >= 0 && routeHistoryIndex.value < routeHistory.value.length - 1,
);

watch(
  () => route.fullPath,
  (path) => {
    syncRouteHistory(path);
  },
  { immediate: true },
);

function isActive(itemTo: string) {
  if (itemTo === "/jobs") {
    return route.path.startsWith("/jobs");
  }

  return route.path === itemTo;
}

function syncRouteHistory(path: string) {
  if (!path) {
    return;
  }

  if (routeHistoryIndex.value === -1) {
    routeHistory.value = [path];
    routeHistoryIndex.value = 0;
    return;
  }

  if (routeHistory.value[routeHistoryIndex.value] === path) {
    return;
  }

  const previousPath = routeHistory.value[routeHistoryIndex.value - 1];
  if (previousPath === path) {
    routeHistoryIndex.value -= 1;
    return;
  }

  const nextPath = routeHistory.value[routeHistoryIndex.value + 1];
  if (nextPath === path) {
    routeHistoryIndex.value += 1;
    return;
  }

  routeHistory.value = routeHistory.value.slice(0, routeHistoryIndex.value + 1);
  routeHistory.value.push(path);
  routeHistoryIndex.value = routeHistory.value.length - 1;
}

async function goBack() {
  if (!canGoBack.value) {
    return;
  }

  const target = routeHistory.value[routeHistoryIndex.value - 1];
  if (!target) {
    return;
  }

  await router.push(target);
}

async function goForward() {
  if (!canGoForward.value) {
    return;
  }

  const target = routeHistory.value[routeHistoryIndex.value + 1];
  if (!target) {
    return;
  }

  await router.push(target);
}
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="nav-wrap">
        <header class="nav-header">
          <h1>Liberty</h1>
          <p>会议音频桌面端</p>
        </header>

        <nav class="nav-list">
          <RouterLink
            v-for="item in navItems"
            :key="item.to"
            :to="item.to"
            class="nav-link"
            :class="{ active: isActive(item.to) }"
          >
            {{ item.label }}
          </RouterLink>
        </nav>

        <div class="nav-footer">
          <div class="nav-footer-item">
            <span>当前模式</span>
            <strong>
              {{
                store.localMode.value
                  ? "本地 FunASR"
                  : store.settings.value.backendUrl
                    ? "远端服务"
                    : "Mock"
              }}
            </strong>
          </div>
          <div class="nav-footer-item">
            <span>处理中</span>
            <strong>{{ activeJobCount }}</strong>
          </div>
        </div>
      </div>
    </aside>

    <main class="content">
      <section class="content-page">
        <header class="content-toolbar">
          <div class="toolbar-nav">
            <button
              class="toolbar-nav-btn"
              type="button"
              :disabled="!canGoBack"
              aria-label="返回上一页"
              @click="goBack"
            >
              ‹
            </button>
            <button
              class="toolbar-nav-btn"
              type="button"
              :disabled="!canGoForward"
              aria-label="前进到下一页"
              @click="goForward"
            >
              ›
            </button>
          </div>

          <div class="toolbar-copy">
            <h2 class="toolbar-title">{{ route.meta.title ?? "Liberty" }}</h2>
          </div>

          <div class="toolbar-actions">
            <div class="toolbar-pill">
              <span class="toolbar-pill-dot"></span>
              {{
                store.localMode.value
                  ? "本地 Python 处理已启用"
                  : store.settings.value.backendUrl
                    ? "远端服务已配置"
                    : "当前为本地 Mock 模式"
              }}
            </div>
          </div>
        </header>

        <section class="content-body">
          <RouterView />
        </section>
      </section>
    </main>
  </div>
</template>
