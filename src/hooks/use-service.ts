import { useEffect, useRef, useState } from "react";
import {
  api, streamServiceEvents,
  type AppSettings, type DownloadJob, type ServiceConnection, type ServiceEvent, type ServiceHealth,
} from "../lib/downloader";
import {
  builtInServiceConnection, errorMessage, notificationAPI, terminalNotification,
} from "../components/downloader/view-model";

const fallbackCategories = ["Tech", "Science", "Coding", "Music", "Education", "Gaming", "Podcasts", "Archival", "General"];

const defaultSettings: AppSettings = {
  defaultQuality: "best",
  defaultVideoStrategy: "best",
  maxConcurrentDownloads: 3,
  bandwidthLimitBytesPerSec: 0,
  notificationsEnabled: false,
  downloadLocation: "",
  namingPattern: "{channel} - {title} [{resolution}]",
  subfolderSorting: "channel",
  defaultCategory: "General",
  userCategories: fallbackCategories,
  storageMode: "managed-published",
};

function hydratedSettings(settings: AppSettings, previous: AppSettings = defaultSettings): AppSettings {
  return {
    ...previous,
    ...settings,
    defaultVideoStrategy: settings.defaultVideoStrategy || "best",
    maxConcurrentDownloads: settings.maxConcurrentDownloads || 3,
    bandwidthLimitBytesPerSec: settings.bandwidthLimitBytesPerSec ?? 0,
    notificationsEnabled: settings.notificationsEnabled ?? false,
    namingPattern: settings.namingPattern || "{channel} - {title} [{resolution}]",
    subfolderSorting: settings.subfolderSorting || "channel",
    defaultCategory: settings.defaultCategory || "General",
    userCategories: settings.userCategories?.length ? settings.userCategories : previous.userCategories,
    storageMode: settings.storageMode || "managed-published",
  };
}

export function useService() {
  const [connection] = useState<ServiceConnection>(builtInServiceConnection);
  const [serviceReady, setServiceReady] = useState(false);
  const [jobs, setJobs] = useState<DownloadJob[]>([]);
  const [settings, setSettings] = useState<AppSettings>(defaultSettings);
  const [mp3Supported, setMp3Supported] = useState(false);
  const [serviceError, setServiceError] = useState("");
  const [pollError, setPollError] = useState("");
  const knownJobStatuses = useRef<Map<string, DownloadJob["status"]>>(new Map());
  const notificationsEnabledRef = useRef(false);

  useEffect(() => {
    notificationsEnabledRef.current = settings.notificationsEnabled;
  }, [settings.notificationsEnabled]);

  useEffect(() => {
    const controller = new AbortController();
    let retryTimer: ReturnType<typeof setTimeout>;

    async function initialize() {
      try {
        const health = await api<ServiceHealth>(connection, "/api/health", {
          signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        });
        if (health.engine !== "native-go" || health.capabilities?.externalBinariesRequired !== false) {
          throw new Error("The built-in service is an older or incompatible version.");
        }
        if (!health.ready) {
          throw new Error(health.missing.length
            ? `The Go service is not ready: ${health.missing.join(", ")}.`
            : "The Go service is starting up.");
        }
        setMp3Supported(health.capabilities?.mp3AudioSupported === true);
        const [jobResult, settingResult] = await Promise.all([
          api<{ jobs: DownloadJob[] }>(connection, "/api/jobs", {
            signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          }),
          api<{ settings: AppSettings }>(connection, "/api/settings", {
            signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
          }),
        ]);
        if (controller.signal.aborted) return;
        setJobs(jobResult.jobs);
        knownJobStatuses.current = new Map(jobResult.jobs.map(job => [job.id, job.status]));
        setSettings(previous => hydratedSettings(settingResult.settings, previous));
        setServiceError("");
        setServiceReady(true);
      } catch (error) {
        if (controller.signal.aborted) return;
        setServiceReady(false);
        setServiceError(errorMessage(error));
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
    let reconcileTimer: ReturnType<typeof setInterval>;

    const mergeLiveJob = (job: DownloadJob) => {
      setJobs(previous => previous.some(item => item.id === job.id)
        ? previous.map(item => item.id === job.id ? job : item)
        : [job, ...previous]);
    };

    const maybeNotifyTerminal = (job: DownloadJob) => {
      const previousStatus = knownJobStatuses.current.get(job.id);
      const changed = previousStatus !== undefined && previousStatus !== job.status;
      knownJobStatuses.current.set(job.id, job.status);
      if (!changed || !notificationsEnabledRef.current) return;
      const notification = terminalNotification(job);
      const NotificationAPI = notificationAPI();
      if (!notification || !NotificationAPI || NotificationAPI.permission !== "granted") return;
      try {
        new NotificationAPI(notification.title, {
          body: notification.body,
          tag: `yt-dl-go:${job.id}:${job.status}`,
        });
      } catch {
        // Notification support is optional; job state remains authoritative.
      }
    };

    const applyLiveEvent = (event: ServiceEvent) => {
      if (controller.signal.aborted) return;
      if (event.type === "snapshot") {
        if (event.jobs) {
          for (const job of event.jobs) maybeNotifyTerminal(job);
          setJobs(event.jobs);
        }
        if (event.settings) {
          setSettings(previous => hydratedSettings(event.settings!, previous));
        }
      } else if (event.type === "job-deleted" && event.jobId) {
        knownJobStatuses.current.delete(event.jobId);
        setJobs(previous => previous.filter(job => job.id !== event.jobId));
      } else if (event.type === "settings-changed" && event.settings) {
        setSettings(previous => ({
          ...previous,
          ...event.settings,
          bandwidthLimitBytesPerSec: event.settings?.bandwidthLimitBytesPerSec ?? 0,
          notificationsEnabled: event.settings?.notificationsEnabled ?? false,
        }));
      } else if (event.job) {
        maybeNotifyTerminal(event.job);
        mergeLiveJob(event.job);
      }
      setPollError("");
    };

    async function reconcile() {
      try {
        const result = await api<{ jobs: DownloadJob[] }>(connection, "/api/jobs", {
          signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        });
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
    reconcileTimer = setInterval(() => { void reconcile(); }, 30000);
    return () => {
      controller.abort();
      clearTimeout(reconnectTimer);
      clearInterval(reconcileTimer);
    };
  }, [connection, serviceReady]);

  return {
    connection,
    serviceReady,
    jobs,
    setJobs,
    settings,
    setSettings,
    mp3Supported,
    serviceError,
    setServiceError,
    pollError,
  };
}
