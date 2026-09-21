/**
 * Production downloader screen. The bundled page connects to the Go API served
 * by the same executable, then uses that API for jobs and SQLite preferences.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import { LibraryPage } from "./library";
import { QueuePage } from "./queue";
import { SettingsPage } from "./settings";
import type { DragEndEvent } from "@dnd-kit/core";
import { arrayMove } from "@dnd-kit/sortable";
import {
  Activity, AlertCircle, ArrowDownToLine, Check, ChevronDown, CircleHelp, Clock3,
  DownloadCloud, FileText, Film, Folder, FolderTree, Gauge, HardDrive, Layers, ListVideo, LoaderCircle,
  Pause, Play, Plus, RefreshCw, Search, Settings, ShieldCheck, Sparkles, Trash2, X,
} from "lucide-react";
import {
  api, formatBytes, isActive, parseYouTubeURL, streamServiceEvents,
  type AppSettings, type DownloadFile, type DownloadJob, type Inspection, type QueueItem, type Quality,
  type ServiceConnection, type ServiceEvent, type ServiceHealth,
} from "../lib/downloader";

import {
  LibraryThumbnail, SortableQueueOrderRow, approximateMP3Bytes, builtInServiceConnection,
  button, clearedQueueItemsKey, dateLabel, durationLabel, durationMetric, errorMessage, etaLabel,
  field, inspectedVideoID, libraryLayoutKey, notificationAPI, panel, playlistEntries, primaryButton,
  qualityLabels, queueItemKey, queueItemsFor, selectablePlaylistEntries, selectedPlaylistEntries,
  statusClass, statusLabels, terminalNotification,
  type Draft, type LibraryFilter, type QueueFilter, type QueueRow, type Tab,
} from "../components/downloader/view-model";

export function HomePage() {
  const queueSensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const [tab, setTab] = useState<Tab>("queue");
  const [connection] = useState<ServiceConnection>(builtInServiceConnection);
  const [serviceReady, setServiceReady] = useState(false);
  const [jobs, setJobs] = useState<DownloadJob[]>([]);
  const [settings, setSettings] = useState<AppSettings>({
    defaultQuality: "best", maxConcurrentDownloads: 3, bandwidthLimitBytesPerSec: 0, notificationsEnabled: false, downloadLocation: "",
    namingPattern: "{channel} - {title} [{resolution}]", subfolderSorting: "channel",
    defaultCategory: "General", userCategories: ["Tech", "Science", "Coding", "Music", "Education", "Gaming", "Podcasts", "Archival", "General"],
    storageMode: "managed-published",
  });
  const knownJobStatuses = useRef<Map<string, DownloadJob["status"]>>(new Map());
  const notificationsEnabledRef = useRef(false);
  const [newCategoryInput, setNewCategoryInput] = useState("");
  const [mp3Supported, setMp3Supported] = useState(false);
  const [serviceError, setServiceError] = useState("");
  const [savingSettings, setSavingSettings] = useState(false);
  const [settingsSaved, setSettingsSaved] = useState(false);
  const [pollError, setPollError] = useState("");
  const [url, setUrl] = useState("");
  const [batchMode, setBatchMode] = useState(false);
  const [rightsConfirmed, setRightsConfirmed] = useState(false);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [inspecting, setInspecting] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const [notice, setNotice] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const [actionError, setActionError] = useState("");
  const [preview, setPreview] = useState<{ title: string; url: string; mimeType: string } | null>(null);
  const [queueFilter, setQueueFilter] = useState<QueueFilter>("all");
  const [search, setSearch] = useState("");
  const [libraryFilter, setLibraryFilter] = useState<LibraryFilter>("all");
  const [librarySearch, setLibrarySearch] = useState("");
  const [libraryCategory, setLibraryCategory] = useState("all");
  const [libraryChannel, setLibraryChannel] = useState("all");
  const [libraryLayout, setLibraryLayout] = useState<"grid" | "list">(() => {
    try { return window.localStorage.getItem(libraryLayoutKey) === "list" ? "list" : "grid"; }
    catch { return "grid"; }
  });
  const [clearedQueueItems, setClearedQueueItems] = useState<string[]>(() => {
    try {
      const stored = window.localStorage.getItem(clearedQueueItemsKey);
      const parsed: unknown = stored ? JSON.parse(stored) : [];
      return Array.isArray(parsed) && parsed.every(item => typeof item === "string") ? parsed : [];
    } catch { return []; }
  });

  const queuedJobsOrdered = useMemo(
    () => jobs.filter(job => job.status === "queued").sort((a, b) => {
      const left = a.queuePosition ?? Number.MAX_SAFE_INTEGER;
      const right = b.queuePosition ?? Number.MAX_SAFE_INTEGER;
      return left === right ? a.createdAt.localeCompare(b.createdAt) : left - right;
    }),
    [jobs],
  );
  const queueDisplayJobs = useMemo(() => {
    const rank = (job: DownloadJob) => job.status === "downloading" || job.status === "processing" ? 0 : job.status === "queued" ? 1 : job.status === "paused" ? 2 : 3;
    return [...jobs].sort((a, b) => {
      const rankDifference = rank(a) - rank(b);
      if (rankDifference) return rankDifference;
      if (a.status === "queued" && b.status === "queued") {
        return (a.queuePosition ?? Number.MAX_SAFE_INTEGER) - (b.queuePosition ?? Number.MAX_SAFE_INTEGER);
      }
      return b.createdAt.localeCompare(a.createdAt);
    });
  }, [jobs]);
  const queueRows = useMemo<QueueRow[]>(
    () => queueDisplayJobs.flatMap(job => queueItemsFor(job).map(item => ({ job, item }))),
    [queueDisplayJobs],
  );
  const visibleQueueRows = useMemo(
    () => queueRows.filter(row => row.item.status !== "completed" || !clearedQueueItems.includes(queueItemKey(row))),
    [queueRows, clearedQueueItems],
  );
  const activeCount = visibleQueueRows.filter(({ item }) => item.status === "downloading" || item.status === "processing").length;
  const queuedCount = visibleQueueRows.filter(({ item }) => item.status === "queued").length;
  const completedQueueCount = visibleQueueRows.filter(({ item }) => item.status === "completed").length;
  const finishedFiles = jobs.reduce((sum, job) => sum + job.files.length, 0);
  const totalCurrentSpeed = queueRows.reduce((sum, { item }) => sum + ((item.status === "downloading" || item.status === "processing") ? item.speedBytesPerSec : 0), 0);
  const queueJobs = useMemo(
    () => jobs.filter(job => job.status === "queued" || job.status === "downloading" || job.status === "processing" || job.status === "paused" || ((job.status === "failed" || job.status === "cancelled") && job.files.length === 0)),
    [jobs],
  );
  const libraryJobs = useMemo(
    () => jobs.filter(job => ["completed", "partial", "failed", "cancelled"].includes(job.status) && job.files.length > 0),
    [jobs],
  );
  const libraryCategories = useMemo(() => {
    const counts = new Map<string, number>();
    for (const job of libraryJobs) {
      const category = job.category || job.files.find(file => file.category)?.category || "Uncategorized";
      counts.set(category, (counts.get(category) ?? 0) + job.files.length);
    }
    return [...counts.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [libraryJobs]);
  const libraryChannels = useMemo(() => {
    const counts = new Map<string, number>();
    for (const job of libraryJobs) {
      for (const file of job.files) {
        const channel = file.author?.trim() || "Unknown channel";
        counts.set(channel, (counts.get(channel) ?? 0) + 1);
      }
    }
    return [...counts.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [libraryJobs]);
  const libraryStats = useMemo(() => libraryJobs.reduce((stats, job) => {
    for (const file of job.files) {
      stats.files += 1;
      stats.logicalBytes += file.size + (file.subtitle?.size ?? 0);
      if (file.managedAvailable !== false) stats.managedBytes += file.size;
      if (file.subtitle?.managedAvailable) stats.managedBytes += file.subtitle.size;
      if (file.publishedAvailable !== false && file.outputRelativePath) stats.publishedBytes += file.size;
      if (file.subtitle?.publishedAvailable && file.subtitle.outputRelativePath) stats.publishedBytes += file.subtitle.size;
    }
    return stats;
  }, { files: 0, logicalBytes: 0, managedBytes: 0, publishedBytes: 0 }), [libraryJobs]);
  const visibleLibraryJobs = useMemo(() => libraryJobs.filter(job => {
    const query = librarySearch.trim().toLowerCase();
    const category = job.category || job.files.find(file => file.category)?.category || "Uncategorized";
    const channels = job.files.map(file => file.author?.trim() || "Unknown channel");
    const text = `${job.title} ${job.url} ${job.category ?? ""} ${job.audioFormat ?? ""} ${job.subtitleLanguage ?? ""} ${job.files.map(file => `${file.title ?? ""} ${file.author ?? ""} ${file.category ?? ""} ${file.name} ${file.outputName ?? ""} ${file.outputRelativePath ?? ""} ${file.subtitle?.label ?? ""} ${file.subtitle?.languageCode ?? ""}`).join(" ")}`.toLowerCase();
    return (!query || text.includes(query))
      && (libraryFilter === "all" || (job.mediaType ?? "video") === libraryFilter)
      && (libraryCategory === "all" || category === libraryCategory)
      && (libraryChannel === "all" || channels.includes(libraryChannel));
  }), [libraryJobs, libraryFilter, librarySearch, libraryCategory, libraryChannel]);

  useEffect(() => {
    try { window.localStorage.setItem(libraryLayoutKey, libraryLayout); }
    catch { /* Layout persistence is optional; keep the current session state. */ }
  }, [libraryLayout]);

  useEffect(() => {
    notificationsEnabledRef.current = settings.notificationsEnabled;
  }, [settings.notificationsEnabled]);

  useEffect(() => {
    // Check the bundled Go service first, then hydrate the page from its
    // SQLite-backed job and preference APIs. Failed startup checks retry every
    // 2.5 seconds; unmounting aborts requests and cancels the pending retry.
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
        setSettings({
          ...settingResult.settings,
          maxConcurrentDownloads: settingResult.settings.maxConcurrentDownloads || 3,
          bandwidthLimitBytesPerSec: settingResult.settings.bandwidthLimitBytesPerSec ?? 0,
          notificationsEnabled: settingResult.settings.notificationsEnabled ?? false,
          namingPattern: settingResult.settings.namingPattern || "{channel} - {title} [{resolution}]",
          subfolderSorting: settingResult.settings.subfolderSorting || "channel",
          defaultCategory: settingResult.settings.defaultCategory || "General",
          userCategories: settingResult.settings.userCategories?.length ? settingResult.settings.userCategories : ["Tech", "Science", "Coding", "Music", "Education", "Gaming", "Podcasts", "Archival", "General"],
          storageMode: settingResult.settings.storageMode || "managed-published",
        });
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
    return () => { controller.abort(); clearTimeout(retryTimer); };
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
          setSettings(previous => ({
            ...previous,
            ...event.settings,
            maxConcurrentDownloads: event.settings?.maxConcurrentDownloads || 3,
            bandwidthLimitBytesPerSec: event.settings?.bandwidthLimitBytesPerSec ?? 0,
            notificationsEnabled: event.settings?.notificationsEnabled ?? false,
            namingPattern: event.settings?.namingPattern || "{channel} - {title} [{resolution}]",
            subfolderSorting: event.settings?.subfolderSorting || "channel",
            defaultCategory: event.settings?.defaultCategory || "General",
            userCategories: event.settings?.userCategories?.length ? event.settings.userCategories : previous.userCategories,
            storageMode: event.settings?.storageMode || "managed-published",
          }));
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

  async function inspectLinks() {
    setFormError("");
    setNotice("");
    setDrafts([]);
    if (!serviceReady) {
      setFormError("The built-in Go service is still starting. It will connect automatically.");
      return;
    }
    const lines = (batchMode ? url.split(/\r?\n/) : [url]).map(value => value.trim()).filter(Boolean);
    if (lines.length === 0) { setFormError("Paste a YouTube video or playlist URL first."); return; }
    if (lines.length > 20) { setFormError("Inspect up to 20 links at a time."); return; }
    let normalized: ReturnType<typeof parseYouTubeURL>[];
    try { normalized = lines.map(line => parseYouTubeURL(line)); }
    catch (error) { setFormError(errorMessage(error)); return; }

    setInspecting(true);
    try {
      const results = await Promise.all(normalized.map(async value => {
        const result = await api<Inspection>(connection, "/api/inspect", {
          method: "POST",
          body: JSON.stringify({ url: value.canonical }),
          signal: AbortSignal.timeout(30000),
        });
        const qualities = result.availableQualities ?? [];
        const selectedQuality = qualities.some(option => option.value === settings.defaultQuality)
          ? settings.defaultQuality
          : qualities[0]?.value ?? settings.defaultQuality;
        const entries = result.entries ?? result.items ?? [];
        const selectedPlaylistIndexes = result.kind === "playlist"
          ? entries.filter(item => Number.isInteger(item.index) && (item.index ?? 0) > 0 && inspectedVideoID.test(item.id)).map(item => item.index!)
          : [];
        return {
          ...result, selectedQuality, mediaType: "video" as const, audioFormat: "mp3" as const, audioBitrate: "192k",
          subtitleLanguage: "", subtitleFormat: "vtt" as const,
          category: settings.defaultCategory || settings.userCategories[0] || "General",
          selectedPlaylistIndexes, playlistExpanded: false,
        };
      }));
      setDrafts(results);
      if (results.length === 1 && results[0].kind === "video") setNotice("Video metadata and supported qualities loaded from YouTube.");
    } catch (error) { setFormError(errorMessage(error)); }
    finally { setInspecting(false); }
  }

  function applyMediaTypeToAll(mediaType: Draft["mediaType"]) {
    setDrafts(previous => previous.map(item => {
      if (mediaType === "audio" && !item.audioOnlyAvailable) return item;
      return { ...item, mediaType, ...(mediaType === "audio" && !mp3Supported && item.audioFormat === "mp3" ? { audioFormat: "m4a" as const } : {}) };
    }));
  }

  function applyQualityToAll(quality: Quality) {
    setDrafts(previous => previous.map(item => {
      if (item.mediaType !== "video") return item;
      const supported = item.kind === "playlist" || !item.availableQualities?.length || item.availableQualities.some(option => option.value === quality);
      return supported ? { ...item, selectedQuality: quality } : item;
    }));
  }

  function applyAudioFormatToAll(audioFormat: Draft["audioFormat"]) {
    setDrafts(previous => previous.map(item =>
      item.mediaType === "audio" && item.audioOnlyAvailable && (audioFormat !== "mp3" || mp3Supported)
        ? { ...item, audioFormat }
        : item,
    ));
  }

  function applyAudioBitrateToAll(audioBitrate: string) {
    setDrafts(previous => previous.map(item => item.mediaType === "audio" && item.audioFormat === "mp3" ? { ...item, audioBitrate } : item));
  }


  function applyCategoryToAll(category: string) {
    setDrafts(previous => previous.map(item => ({ ...item, category })));
  }

  function updatePlaylistSelection(draftIndex: number, indexes: number[]) {
    const unique = [...new Set(indexes)].sort((a, b) => a - b);
    setDrafts(previous => previous.map((item, itemIndex) =>
      itemIndex === draftIndex ? { ...item, selectedPlaylistIndexes: unique } : item,
    ));
  }

  function togglePlaylistEntry(draftIndex: number, playlistIndex: number, selected: boolean) {
    const current = drafts[draftIndex];
    if (!current) return;
    const next = selected
      ? [...current.selectedPlaylistIndexes, playlistIndex]
      : current.selectedPlaylistIndexes.filter(index => index !== playlistIndex);
    updatePlaylistSelection(draftIndex, next);
  }

  async function addDownloads(event: React.FormEvent) {
    event.preventDefault();
    setFormError("");
    setNotice("");
    if (!serviceReady) { setFormError("The built-in Go service is still starting. It will connect automatically."); return; }
    if (!rightsConfirmed) { setFormError("Confirm you have permission to download this content."); return; }
    if (drafts.length === 0) { setFormError("Inspect at least one link before adding it to the queue."); return; }
    if (drafts.some(draft => draft.kind === "playlist" && draft.selectedPlaylistIndexes.length === 0)) {
      setFormError("Select at least one playlist item before adding the playlist to the queue.");
      return;
    }
    setSubmitting(true);
    const added: DownloadJob[] = [];
    try {
      for (const draft of drafts) {
        const job = await api<DownloadJob>(connection, "/api/jobs", {
          method: "POST",
          body: JSON.stringify({
            url: draft.url, quality: draft.selectedQuality, mediaType: draft.mediaType,
            ...(draft.mediaType === "audio" ? { audioFormat: draft.audioFormat } : {}),
            ...(draft.mediaType === "audio" && draft.audioFormat === "mp3" ? { audioBitrate: draft.audioBitrate } : {}),
            ...(draft.subtitleLanguage ? { subtitleLanguage: draft.subtitleLanguage, subtitleFormat: draft.subtitleFormat } : {}),
            category: draft.category, rightsConfirmed: true,
            ...(draft.kind === "playlist" ? { items: selectedPlaylistEntries(draft) } : {}),
          }),
          signal: AbortSignal.timeout(15000),
        });
        added.push(job);
      }
      setJobs(previous => [...added, ...previous.filter(item => !added.some(value => value.id === item.id))]);
      setNotice(added.length === 1 && added[0].kind === "playlist"
        ? `Playlist added with ${drafts[0].selectedPlaylistIndexes.length} selected item${drafts[0].selectedPlaylistIndexes.length === 1 ? "" : "s"}; original playlist positions are preserved.`
        : `${added.length} download${added.length === 1 ? "" : "s"} added to the queue.`);
      setUrl("");
      setDrafts([]);
      setRightsConfirmed(false);
      setBatchMode(false);
      setTab("queue");
    } catch (error) {
      if (added.length) setJobs(previous => [...added, ...previous]);
      setFormError(added.length ? `${errorMessage(error)} ${added.length} earlier link(s) were queued successfully.` : errorMessage(error));
    } finally { setSubmitting(false); }
  }

  async function selectDownloadFolder() {
    if (!serviceReady) return;
    setServiceError("");
    try {
      const result = await api<{ path: string } | undefined>(connection, "/api/folders/select", {
        method: "POST", body: "{}", signal: AbortSignal.timeout(5 * 60 * 1000),
      });
      if (!result?.path) return;
      changeSetting("downloadLocation", result.path);
      setNotice("Download folder selected. Save preferences to apply it to newly queued jobs.");
    } catch (error) {
      setServiceError(errorMessage(error));
    }
  }

  async function savePreferences(event: React.FormEvent) {
    event.preventDefault();
    if (!serviceReady) { setServiceError("The built-in Go service is still starting. It will connect automatically."); return; }
    setSavingSettings(true);
    setServiceError("");
    setSettingsSaved(false);
    try {
      const result = await api<{ settings: AppSettings }>(connection, "/api/settings", {
        method: "PUT", body: JSON.stringify(settings), signal: AbortSignal.timeout(10000),
      });
      setSettings(result.settings);
      setSettingsSaved(true);
    } catch (error) { setServiceError(errorMessage(error)); }
    finally { setSavingSettings(false); }
  }

  function changeSetting<K extends keyof AppSettings>(key: K, value: AppSettings[K]) {
    setSettings(previous => ({ ...previous, [key]: value }));
    setSettingsSaved(false);
  }

  async function toggleNotifications() {
    if (settings.notificationsEnabled) {
      changeSetting("notificationsEnabled", false);
      return;
    }
    const NotificationAPI = notificationAPI();
    if (!NotificationAPI) {
      setServiceError("System notifications are not supported by this browser.");
      return;
    }
    let permission = NotificationAPI.permission;
    if (permission === "default") {
      try { permission = await NotificationAPI.requestPermission(); }
      catch { permission = "denied"; }
    }
    if (permission !== "granted") {
      setServiceError("Notification permission was not granted. Enable it in your browser/OS settings to use system notifications.");
      return;
    }
    setServiceError("");
    changeSetting("notificationsEnabled", true);
    setNotice("System notifications enabled. Save preferences to keep this setting.");
  }

  function addCategory() {
    const category = newCategoryInput.trim();
    if (!category || settings.userCategories.some(value => value.toLowerCase() === category.toLowerCase())) return;
    if (settings.userCategories.length >= 50) { setServiceError("You can save up to 50 categories."); return; }
    changeSetting("userCategories", [...settings.userCategories, category]);
    setNewCategoryInput("");
  }

  function removeCategory(category: string) {
    if (settings.userCategories.length <= 1) return;
    const remaining = settings.userCategories.filter(value => value !== category);
    setSettings(previous => ({
      ...previous,
      userCategories: remaining,
      defaultCategory: previous.defaultCategory === category ? (remaining[0] ?? "General") : previous.defaultCategory,
    }));
    setSettingsSaved(false);
  }

  async function jobAction(job: DownloadJob, action: "pause" | "resume" | "cancel" | "retry" | "remove" | "delete-managed" | "delete-published" | "delete-all") {
    if (!serviceReady) return;
    if (action === "retry" && !window.confirm("Retry this URL? Confirm that you own the content or have permission to download it.")) return;
    if (action === "remove" && !window.confirm("Remove this item from the Library? The app-managed copy and history will be removed, but published files in your configured download folder will be preserved.")) return;
    if (action === "delete-managed" && !window.confirm("Delete the app-managed media copy? Published files in your configured download folder will be preserved, but in-app Save links will no longer work.")) return;
    if (action === "delete-published" && !window.confirm("Delete the published media from your configured download folder? The app-managed Library copy will be preserved.")) return;
    if (action === "delete-all" && !window.confirm("Delete this media everywhere? This removes the app-managed copy, published output files, and Library history. This cannot be undone.")) return;
    setBusyAction(job.id);
    setActionError("");
    try {
      if (action === "remove" || action === "delete-all") {
        const suffix = action === "delete-all" ? "/all" : "";
        await api<void>(connection, `/api/jobs/${encodeURIComponent(job.id)}${suffix}`, { method: "DELETE", signal: AbortSignal.timeout(15000) });
        setJobs(previous => previous.filter(item => item.id !== job.id));
        setNotice(action === "delete-all"
          ? "Media, published output, and Library history deleted."
          : "Removed from Library. Published output files were preserved.");
      } else if (action === "delete-managed" || action === "delete-published") {
        const scope = action === "delete-managed" ? "managed" : "published";
        const result = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/${scope}`, {
          method: "DELETE", signal: AbortSignal.timeout(15000),
        });
        setJobs(previous => previous.map(item => item.id === job.id ? result : item));
        setNotice(action === "delete-managed"
          ? "App-managed media copies deleted. Published output files were preserved."
          : "Published output files deleted. App-managed Library copies were preserved.");
      } else {
        const result = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/${action}`, {
          method: "POST",
          ...(action === "retry" ? { body: JSON.stringify({ rightsConfirmed: true }) } : {}),
          signal: AbortSignal.timeout(15000),
        });
        setJobs(previous => action === "retry"
          ? [result, ...previous]
          : previous.map(item => item.id === job.id ? result : item));
      }
    } catch (error) { setActionError(errorMessage(error)); }
    finally { setBusyAction(""); }
  }

  async function retryPlaylistItem(job: DownloadJob, item: QueueItem) {
    if (!serviceReady || job.kind !== "playlist" || (item.status !== "failed" && item.status !== "cancelled")) return;
    const key = `${job.id}:retry-item:${item.index}`;
    setBusyAction(key);
    setActionError("");
    try {
      const updated = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/retry-item`, {
        method: "POST",
        body: JSON.stringify({ index: item.index }),
        signal: AbortSignal.timeout(15000),
      });
      setJobs(previous => previous.map(value => value.id === job.id ? updated : value));
      setNotice(`Retrying only "${item.title}". Successful playlist files are being preserved.`);
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function previewFile(job: DownloadJob, file: DownloadFile) {
    if (!serviceReady || file.managedAvailable === false) return;
    const key = `${job.id}:${file.id}:preview`;
    setBusyAction(key);
    setActionError("");
    try {
      const ticket = await api<{ path: string }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/ticket`, {
        method: "POST",
        body: JSON.stringify({ fileId: file.id, inline: true }),
        signal: AbortSignal.timeout(15000),
      });
      if (!/^\/api\/downloads\/[A-Za-z0-9_-]+$/.test(ticket.path)) throw new Error("The service returned an invalid preview link.");
      setPreview({
        title: file.title || file.outputName || file.name,
        url: `${connection.base}${ticket.path}`,
        mimeType: file.mimeType || (job.mediaType === "audio" ? "audio/mpeg" : "video/mp4"),
      });
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function saveFile(job: DownloadJob, fileId?: string) {
    if (!serviceReady) return;
    const key = `${job.id}:${fileId ?? "zip"}`;
    setBusyAction(key);
    setActionError("");
    try {
      const ticket = await api<{ path: string }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/ticket`, {
        method: "POST", body: JSON.stringify(fileId ? { fileId } : {}), signal: AbortSignal.timeout(15000),
      });
      if (!/^\/api\/downloads\/[A-Za-z0-9_-]+$/.test(ticket.path)) throw new Error("The service returned an invalid download link.");
      const anchor = document.createElement("a");
      anchor.href = `${connection.base}${ticket.path}`;
      anchor.rel = "noopener noreferrer";
      anchor.referrerPolicy = "no-referrer";
      anchor.target = "_blank";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      setNotice("Download requested. Your browser will save the file.");
    } catch (error) { setActionError(errorMessage(error)); }
    finally { setBusyAction(""); }
  }

  async function filesystemAction(job: DownloadJob, fileId: string, action: "copy-path" | "reveal" | "open-folder") {
    if (!serviceReady) return;
    const key = `${job.id}:${fileId}:${action}`;
    setBusyAction(key);
    setActionError("");
    try {
      const result = await api<{ path: string }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/filesystem`, {
        method: "POST", body: JSON.stringify({ fileId, action }), signal: AbortSignal.timeout(15000),
      });
      if (action === "copy-path") {
        try {
          await navigator.clipboard.writeText(result.path);
          setNotice("Published file path copied to the clipboard.");
        } catch {
          setNotice(`Published file path: ${result.path}`);
        }
      } else {
        setNotice(action === "reveal" ? "Opened the published file location." : "Opened the published output folder.");
      }
    } catch (error) { setActionError(errorMessage(error)); }
    finally { setBusyAction(""); }
  }

  function mergeQueuedSnapshots(updated: DownloadJob[]) {
    const byID = new Map(updated.map(job => [job.id, job]));
    setJobs(previous => previous.map(job => byID.get(job.id) ?? job));
  }

  async function reorderQueuedJobs(event: DragEndEvent) {
    if (!event.over || event.active.id === event.over.id || busyAction) return;
    const activeID = String(event.active.id).replace(/^job:/, "");
    const overID = String(event.over.id).replace(/^job:/, "");
    const oldIndex = queuedJobsOrdered.findIndex(job => job.id === activeID);
    const newIndex = queuedJobsOrdered.findIndex(job => job.id === overID);
    if (oldIndex < 0 || newIndex < 0) return;
    const reordered = arrayMove(queuedJobsOrdered, oldIndex, newIndex);
    setBusyAction("queue-order");
    setActionError("");
    try {
      const result = await api<{ jobs: DownloadJob[] }>(connection, "/api/queue/order", {
        method: "PUT", body: JSON.stringify({ jobIds: reordered.map(job => job.id) }), signal: AbortSignal.timeout(15000),
      });
      mergeQueuedSnapshots(result.jobs);
      setNotice("Queued job order updated.");
    } catch (error) { setActionError(errorMessage(error)); }
    finally { setBusyAction(""); }
  }

  async function downloadNext(job: DownloadJob) {
    if (job.status !== "queued") return;
    setBusyAction(`${job.id}:next`);
    setActionError("");
    try {
      const result = await api<{ jobs: DownloadJob[] }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/next`, {
        method: "POST", signal: AbortSignal.timeout(15000),
      });
      mergeQueuedSnapshots(result.jobs);
      setNotice(`"${job.title}" will be the next queued job to start.`);
    } catch (error) { setActionError(errorMessage(error)); }
    finally { setBusyAction(""); }
  }

  async function reorderPlaylistItems(job: DownloadJob, event: DragEndEvent) {
    if (!event.over || event.active.id === event.over.id || job.status !== "queued" || busyAction) return;
    const items = job.items ?? [];
    const activeIndex = Number(String(event.active.id).split(":").at(-1));
    const overIndex = Number(String(event.over.id).split(":").at(-1));
    const oldIndex = items.findIndex(item => item.playlistIndex === activeIndex);
    const newIndex = items.findIndex(item => item.playlistIndex === overIndex);
    if (oldIndex < 0 || newIndex < 0) return;
    const reordered = arrayMove(items, oldIndex, newIndex);
    setBusyAction(`${job.id}:items`);
    setActionError("");
    try {
      const updated = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/items`, {
        method: "PUT", body: JSON.stringify({ playlistIndexes: reordered.map(item => item.playlistIndex) }), signal: AbortSignal.timeout(15000),
      });
      setJobs(previous => previous.map(item => item.id === job.id ? updated : item));
      setNotice(`Playlist queue order updated for "${job.title}".`);
    } catch (error) { setActionError(errorMessage(error)); }
    finally { setBusyAction(""); }
  }

  async function batchAction(action: "pause" | "resume") {
    const eligible = jobs.filter(job => action === "pause"
      ? job.status === "queued" || job.status === "downloading" || job.status === "processing"
      : job.status === "paused");
    for (const job of eligible) await jobAction(job, action);
  }

  function clearCompleted() {
    const completedItems = visibleQueueRows.filter(row => row.item.status === "completed").map(queueItemKey);
    if (!serviceReady || completedItems.length === 0) return;
    const updated = [...new Set([...clearedQueueItems, ...completedItems])];
    setClearedQueueItems(updated);
    try { window.localStorage.setItem(clearedQueueItemsKey, JSON.stringify(updated)); } catch { /* Keep the current queue cleared for this session. */ }
    setNotice("Completed downloads cleared from the queue. Your files remain in the library.");
  }

  const filteredQueue = visibleQueueRows.filter(({ job, item }) => {
    const query = search.trim().toLowerCase();
    if (query && !`${item.title} ${item.author ?? ""} ${job.title} ${job.url}`.toLowerCase().includes(query)) return false;
    if (queueFilter === "active") return item.status === "downloading" || item.status === "processing";
    if (queueFilter === "queued") return item.status === "queued";
    if (queueFilter === "completed") return item.status === "completed";
    return true;
  });

  return (
    <div className="dark min-h-screen bg-[#0c0d10] text-neutral-100 selection:bg-rose-600 selection:text-white">
      <header className="sticky top-0 z-30 border-b border-neutral-800/80 bg-neutral-950/95 backdrop-blur-md">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-6">
          <div className="flex items-center gap-3">
            <div className="relative flex size-10 items-center justify-center rounded-xl bg-gradient-to-br from-rose-600 to-red-700 shadow-lg shadow-rose-950/40">
              <DownloadCloud className="size-5 text-white" aria-hidden="true" />
              {activeCount > 0 && <span className="absolute -right-1 -top-1 grid size-4 place-items-center rounded-full bg-emerald-500 text-[9px] font-bold text-black">{activeCount}</span>}
            </div>
            <div>
              <h1 className="text-base font-bold tracking-tight sm:text-lg">YouTube Downloader</h1>
              <p className="hidden text-xs text-neutral-400 sm:block">Native downloads · SQLite-backed history</p>
            </div>
          </div>

          <nav aria-label="Main navigation" className="order-3 flex w-full items-center gap-1 rounded-xl border border-neutral-800 bg-neutral-900 p-1 sm:order-none sm:w-auto">
            {([
              ["queue", Layers, "Queue"], ["library", Film, "Library"], ["settings", Settings, "Settings"],
            ] as const).map(([value, Icon, label]) => (
              <button key={value} type="button" onClick={() => setTab(value)} aria-current={tab === value ? "page" : undefined}
                className={`flex flex-1 items-center justify-center gap-2 rounded-lg px-3 py-2 text-xs font-semibold transition-colors sm:flex-none ${tab === value ? "bg-neutral-800 text-white shadow-sm" : "text-neutral-400 hover:text-neutral-200"}`}>
                <Icon className="size-3.5 text-rose-400" aria-hidden="true" />{label}
                {value === "queue" && visibleQueueRows.length > 0 && <span className="rounded-full bg-rose-600 px-1.5 text-[10px] text-white">{visibleQueueRows.length}</span>}
                {value === "library" && libraryJobs.length > 0 && <span className="rounded-full bg-neutral-700 px-1.5 text-[10px] text-neutral-200">{libraryJobs.length}</span>}
              </button>
            ))}
          </nav>

          <div className="flex items-center gap-2">
            <div className="hidden items-center gap-2 rounded-lg border border-neutral-800 bg-neutral-900/70 px-2.5 py-2 text-xs sm:flex">
              {serviceReady ? <><Activity className="size-3.5 text-emerald-400" aria-hidden="true" /><span className="text-emerald-300">Service connected</span></>
                : <><AlertCircle className="size-3.5 text-amber-400" aria-hidden="true" /><span className="text-neutral-300">{serviceError ? "Reconnecting…" : "Connecting…"}</span></>}
            </div>
            <button type="button" className={primaryButton} onClick={() => { setTab("queue"); document.getElementById("new-download")?.scrollIntoView({ behavior: "smooth", block: "start" }); }}>
              <Plus className="size-4" aria-hidden="true" /><span className="hidden sm:inline">Add links</span><span className="sm:hidden">Add</span>
            </button>
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-7xl space-y-6 px-4 py-6 sm:px-6">
        {notice && <div role="status" className="flex items-start gap-2 rounded-xl border border-emerald-800/60 bg-emerald-950/40 px-4 py-3 text-sm text-emerald-200"><Check className="mt-0.5 size-4 shrink-0" aria-hidden="true" />{notice}</div>}
        {!serviceReady && tab !== "settings" && <div role={serviceError ? "alert" : "status"} className="flex items-start gap-2 rounded-xl border border-amber-800/60 bg-amber-950/40 px-4 py-3 text-sm text-amber-200"><AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />{serviceError ? `${serviceError} The app will retry automatically.` : "Connecting to the built-in Go service…"}</div>}
        {pollError && <div role="alert" className="flex items-start gap-2 rounded-xl border border-amber-800/60 bg-amber-950/40 px-4 py-3 text-sm text-amber-200"><AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />{pollError}</div>}
        {actionError && <div role="alert" className="flex items-start gap-2 rounded-xl border border-red-900 bg-red-950/40 px-4 py-3 text-sm text-red-200"><X className="mt-0.5 size-4 shrink-0" aria-hidden="true" />{actionError}</div>}

        {tab === "queue" && <QueuePage
          url={url}
          setUrl={setUrl}
          batchMode={batchMode}
          setBatchMode={setBatchMode}
          drafts={drafts}
          setDrafts={setDrafts}
          serviceReady={serviceReady}
          inspecting={inspecting}
          inspectLinks={inspectLinks}
          formError={formError}
          mp3Supported={mp3Supported}
          settings={settings}
          applyMediaTypeToAll={applyMediaTypeToAll}
          applyQualityToAll={applyQualityToAll}
          applyAudioFormatToAll={applyAudioFormatToAll}
          applyAudioBitrateToAll={applyAudioBitrateToAll}
          applyCategoryToAll={applyCategoryToAll}
          updatePlaylistSelection={updatePlaylistSelection}
          togglePlaylistEntry={togglePlaylistEntry}
          rightsConfirmed={rightsConfirmed}
          setRightsConfirmed={setRightsConfirmed}
          submitting={submitting}
          addDownloads={addDownloads}
          visibleQueueRows={visibleQueueRows}
          activeCount={activeCount}
          totalCurrentSpeed={totalCurrentSpeed}
          completedQueueCount={completedQueueCount}
          queuedCount={queuedCount}
          jobs={jobs}
          batchAction={batchAction}
          clearCompleted={clearCompleted}
          queuedJobsOrdered={queuedJobsOrdered}
          reorderQueuedJobs={reorderQueuedJobs}
          reorderPlaylistItems={reorderPlaylistItems}
          busyAction={busyAction}
          downloadNext={downloadNext}
          queueFilter={queueFilter}
          setQueueFilter={setQueueFilter}
          search={search}
          setSearch={setSearch}
          filteredQueue={filteredQueue}
          retryPlaylistItem={retryPlaylistItem}
          jobAction={jobAction}
        />}

        {tab === "library" && <LibraryPage
          visibleLibraryJobs={visibleLibraryJobs}
          librarySearch={librarySearch}
          setLibrarySearch={setLibrarySearch}
          libraryFilter={libraryFilter}
          setLibraryFilter={setLibraryFilter}
          libraryCategory={libraryCategory}
          setLibraryCategory={setLibraryCategory}
          libraryChannel={libraryChannel}
          setLibraryChannel={setLibraryChannel}
          libraryCategories={libraryCategories}
          libraryChannels={libraryChannels}
          libraryLayout={libraryLayout}
          setLibraryLayout={setLibraryLayout}
          libraryStats={libraryStats}
          connection={connection}
          busyAction={busyAction}
          previewFile={previewFile}
          saveFile={saveFile}
          filesystemAction={filesystemAction}
          jobAction={jobAction}
        />}

        {tab === "settings" && <SettingsPage
          connection={connection}
          serviceReady={serviceReady}
          serviceError={serviceError}
          settings={settings}
          savingSettings={savingSettings}
          settingsSaved={settingsSaved}
          mp3Supported={mp3Supported}
          newCategoryInput={newCategoryInput}
          setNewCategoryInput={setNewCategoryInput}
          savePreferences={savePreferences}
          selectDownloadFolder={selectDownloadFolder}
          changeSetting={changeSetting}
          addCategory={addCategory}
          removeCategory={removeCategory}
          toggleNotifications={toggleNotifications}
        />}
      </main>
      {preview && <div role="dialog" aria-modal="true" aria-labelledby="media-preview-title" className="fixed inset-0 z-50 grid place-items-center bg-black/80 p-4" onMouseDown={event => { if (event.target === event.currentTarget) setPreview(null); }}>
        <div className="w-full max-w-4xl overflow-hidden rounded-2xl border border-neutral-700 bg-neutral-950 shadow-2xl">
          <div className="flex items-center justify-between gap-3 border-b border-neutral-800 px-4 py-3"><div className="min-w-0"><p className="text-[10px] font-bold uppercase tracking-wider text-rose-400">Local preview</p><h2 id="media-preview-title" className="truncate text-sm font-semibold text-white">{preview.title}</h2></div><button type="button" className={button} onClick={() => setPreview(null)} aria-label="Close media preview"><X className="size-4" aria-hidden="true" />Close</button></div>
          <div className="bg-black p-3 sm:p-5">
            {preview.mimeType.startsWith("audio/") ? <audio controls preload="metadata" src={preview.url} className="w-full">Your browser cannot play this audio format.</audio>
              : <video controls preload="metadata" src={preview.url} className="max-h-[70vh] w-full rounded-lg bg-black">Your browser cannot play this video format.</video>}
            <p className="mt-3 text-[10px] text-neutral-500">Preview uses a short-lived file-scoped ticket with HTTP Range support. Playback never starts automatically.</p>
          </div>
        </div>
      </div>}
      <footer className="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 pb-8 text-[10px] text-neutral-600 sm:px-6"><span className="flex items-center gap-1.5"><Clock3 className="size-3" aria-hidden="true" />Live job updates from the Go service</span><span className="flex items-center gap-1.5"><HardDrive className="size-3" aria-hidden="true" />SQLite-backed history</span></footer>
    </div>
  );
}
