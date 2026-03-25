import { createRouter, createWebHistory } from "vue-router";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      name: "new-job",
      component: () => import("@/views/NewJobView.vue"),
      meta: { title: "新建会议任务" },
    },
    {
      path: "/jobs",
      name: "jobs",
      component: () => import("@/views/JobsView.vue"),
      meta: { title: "任务列表" },
    },
    {
      path: "/jobs/:id",
      name: "job-detail",
      component: () => import("@/views/JobDetailView.vue"),
      meta: { title: "任务详情" },
    },
    {
      path: "/jobs/:id/workbench",
      name: "workbench",
      component: () => import("@/views/WorkbenchView.vue"),
      meta: { title: "结果工作台" },
    },
    {
      path: "/settings",
      name: "settings",
      component: () => import("@/views/SettingsView.vue"),
      meta: { title: "系统设置" },
    },
  ],
});

export default router;
