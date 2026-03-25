<script setup lang="ts">
import { computed } from "vue";
import type { TranscriptSegment } from "@/types/meeting";

const props = defineProps<{
  segments: TranscriptSegment[];
  query: string;
}>();

const filteredSegments = computed(() => {
  const keyword = props.query.trim().toLowerCase();

  if (!keyword) {
    return props.segments;
  }

  return props.segments.filter((segment) => {
    const body = `${segment.speaker ?? ""} ${segment.text}`.toLowerCase();
    return body.includes(keyword);
  });
});

function formatClock(ms: number) {
  const date = new Date(ms);
  return date.toISOString().slice(14, 19);
}
</script>

<template>
  <div class="timeline">
    <div
      v-for="segment in filteredSegments"
      :key="segment.id"
      class="timeline-item"
    >
      <div class="timeline-head">
        <div>
          <span class="speaker-tag">{{ segment.speaker ?? "未知说话人" }}</span>
        </div>
        <span class="job-meta-line">
          {{ formatClock(segment.startMs) }} - {{ formatClock(segment.endMs) }}
        </span>
      </div>
      <div class="timeline-text">{{ segment.text }}</div>
    </div>

    <div
      v-if="!filteredSegments.length"
      class="empty-state"
    >
      没有匹配到逐字稿片段。
    </div>
  </div>
</template>
