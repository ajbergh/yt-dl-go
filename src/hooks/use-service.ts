import { useCallback, useEffect, useRef, useState } from "react";
import { desktopBridge, reportDesktopSmokeStage } from "../lib/desktop";
import {
  api, streamServiceEvents,
  type AppSettings, type BuildInfo, type DownloadJob, type LibraryPageResponse, type LibraryQuery, type RuntimeSettingSources, type RuntimeSettingValues, type ServiceConnection, type ServiceEvent, type ServiceHealth, type UpdateStatus,
} from "../lib/downloader";
import {
  builtInServiceConnection, errorMessage, notificationAPI, queueItemsFor, terminalNotification,
} from "../components/downloader/view-model";

type StorageWarning = { path: string; freeBytes: number; totalBytes: number; freePercent: number };

const fallbackCategories = ["Tech", "Science", "Coding", "Music", "Education", "Gaming", "Podcasts", "Archival", "General"];

function initialLibraryQuery(): LibraryQuery {
  try {
    const sort = window.localStorage.getItem("yt-dl-go:library-sort-v2");
    return { sort: sort === "oldest" || sort === "title" || sort === "size" || sort === "duration" ? sort : "recent" };
  } catch {
    return { sort: "recent" };
  }
}

function libraryPagePath(query: LibraryQuery, cursor?: string): string {
  const params = new URLSearchParams({ limit: "50", sort: query.sort || "recent" });
  if (query.q) params.set("q", query.q);
  if (query.type) params.set("type", query.type);
  if (query.category) params.set("category", query.category);
  if (query.channel) params.set("channel", query.channel);
  if (query.favorites) params.set("favorites", "1");
  if (query.collection) params.set("collection", query.collection);
  if (query.playlists) params.set("playlists", "1");
  if (query.source) params.set("source", query.source);
  if (query.recent) params.set("recent", "1");
  if (cursor) params.set("cursor", cursor);
  return `/api/library/files?${params.toString()}`;
}

const emptyLibraryPage: LibraryPageResponse = {
  mediaCounts: { video: 0, audio: 0, favorites: 0, playlists: 0 },
  jobs: [], totalJobs: 0, totalFiles: 0, categories: [], channels: [],
  stats: { files: 0, logicalBytes: 0, managedBytes: 0, publishedBytes: 0 },
};

function normalizeLibraryPage(response: Partial<LibraryPageResponse>): LibraryPageResponse {
  const jobs = response.jobs ?? [];
  const categories = response.categories ?? (() => {
    const counts = new Map<string, number>();
    for (const job of jobs) {
      const category = job.category || job.files.find(file => file.category)?.category || "Uncategorized";
      counts.set(category, (counts.get(category) ?? 0) + job.files.length);
    }
    return [...counts].map(([value, count]) => ({ value, count }));
  })();
  const channels = response.channels ?? (() => {
    const counts = new Map<string, number>();
    for (const job of jobs) for (const file of job.files) {
      const channel = file.author?.trim() || "Unknown channel";
      counts.set(channel, (counts.get(channel) ?? 0) + 1);
    }
    return [...counts].map(([value, count]) => ({ value, count }));
  })();
  const stats = response.stats ?? jobs.reduce((total, job) => {
    for (const file of job.files) {
      total.files += 1;
      total.logicalBytes += file.size + (file.subtitle?.size ?? 0);
      if (file.managedAvailable !== false) total.managedBytes += file.size;
      if (file.subtitle?.managedAvailable) total.managedBytes += file.subtitle.size;
      if (file.publishedAvailable !== false && file.outputRelativePath) total.publishedBytes += file.size;
      if (file.subtitle?.publishedAvailable && file.subtitle.outputRelativePath) total.publishedBytes += file.subtitle.size;
    }
    return total;
  }, { ...emptyLibraryPage.stats });
  return {
    ...emptyLibraryPage,
    ...response,
    jobs,
    totalJobs: response.totalJobs ?? jobs.length,
    totalFiles: response.totalFiles ?? jobs.reduce((total, job) => total + job.files.length, 0),
    categories,
    channels,
    stats,
  };
}

