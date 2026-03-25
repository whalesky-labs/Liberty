<script setup lang="ts">
import type { MeetingSummary } from "@/types/meeting";

defineProps<{
  summary: MeetingSummary;
}>();
</script>

<template>
  <div class="notes-list">
    <article class="note-block">
      <div class="notes-head">
        <h4>摘要</h4>
      </div>
      <div>{{ summary.overview }}</div>
    </article>

    <article class="note-block">
      <div class="notes-head">
        <h4>议题</h4>
      </div>
      <ul>
        <li v-for="item in summary.topics" :key="item">{{ item }}</li>
      </ul>
    </article>

    <article class="note-block">
      <div class="notes-head">
        <h4>结论</h4>
      </div>
      <ul>
        <li v-for="item in summary.decisions" :key="item">{{ item }}</li>
      </ul>
    </article>

    <article class="note-block">
      <div class="notes-head">
        <h4>行动项</h4>
      </div>
      <ul>
        <li v-for="item in summary.actionItems" :key="item">{{ item }}</li>
      </ul>
    </article>
  </div>
</template>
