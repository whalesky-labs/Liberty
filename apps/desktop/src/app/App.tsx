import { Suspense, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { getVersion } from "@tauri-apps/api/app";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { shellIconPaths } from "@liberty-resources/presentation/icons";
import sidebarMascotUrl from "@liberty-resources/presentation/sidebar-mascot.webp";
import { Link, RouterProvider, useRouter } from "@/app/router/RouterContext";
import { useMeetingStore } from "@/features/meeting/stores/useMeetingStore";
import {
  getActiveSettingsSection,
  getSettingsNavigationGroups,
  setActiveSettingsSection,
  subscribeActiveSettingsSection,
} from "@/features/settings/application/settingsNavigation";
import { useAiStore } from "@/features/ai/stores/useAiStore";
import { usePetStore } from "@/features/pet/stores/usePetStore";
import { formatMessage, getMessages } from "@/shared/i18n";
import { applyDesktopPetState } from "@/shared/services/tauri/pet";
import { getProcessMetrics, openExternalUrl } from "@/shared/services/tauri/system";
import { applyAppearance, watchSystemThemeChange } from "@/shared/services/ui/appearance";
import { navIconSvg, type NavIconKey } from "@/shared/services/ui/navIcons";
import {
  fulfillJobWindowOpenRequest,
  listenForEntityChanges,
  listenForJobWindowOpenRequests,
} from "@/shared/services/ui/windows";
import {
  getAppStatusNotification,
  clearAppStatusNotification,
  listenForForwardedAppStatus,
  publishAppStatus,
  runAppStatusAction,
  subscribeAppStatus,
} from "@/shared/services/ui/statusNotifications";
import type { PetSettings, ProcessMetrics } from "@/shared/types/meeting";

type NavItem = {
  label: string;
  to: string;
  icon: NavIconKey;
};

function AppContent() {
  const router = useRouter();
  const store = useMeetingStore();
  const aiStore = useAiStore();
  const petStore = usePetStore();
  const [processMetrics, setProcessMetrics] = useState<ProcessMetrics>({
    cpuPercent: 0,
    memoryMb: 0,
  });
  const [graphicsMemoryMb, setGraphicsMemoryMb] = useState(0);
  const [appVersion, setAppVersion] = useState("");
  const [isWindowsTitlebar, setIsWindowsTitlebar] = useState(false);
  const [settingsSearchQuery, setSettingsSearchQuery] = useState("");
  const statusNotification = useSyncExternalStore(
    subscribeAppStatus,
    getAppStatusNotification,
    getAppStatusNotification,
  );
  const knownJobStatuses = useRef(new Map<string, string>());
  const lastAppPath = useRef("/");
  const didHydrateJobStatuses = useRef(false);
  const metricsPollingId = useRef<number | null>(null);
  const didRecordPetDailyOpen = useRef(false);
  const lastAppliedDesktopPetEnabled = useRef<boolean | null>(null);
  const latestSettings = useRef(store.settings);
  const entityRevisions = useRef(new Map<string, string>());
  const forwardedStatusIds = useRef(new Map<string, number>());
  const [entityViewRevision, setEntityViewRevision] = useState(0);
  const messages = getMessages(store.settings.locale);
  const CurrentView = router.route.component;
  const isStandaloneRoute = Boolean(router.route.standalone);
  const isSettingsRoute = router.path === "/settings";
  const activeSettingsSection = useSyncExternalStore(
    subscribeActiveSettingsSection,
    getActiveSettingsSection,
    getActiveSettingsSection,
  );
  const settingsNavigationGroups = getSettingsNavigationGroups(messages.settings);
  const normalizedSettingsSearchQuery = settingsSearchQuery
    .trim()
    .toLocaleLowerCase(store.settings.locale);
  const filteredSettingsNavigationGroups = settingsNavigationGroups
    .map((section) => ({
      ...section,
      items: section.items.filter((item) => (
        !normalizedSettingsSearchQuery
        || `${item.label} ${item.description}`
          .toLocaleLowerCase(store.settings.locale)
          .includes(normalizedSettingsSearchQuery)
      )),
    }))
    .filter((section) => section.items.length > 0);
  const navSections = useMemo(
    () => [
      {
        key: "work",
        title: store.settings.locale === "en-US" ? "Work" : "工作",
        items: [
          { label: messages.nav.dashboard, to: "/", icon: "dashboard" },
          { label: messages.nav.newJob, to: "/jobs/new", icon: "plus" },
          { label: messages.nav.jobs, to: "/jobs", icon: "tray" },
        ] satisfies NavItem[],
      },
      {
        key: "work-games",
        title: store.settings.locale === "en-US" ? "Work Games" : "打工",
        items: [{ label: messages.nav.workMarket, to: "/work-market", icon: "market" }] satisfies NavItem[],
      },
      {
        key: "resources",
        title: store.settings.locale === "en-US" ? "Resources" : "资源",
        items: [
          { label: messages.nav.models, to: "/models", icon: "chip" },
          { label: messages.nav.templates, to: "/templates", icon: "doc" },
          { label: messages.nav.members, to: "/members", icon: "people" },
        ] satisfies NavItem[],
      },
      {
        key: "companion",
        title: store.settings.locale === "en-US" ? "Companion" : "伙伴",
        items: [
          { label: messages.nav.pet, to: "/pet", icon: "pet" },
          { label: messages.nav.petStore, to: "/pet-store", icon: "store" },
        ] satisfies NavItem[],
      },
      {
        key: "benefits",
        title: store.settings.locale === "en-US" ? "Benefits" : "福利",
        items: [
          { label: messages.nav.redeemKey, to: "/redeem-key", icon: "key" },
          { label: messages.nav.dailyCheckIn, to: "/daily-check-in", icon: "check" },
          { label: messages.nav.dailyBlindBox, to: "/daily-blind-box", icon: "gift" },
        ] satisfies NavItem[],
      },
    ],
    [messages, store.settings.locale],
  );
  const currentModeLabel = store.localMode
    ? messages.shell.localMode
    : messages.shell.remoteMode;
  const desktopPetVisible = Boolean(petStore.settings?.desktopEnabled);
  const toolbarStatus = store.localMode
    ? messages.shell.localReady
    : messages.shell.remoteReady;
  const toolbarMetrics = [
    {
      key: "cpu",
      label: messages.shell.cpu,
      value: `${Math.max(0, processMetrics.cpuPercent).toFixed(1)}%`,
    },
    {
      key: "memory",
      label: messages.shell.memory,
      value: `${Math.max(0, processMetrics.memoryMb)}M`,
    },
    {
      key: "graphics",
      label: messages.shell.graphics,
      value: `${Math.max(0, graphicsMemoryMb)}M`,
    },
  ] as const;
  const activeJobCount = store.jobs.filter((job) =>
    ["queued", "transcribing", "speaker_processing", "summarizing"].includes(job.overallStatus),
  ).length;

  useEffect(() => {
    if (isStandaloneRoute) {
      return;
    }
    let active = true;

    void getVersion()
      .then((version) => {
        if (active) {
          setAppVersion(version);
        }
      })
      .catch(() => {
        if (active) {
          setAppVersion("dev");
        }
      });

    return () => {
      active = false;
    };
  }, [isStandaloneRoute]);

  useEffect(() => {
    if (!isSettingsRoute) {
      lastAppPath.current = router.path;
      setSettingsSearchQuery("");
    }
  }, [isSettingsRoute, router.path]);

  useEffect(() => {
    void store.ensureSettingsLoaded().catch(() => undefined);
  }, []);

  useEffect(() => {
    if (isStandaloneRoute) {
      return;
    }

    const jobs = store.jobs.map((job) => ({
      id: job.id,
      title: job.title,
      status: job.overallStatus,
    }));

    if (!didHydrateJobStatuses.current) {
      knownJobStatuses.current.clear();
      for (const job of jobs) {
        knownJobStatuses.current.set(job.id, job.status);
      }
      didHydrateJobStatuses.current = true;
      return;
    }

    const nextStatuses = new Map<string, string>();

    for (const job of jobs) {
      const previousStatus = knownJobStatuses.current.get(job.id);
      nextStatuses.set(job.id, job.status);

      if (previousStatus && previousStatus !== "transcribing" && job.status === "transcribing") {
        void petStore.applyWorkflowEvent({
          eventType: "transcription_started",
          metadata: job.id,
        }).catch(() => undefined);
      }

      if (previousStatus && previousStatus !== "completed" && job.status === "completed") {
        void notifyJobCompleted(job);
      }
    }

    knownJobStatuses.current = nextStatuses;
  }, [isStandaloneRoute, store.jobs]);

  useEffect(() => {
    if (isStandaloneRoute) {
      return;
    }

    store.setGlobalEffectsEnabled(true);
    let stopEntityChanges: (() => void) | null = null;
    let stopJobWindowRequests: (() => void) | null = null;
    let stopForwardedStatus: (() => void) | null = null;
    void listenForEntityChanges((event) => {
      const revisionKey = `${event.entity}:${event.id}`;
      const previousRevision = entityRevisions.current.get(revisionKey);
      if (previousRevision && previousRevision >= event.revision) {
        return;
      }
      entityRevisions.current.set(revisionKey, event.revision);
      if (event.entity === "job" || event.entity === "summary") {
        void store.refreshJob(event.id).catch(() => store.refreshJobs().then(() => undefined));
      } else if (event.entity === "model" || event.entity === "template") {
        void aiStore.reloadState().catch(() => undefined);
      } else if (event.entity === "member") {
        setEntityViewRevision((revision) => revision + 1);
      }
    }).then((unlisten) => {
      stopEntityChanges = unlisten;
    });
    void listenForJobWindowOpenRequests((request) => {
      void fulfillJobWindowOpenRequest(request).catch(() => undefined);
    }).then((unlisten) => {
      stopJobWindowRequests = unlisten;
    });
    void listenForForwardedAppStatus((event) => {
      const key = `${event.source}:${event.notificationId}`;
      const localId = forwardedStatusIds.current.get(key);
      if (event.kind === "clear") {
        if (localId !== undefined) {
          clearAppStatusNotification(localId);
          forwardedStatusIds.current.delete(key);
        }
        return;
      }
      if (!event.notification) return;
      if (localId !== undefined) clearAppStatusNotification(localId);
      forwardedStatusIds.current.set(key, publishAppStatus(event.notification.message, {
        tone: event.notification.tone,
        durationMs: event.durationMs,
      }));
    }).then((unlisten) => {
      stopForwardedStatus = unlisten;
    });
    void initializeWindowTitlebar();
    void initializePetState();
    updateGraphicsMemoryEstimate();
    syncToolbarMetricsPolling();
    window.addEventListener("focus", syncToolbarMetricsPolling);
    window.addEventListener("blur", syncToolbarMetricsPolling);
    document.addEventListener("visibilitychange", syncToolbarMetricsPolling);
    window.addEventListener("resize", updateGraphicsMemoryEstimate);
    const stopWatchingSystemTheme = watchSystemThemeChange(() => {
      if (latestSettings.current.themeMode === "auto") {
        applyAppearance(latestSettings.current);
      }
    });
    void refreshToolbarMetrics();

    return () => {
      store.setGlobalEffectsEnabled(false);
      stopEntityChanges?.();
      stopJobWindowRequests?.();
      stopForwardedStatus?.();
      window.removeEventListener("focus", syncToolbarMetricsPolling);
      window.removeEventListener("blur", syncToolbarMetricsPolling);
      document.removeEventListener("visibilitychange", syncToolbarMetricsPolling);
      window.removeEventListener("resize", updateGraphicsMemoryEstimate);
      stopWatchingSystemTheme();
      if (metricsPollingId.current !== null) {
        window.clearInterval(metricsPollingId.current);
        metricsPollingId.current = null;
      }
    };
  }, [isStandaloneRoute]);

  useEffect(() => {
    latestSettings.current = store.settings;
    applyAppearance(store.settings);
  }, [store.settings]);

  function isActive(itemTo: string) {
    if (itemTo === "/jobs") {
      return router.path === "/jobs"
        || (router.path !== "/jobs/new" && /^\/jobs\/[^/]+(?:\/workbench)?$/.test(router.path));
    }

    if (itemTo === "/work-market") {
      return router.path === "/work-market"
        || router.path === "/farm"
        || router.path.startsWith("/work-game/");
    }

    return router.path === itemTo;
  }

  async function openProjectGithub() {
    await openExternalUrl("https://github.com/westng/Liberty");
  }

  async function initializeWindowTitlebar() {
    if (!navigator.userAgent.includes("Windows")) {
      return;
    }

    setIsWindowsTitlebar(true);
    await getCurrentWindow().setDecorations(false);
  }

  async function minimizeWindow() {
    await getCurrentWindow().minimize();
  }

  async function toggleMaximizeWindow() {
    await getCurrentWindow().toggleMaximize();
  }

  async function closeWindow() {
    await getCurrentWindow().close();
  }

  async function initializePetState() {
    try {
      await petStore.loadPetState();
      if (!didRecordPetDailyOpen.current) {
        didRecordPetDailyOpen.current = true;
        void petStore.applyWorkflowEvent({
          eventType: "daily_open",
          metadata: "Liberty app opened",
        }).catch(() => undefined);
      }
      const settings = petStore.settings;
      if (settings) {
        await syncDesktopPetState(settings, "startup").catch((error) => {
          console.error("[pet-window] failed to sync native pet window", error);
        });
      }
    } catch {
      // Keep the main app usable even if the pet state fails to load.
    }
  }

  async function syncDesktopPetState(settings: PetSettings, source: string) {
    if (lastAppliedDesktopPetEnabled.current === settings.desktopEnabled) {
      return;
    }

    lastAppliedDesktopPetEnabled.current = settings.desktopEnabled;
    await applyDesktopPetState(settings, source);
  }

  async function toggleToolbarPetDesktop() {
    await runAppStatusAction("togglePet", async () => {
      await petStore.loadPetState();
      const current = petStore.settings;
      if (!current) {
        return;
      }

      const savedSettings = await petStore.saveSettings({
        ...current,
        desktopEnabled: !current.desktopEnabled,
      });
      lastAppliedDesktopPetEnabled.current = null;
      await syncDesktopPetState(savedSettings, "toolbar");
    });
  }

  async function notifyJobCompleted(job: { id: string; title: string }) {
    publishAppStatus(formatMessage(messages.shell.jobCompletedBody, { title: job.title }), {
      tone: "success",
      durationMs: 7000,
    });

    if (typeof window !== "undefined" && typeof Notification !== "undefined") {
      let permission = Notification.permission;

      if (permission === "default") {
        permission = await Notification.requestPermission();
      }

      if (permission === "granted") {
        new Notification(messages.shell.jobCompletedTitle, {
          body: formatMessage(messages.shell.jobCompletedBody, { title: job.title }),
        });
      }
    }

    try {
      await petStore.applyWorkflowEvent({
        eventType: "transcription_completed",
        metadata: job.id,
      });
    } catch {
      // Pet updates are best-effort only.
    }
  }

  async function refreshToolbarMetrics() {
    try {
      setProcessMetrics(await getProcessMetrics());
    } catch {
      // Keep the last known metrics when polling fails.
    }
    updateGraphicsMemoryEstimate();
  }

  function syncToolbarMetricsPolling() {
    if (typeof window === "undefined") {
      return;
    }

    const shouldPoll = document.visibilityState !== "hidden" && document.hasFocus();

    if (shouldPoll && metricsPollingId.current === null) {
      metricsPollingId.current = window.setInterval(() => {
        void refreshToolbarMetrics();
      }, 10000);
      return;
    }

    if (!shouldPoll && metricsPollingId.current !== null) {
      window.clearInterval(metricsPollingId.current);
      metricsPollingId.current = null;
    }
  }

  function updateGraphicsMemoryEstimate() {
    const width = window.innerWidth || 0;
    const height = window.innerHeight || 0;
    const pixelRatio = Math.max(window.devicePixelRatio || 1, 1);
    const bytes = width * height * 4 * pixelRatio * pixelRatio;
    setGraphicsMemoryMb(Math.max(1, Math.round(bytes / (1024 * 1024))));
  }

  const currentView = (
    <Suspense fallback={<div className="view-stack native-page" />}>
      <CurrentView key={router.path === "/members" ? entityViewRevision : router.path} />
    </Suspense>
  );

  if (isStandaloneRoute) {
    return currentView;
  }

  return (
    <div className={`app-shell${isSettingsRoute ? " app-shell-settings" : ""}`}>
      <div className={`window-titlebar ${isWindowsTitlebar ? "windows-titlebar" : ""}`}>
        <div className="window-titlebar-metrics">
          {toolbarMetrics.map((metric) => (
            <div key={metric.key} className="toolbar-metric titlebar-metric">
              <span className="toolbar-metric-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24">
                  <path fill="currentColor" d={shellIconPaths[metric.key]} />
                </svg>
              </span>
              <span className="toolbar-metric-label">{metric.label}</span>
              <strong className="toolbar-metric-value">{metric.value}</strong>
            </div>
          ))}
        </div>
        <div className="window-titlebar-spacer" data-tauri-drag-region />
        <div className="window-titlebar-actions">
          <button
            className="toolbar-pet-toggle titlebar-pet-toggle"
            type="button"
            aria-pressed={desktopPetVisible}
            title={desktopPetVisible ? messages.shell.petDisableTitle : messages.shell.petEnableTitle}
            onClick={toggleToolbarPetDesktop}
          >
            <span className="toolbar-pet-dot" data-active={desktopPetVisible} />
            {desktopPetVisible ? messages.shell.petEnabled : messages.shell.petDisabled}
          </button>
          <div className="toolbar-pill titlebar-pill">
            <span className="toolbar-pill-dot" />
            {toolbarStatus}
          </div>
          <button
            className="toolbar-icon-btn titlebar-icon-btn"
            type="button"
            aria-label={messages.shell.github}
            title={messages.shell.github}
            onClick={openProjectGithub}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path fill="currentColor" d={shellIconPaths.github} />
            </svg>
          </button>
        </div>
        {isWindowsTitlebar && (
          <div className="window-controls">
            <button className="window-control" type="button" aria-label="Minimize" onClick={minimizeWindow}>
              -
            </button>
            <button className="window-control" type="button" aria-label="Maximize" onClick={toggleMaximizeWindow}>
              □
            </button>
            <button className="window-control window-control-close" type="button" aria-label="Close" onClick={closeWindow}>
              ×
            </button>
          </div>
        )}
      </div>

      <aside className="sidebar">
        {isSettingsRoute ? (
          <div className="nav-wrap settings-sidebar">
            <header className="settings-sidebar-header">
              <Link className="settings-sidebar-back" to={lastAppPath.current}>
                <span
                  className="nav-link-icon"
                  aria-hidden="true"
                  dangerouslySetInnerHTML={{ __html: navIconSvg.back }}
                />
                <span>{messages.settings.backToApp}</span>
              </Link>
              <h2 className="settings-sidebar-title">{messages.settings.pageTitle}</h2>
              <div className="settings-sidebar-search">
                <span
                  className="settings-sidebar-search-icon"
                  aria-hidden="true"
                  dangerouslySetInnerHTML={{ __html: navIconSvg.search }}
                />
                <input
                  type="search"
                  value={settingsSearchQuery}
                  placeholder={messages.settings.searchPlaceholder}
                  aria-label={messages.settings.searchPlaceholder}
                  onChange={(event) => setSettingsSearchQuery(event.target.value)}
                />
              </div>
            </header>

            <nav className="settings-sidebar-groups" aria-label={messages.settings.navigationLabel}>
              {filteredSettingsNavigationGroups.map((section) => (
                <section key={section.label} className="settings-sidebar-group">
                  <p className="nav-section-title">{section.label}</p>
                  {section.items.map((item) => (
                    <button
                      key={item.id}
                      className={`nav-link settings-sidebar-item${activeSettingsSection === item.id ? " active" : ""}`}
                      type="button"
                      aria-current={activeSettingsSection === item.id ? "page" : undefined}
                      onClick={() => setActiveSettingsSection(item.id)}
                    >
                      <span
                        className="nav-link-icon"
                        aria-hidden="true"
                        dangerouslySetInnerHTML={{ __html: navIconSvg[item.icon] }}
                      />
                      <span className="nav-link-label">{item.label}</span>
                    </button>
                  ))}
                </section>
              ))}
              {filteredSettingsNavigationGroups.length === 0 && (
                <p className="settings-sidebar-empty" role="status">
                  {messages.settings.searchEmpty}
                </p>
              )}
            </nav>
          </div>
        ) : (
        <div className="nav-wrap">
          <header className="nav-header">
            <div className="nav-brand">
              <img className="nav-brand-image" src={sidebarMascotUrl} alt="Liberty mascot" />
              <h1>Liberty.</h1>
            </div>
            <p className="nav-slogan">{messages.shell.slogan}</p>
          </header>

          <nav className="nav-list" aria-label="Liberty">
            {navSections.map((section) => (
              <section key={section.key} className="nav-section">
                <p className="nav-section-title">{section.title}</p>
                {section.items.map((item) => (
                  <Link key={item.to} to={item.to} className={`nav-link ${isActive(item.to) ? "active" : ""}`}>
                    <span className="nav-link-icon" aria-hidden="true" dangerouslySetInnerHTML={{ __html: navIconSvg[item.icon] }} />
                    <span className="nav-link-label">{item.label}</span>
                  </Link>
                ))}
              </section>
            ))}
          </nav>

          <div className="nav-footer">
            <Link to="/settings" className={`nav-link nav-footer-link ${isActive("/settings") ? "active" : ""}`}>
              <span className="nav-link-icon" aria-hidden="true" dangerouslySetInnerHTML={{ __html: navIconSvg.gear }} />
              <span className="nav-link-label">{messages.nav.settings}</span>
            </Link>
            <div className="nav-footer-item">
              <span className="nav-footer-label">
                <span className="nav-link-icon" aria-hidden="true" dangerouslySetInnerHTML={{ __html: navIconSvg.mode }} />
                <span>{messages.shell.modeLabel}</span>
              </span>
              <strong>{currentModeLabel}</strong>
            </div>
            <div className="nav-footer-item">
              <span className="nav-footer-label">
                <span className="nav-link-icon" aria-hidden="true" dangerouslySetInnerHTML={{ __html: navIconSvg.processing }} />
                <span>{messages.shell.processingLabel}</span>
              </span>
              <strong>{activeJobCount}</strong>
            </div>
          </div>
        </div>
        )}
      </aside>

      <main className="content">
        <section className="content-page">
          <div className={`content-shell ${isSettingsRoute ? "settings-content-shell" : ""}`}>
            <section className="content-body">
              {store.settingsLoadError && !isStandaloneRoute ? (
                <div className="note-block error-block" role="alert">
                  <strong>{store.settingsLoadError}</strong>
                  <div className="button-row">
                    <button
                      className="secondary-button"
                      type="button"
                      onClick={() => void store
                        .ensureSettingsLoaded(true)
                        .then(() => store.setGlobalEffectsEnabled(true))
                        .catch(() => undefined)}
                    >
                      重试加载设置
                    </button>
                  </div>
                </div>
              ) : currentView}
            </section>
          </div>
        </section>
        <footer className="content-statusbar" data-tone={statusNotification?.tone ?? "idle"} role="status" aria-live="polite">
          <span className="content-statusbar-message">
            <span className="content-statusbar-dot" aria-hidden="true" />
            <span>{statusNotification?.message ?? messages.shell.statusReady}</span>
          </span>
          <span className="content-statusbar-version">v{appVersion || "-"}</span>
        </footer>
      </main>
    </div>
  );
}

export default function App() {
  return (
    <RouterProvider>
      <AppContent />
    </RouterProvider>
  );
}