const defaultSettings: AppSettings = {
  defaultQuality: "best",
  defaultVideoStrategy: "best",
  allow360pFallback: false,
  maxConcurrentDownloads: 3,
  bandwidthLimitBytesPerSec: 0,
  notificationsEnabled: false,
  downloadLocation: "",
  namingPattern: "{channel} - {title} [{resolution}]",
  subfolderSorting: "channel",
  outputFileMode: "0600",
  outputFolderMode: "0700",
  defaultCategory: "General",
  userCategories: fallbackCategories,
  retention: "never",
  jobTimeout: "none",
  chromePath: "",
  downloadSlots: 4,
};

function hydratedSettings(settings: AppSettings, previous: AppSettings = defaultSettings): AppSettings {
  return {
    ...previous,
    ...settings,
    defaultVideoStrategy: settings.defaultVideoStrategy || "best",
    allow360pFallback: settings.allow360pFallback ?? false,
    maxConcurrentDownloads: settings.maxConcurrentDownloads || 3,
    bandwidthLimitBytesPerSec: settings.bandwidthLimitBytesPerSec ?? 0,
    notificationsEnabled: settings.notificationsEnabled ?? false,
    namingPattern: settings.namingPattern || "{channel} - {title} [{resolution}]",
    subfolderSorting: settings.subfolderSorting || "channel",
    outputFileMode: settings.outputFileMode || "0600",
    outputFolderMode: settings.outputFolderMode || "0700",
    defaultCategory: settings.defaultCategory || "General",
    userCategories: settings.userCategories?.length ? settings.userCategories : previous.userCategories,
    retention: settings.retention || "never",
    jobTimeout: settings.jobTimeout || "none",
    chromePath: settings.chromePath ?? "",
    downloadSlots: settings.downloadSlots || 4,
  };
}

export function useService() {
  const [connection] = useState<ServiceConnection>(builtInServiceConnection);
  const [serviceReady, setServiceReady] = useState(false);
  useEffect(() => {
    if (serviceReady) void desktopBridge()?.UIReady?.();
  }, [serviceReady]);
  const [jobs, setJobs] = useState<DownloadJob[]>([]);
  const [libraryJobs, setLibraryJobs] = useState<DownloadJob[]>([]);
  const [libraryPage, setLibraryPage] = useState<LibraryPageResponse>(emptyLibraryPage);
  const [libraryQuery, setLibraryQueryState] = useState<LibraryQuery>(initialLibraryQuery);
  const [loadingLibraryMore, setLoadingLibraryMore] = useState(false);
  const [settings, setSettings] = useState<AppSettings>(defaultSettings);
  const [runtimeSettingSources, setRuntimeSettingSources] = useState<RuntimeSettingSources>({});
  const [runtimeSettingValues, setRuntimeSettingValues] = useState<RuntimeSettingValues>({});
  const [mp3Supported, setMp3Supported] = useState(false);
  const [buildInfo, setBuildInfo] = useState<BuildInfo>({ version: "dev", commit: "unknown", buildDate: "unknown" });
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null);
  const [updateError, setUpdateError] = useState("");
  const [checkingUpdates, setCheckingUpdates] = useState(false);
  const [serviceError, setServiceError] = useState("");
  const [storageWarnings, setStorageWarnings] = useState<StorageWarning[]>([]);
  const [pollError, setPollError] = useState("");
  const knownTaskStatuses = useRef<Map<string, Map<number, DownloadJob["status"]>>>(new Map());
  const notificationsEnabledRef = useRef(false);
  const libraryQueryRef = useRef<LibraryQuery>(libraryQuery);
  const libraryRequestSequence = useRef(0);
  const libraryLoadedPages = useRef(1);

  const setLibraryQuery = useCallback((query: LibraryQuery) => {
    // Applying the identical default/sort/filter query must not reset an
    // already paginated Library view after the debounce timer expires.
    if (JSON.stringify(libraryQueryRef.current) === JSON.stringify(query)) return;
    libraryLoadedPages.current = 1;
    libraryQueryRef.current = query;
    setLibraryQueryState(query);
  }, []);

  const refreshLibrary = useCallback(async () => {
    const requestID = ++libraryRequestSequence.current;
    const query = libraryQueryRef.current;
    const pages = libraryLoadedPages.current;
    const jobs: DownloadJob[] = [];
    const known = new Set<string>();
    let result: LibraryPageResponse;
    let cursor: string | undefined;
    let loadedPages = 0;
    do {
      const response = await api<LibraryPageResponse>(connection, libraryPagePath(query, cursor), {
        signal: AbortSignal.timeout(10000),
      });
      if (requestID !== libraryRequestSequence.current || query !== libraryQueryRef.current) return [];
      result = normalizeLibraryPage(response);
      for (const job of result.jobs) {
        const files = job.files.filter(file => {
          const key = job.id + ":" + file.id;
          if (known.has(key)) return false;
          known.add(key);
          return true;
        });
        if (files.length) jobs.push({ ...job, files });
      }
      loadedPages++;
      cursor = result.nextCursor || undefined;
    } while (cursor && loadedPages < pages);
    if (requestID === libraryRequestSequence.current && query === libraryQueryRef.current) {
      libraryLoadedPages.current = loadedPages;
      setLoadingLibraryMore(false);
      setLibraryJobs(jobs);
      setLibraryPage(result);
    }
    return jobs;
  }, [connection]);

  const loadMoreLibrary = useCallback(async () => {
    const cursor = libraryPage.nextCursor;
    if (!cursor || loadingLibraryMore) return;
    setLoadingLibraryMore(true);
    const requestID = ++libraryRequestSequence.current;
    try {
      const query = libraryQueryRef.current;
      const response = await api<LibraryPageResponse>(connection, libraryPagePath(query, cursor), {
        signal: AbortSignal.timeout(10000),
      });
      const result = normalizeLibraryPage(response);
      if (requestID !== libraryRequestSequence.current || query !== libraryQueryRef.current) return;
      libraryLoadedPages.current++;
      setLibraryJobs(previous => {
        const known = new Set(previous.flatMap(job => job.files.map(file => job.id + ":" + file.id)));
        const additions = result.jobs.filter(job => {
          const file = job.files[0];
          if (!file) return false;
          const key = job.id + ":" + file.id;
          if (known.has(key)) return false;
          known.add(key);
          return true;
        });
        return [...previous, ...additions];
      });
      setLibraryPage(result);
    } finally {
      if (requestID === libraryRequestSequence.current) setLoadingLibraryMore(false);
    }
  }, [connection, libraryPage.nextCursor, loadingLibraryMore]);

  useEffect(() => {
    if (serviceReady) void refreshLibrary().catch(error => setPollError(errorMessage(error)));
  }, [libraryQuery, refreshLibrary, serviceReady]);

  useEffect(() => {
    notificationsEnabledRef.current = settings.notificationsEnabled;
  }, [settings.notificationsEnabled]);

  async function checkForUpdates() {
    setCheckingUpdates(true);
    setUpdateError("");
    try {
      const status = await api<UpdateStatus>(connection, "/api/update", {
        signal: AbortSignal.timeout(10000),
      });
      setUpdateStatus(status);
    } catch (error) {
      setUpdateError(errorMessage(error));
    } finally {
      setCheckingUpdates(false);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    let retryTimer: ReturnType<typeof setTimeout>;

    async function initialize() {
      try {
        reportDesktopSmokeStage("health-request");
        const health = await api<ServiceHealth>(connection, "/api/health", {
          signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        });
        reportDesktopSmokeStage("health-response");
        if (health.engine !== "native-go" || health.capabilities?.externalBinariesRequired !== false) {
          throw new Error("This app version is incompatible. Please reinstall the latest version.");
        }
        if (!health.ready) {
          reportDesktopSmokeStage("health-not-ready");
          throw new Error(health.missing.length
            ? `The app needs attention: ${health.missing.join(", ")}.`
            : "The app is starting up.");
        }
        setMp3Supported(health.capabilities?.mp3AudioSupported === true);
        setBuildInfo({
          version: health.version || "dev",
          commit: health.commit || "unknown",
          buildDate: health.buildDate || "unknown",
        });
        const [jobResult, libraryResult, settingResult] = await Promise.all([
          api<{ jobs: DownloadJob[] }>(connection, "/api/jobs", {
            signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          }),
          api<LibraryPageResponse>(connection, libraryPagePath(libraryQueryRef.current), {
            signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          }),
          api<{ settings: AppSettings; sources?: RuntimeSettingSources; effective?: RuntimeSettingValues }>(connection, "/api/settings", {
            signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          }),
        ]);
        if (controller.signal.aborted) return;
        setJobs(jobResult.jobs);
        setLibraryJobs(libraryResult.jobs);
        setLibraryPage(normalizeLibraryPage(libraryResult));
        knownTaskStatuses.current = new Map(jobResult.jobs.map(job => [
          job.id, new Map(queueItemsFor(job).map(item => [item.index, item.status])),
        ]));
        setSettings(previous => hydratedSettings(settingResult.settings, previous));
        setRuntimeSettingSources(settingResult.sources ?? {});
        setRuntimeSettingValues(settingResult.effective ?? {});
        setServiceError("");
        setServiceReady(true);
        void api<UpdateStatus>(connection, "/api/update", {
          signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        }).then(status => {
          if (!controller.signal.aborted) setUpdateStatus(status);
        }).catch(() => {
          // Update discovery is advisory and must never affect service readiness.
        });
      } catch (error) {
        if (controller.signal.aborted) return;
        setServiceReady(false);
        setServiceError(errorMessage(error));
        reportDesktopSmokeStage("service-error");
        retryTimer = setTimeout(() => { void initialize(); }, 2500);
      }
    }

    void initialize();
    return () => {
      controller.abort();
      clearTimeout(retryTimer);
    };
  }, [connection]);

  useEffect(() => {
    if (!serviceReady) return;
    const controller = new AbortController();
    let reconnectTimer: ReturnType<typeof setTimeout>;

    const mergeLiveJob = (job: DownloadJob) => {
      setJobs(previous => previous.some(item => item.id === job.id)
        ? previous.map(item => item.id === job.id ? job : item)
        : [job, ...previous]);
    };

    const maybeNotifyTerminal = (job: DownloadJob) => {
      const statuses = knownTaskStatuses.current.get(job.id) ?? new Map();
      knownTaskStatuses.current.set(job.id, statuses);
      for (const item of queueItemsFor(job)) {
        const previousStatus = statuses.get(item.index);
        statuses.set(item.index, item.status);
        // Seed initial snapshots silently and avoid repeats on reconnect/polling.
        if (previousStatus === undefined || previousStatus === item.status || !notificationsEnabledRef.current) continue;
        const notification = terminalNotification({
          ...job, kind: "video", title: item.title, status: item.status,
          error: item.error || (job.kind === "video" ? job.error : ""),
        });
        const NotificationAPI = notificationAPI();
        if (!notification || !NotificationAPI || NotificationAPI.permission !== "granted") continue;
        try {
          new NotificationAPI(notification.title, {
            body: notification.body,
            tag: `yt-dl-go:${job.id}:${item.index}:${item.status}`,
          });
        } catch {
          // Notification support is optional; task state remains authoritative.
        }
      }
    };

    const applyLiveEvent = (event: ServiceEvent) => {
      if (controller.signal.aborted) return;
      if (event.type === "snapshot") {
        if (event.jobs) {
          for (const job of event.jobs) maybeNotifyTerminal(job);
          setJobs(event.jobs);
          void refreshLibrary().catch(error => setPollError(errorMessage(error)));
        }
        if (event.settings) {
          setSettings(previous => hydratedSettings(event.settings!, previous));
        }
        if (event.settingsSources) setRuntimeSettingSources(event.settingsSources);
        if (event.settingsEffective) setRuntimeSettingValues(event.settingsEffective);
      } else if (event.type === "job-deleted" && event.jobId) {
        knownTaskStatuses.current.delete(event.jobId);
        setJobs(previous => previous.filter(job => job.id !== event.jobId));
        void refreshLibrary().catch(error => setPollError(errorMessage(error)));
      } else if (event.type === "settings-changed" && event.settings) {
        setSettings(previous => ({
          ...previous,
          ...event.settings,
          bandwidthLimitBytesPerSec: event.settings?.bandwidthLimitBytesPerSec ?? 0,
          notificationsEnabled: event.settings?.notificationsEnabled ?? false,
        }));
        if (event.settingsSources) setRuntimeSettingSources(event.settingsSources);
        if (event.settingsEffective) setRuntimeSettingValues(event.settingsEffective);
      } else if (event.job) {
        maybeNotifyTerminal(event.job);
        mergeLiveJob(event.job);
        if (event.type === "job-file-finalized" || ["completed", "partial", "failed", "cancelled"].includes(event.job.status)) {
          void refreshLibrary().catch(error => setPollError(errorMessage(error)));
        }
      }
      setPollError("");
    };

    async function reconcile() {
      try {
        const [result] = await Promise.all([
          api<{ jobs: DownloadJob[] }>(connection, "/api/jobs", {
            signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          }),
          refreshLibrary(),
        ]);
        if (!controller.signal.aborted) {
          for (const job of result.jobs) maybeNotifyTerminal(job);
          setJobs(result.jobs);
        }
      } catch (error) {
        if (!controller.signal.aborted) setPollError(errorMessage(error));
      }
    }

    async function connect() {
      try {
        await streamServiceEvents(connection, applyLiveEvent, controller.signal);
        if (!controller.signal.aborted) throw new Error("Live job updates disconnected.");
      } catch (error) {
        if (controller.signal.aborted) return;
        setPollError(`${errorMessage(error)} Reconnecting…`);
        reconnectTimer = setTimeout(() => { void connect(); }, 1500);
      }
    }

    void connect();
    const reconcileTimer = setInterval(() => { void reconcile(); }, 30000);
    return () => {
      controller.abort();
      clearTimeout(reconnectTimer);
      clearInterval(reconcileTimer);
    };
  }, [connection, refreshLibrary, serviceReady]);

  useEffect(() => {
    if (!serviceReady) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function checkStorage() {
      try {
        const response = await api<{ warnings: StorageWarning[] }>(connection, "/api/storage", { signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]) });
        if (!controller.signal.aborted) setStorageWarnings(response.warnings ?? []);
      } catch { /* Disk inspection is advisory; keep download controls available. */ }
      if (!controller.signal.aborted) timer = setTimeout(() => void checkStorage(), 10000);
    }
    void checkStorage();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [connection, serviceReady, settings.downloadLocation]);

  return {
    connection,
    serviceReady,
    jobs,
    setJobs,
    libraryJobs,
    setLibraryJobs,
    libraryPage,
    libraryQuery,
    setLibraryQuery,
    refreshLibrary,
    loadMoreLibrary,
    loadingLibraryMore,
    settings,
    setSettings,
    runtimeSettingSources,
    setRuntimeSettingSources,
    runtimeSettingValues,
    setRuntimeSettingValues,
    mp3Supported,
    buildInfo,
    updateStatus,
    updateError,
    checkingUpdates,
    checkForUpdates,
    serviceError,
    setServiceError,
    pollError,
    storageWarnings,
  };
}
