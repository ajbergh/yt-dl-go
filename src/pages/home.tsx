/**
 * Production downloader screen. The bundled page connects to the Go API served
 * by the same executable, then uses that API for jobs and SQLite preferences.
 */
import { useEffect, useMemo, useState } from "react";
import {
  Activity, AlertCircle, ArrowDownToLine, Check, ChevronDown, CircleHelp, Clock3,
  DownloadCloud, FileText, Film, Folder, FolderTree, Gauge, HardDrive, Layers, ListVideo, LoaderCircle,
  Pause, Play, Plus, RefreshCw, Search, Settings, ShieldCheck, Sparkles, Trash2, X,
} from "lucide-react";
import {
  api, apiBlob, formatBytes, isActive, parseYouTubeURL,
  type AppSettings, type DownloadFile, type DownloadJob, type Inspection, type QueueItem, type Quality,
  type ServiceConnection, type ServiceHealth,
} from "../lib/downloader";

type Tab = "queue" | "library" | "settings";
type Draft = Inspection & {
  selectedQuality: Quality;
  mediaType: "video" | "audio";
  audioFormat: "mp3" | "m4a";
  audioBitrate: string;
  category: string;
  selectedPlaylistIndexes: number[];
  playlistExpanded: boolean;
};
type QueueFilter = "all" | "active" | "queued" | "completed";
type LibraryFilter = "all" | "video" | "audio";
type QueueRow = { job: DownloadJob; item: QueueItem };
const clearedQueueItemsKey = "yt-dl-go:cleared-completed-queue-items";
const libraryLayoutKey = "yt-dl-go:library-layout";

function queueItemKey({ job, item }: QueueRow): string {
  return `${job.id}:${item.index}`;
}

const qualityLabels: Record<Quality, string> = {
  best: "Best available",
  "1080": "Up to 1080p",
  "720": "Up to 720p",
  "480": "Up to 480p",
};
const statusLabels: Record<DownloadJob["status"], string> = {
  queued: "Queued",
  downloading: "Downloading",
  processing: "Converting to MP3",
  paused: "Paused",
  completed: "Completed",
  partial: "Partial",
  failed: "Failed",
  cancelled: "Cancelled",
};
const panel = "rounded-2xl border border-neutral-800 bg-neutral-900/70";
const button = "inline-flex min-h-9 items-center justify-center gap-2 rounded-lg border border-neutral-700 bg-neutral-800 px-3 py-2 text-xs font-semibold text-neutral-200 transition-colors hover:border-neutral-600 hover:bg-neutral-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-rose-500 disabled:cursor-not-allowed disabled:opacity-50";
const primaryButton = "inline-flex min-h-10 items-center justify-center gap-2 rounded-lg bg-rose-600 px-4 py-2 text-xs font-bold text-white transition-colors hover:bg-rose-500 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-rose-400 disabled:cursor-not-allowed disabled:opacity-50";
const field = "w-full rounded-xl border border-neutral-700 bg-neutral-950 px-3.5 py-2.5 text-sm text-neutral-100 outline-none placeholder:text-neutral-500 focus:border-rose-500 focus:ring-1 focus:ring-rose-500";

function errorMessage(error: unknown): string {
  return error instanceof TypeError
    ? "Could not reach the built-in Go service. Retrying automatically."
    : error instanceof Error ? error.message : "Something went wrong. Please try again.";
}

function durationLabel(seconds?: number): string {
  if (!seconds || seconds < 0) return "Duration unavailable";
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainder = Math.floor(seconds % 60);
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(remainder).padStart(2, "0")}`
    : `${minutes}:${String(remainder).padStart(2, "0")}`;
}

function dateLabel(value?: string): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleDateString();
}

const inspectedVideoID = /^[A-Za-z0-9_-]{11}$/;

function playlistEntries(draft: Draft) {
  return draft.entries ?? draft.items ?? [];
}

function selectablePlaylistEntries(draft: Draft) {
  return playlistEntries(draft).filter(item =>
    Number.isInteger(item.index) && (item.index ?? 0) > 0 && inspectedVideoID.test(item.id),
  );
}

function selectedPlaylistEntries(draft: Draft) {
  const selected = new Set(draft.selectedPlaylistIndexes);
  return selectablePlaylistEntries(draft).filter(item => selected.has(item.index!));
}

function approximateMP3Bytes(draft: Draft): number {
  if (draft.kind !== "playlist" || draft.mediaType !== "audio" || draft.audioFormat !== "mp3") return 0;
  const bitrate = Number.parseInt(draft.audioBitrate, 10);
  if (!Number.isFinite(bitrate) || bitrate <= 0) return 0;
  const seconds = selectedPlaylistEntries(draft).reduce((sum, item) => sum + Math.max(0, item.durationSeconds ?? 0), 0);
  return Math.round(seconds * bitrate * 1000 / 8);
}

function statusClass(status: DownloadJob["status"]): string {
  if (status === "completed") return "border-emerald-700/60 bg-emerald-950/50 text-emerald-300";
  if (status === "downloading" || status === "processing") return "border-rose-700/60 bg-rose-950/50 text-rose-300";
  if (status === "paused") return "border-amber-700/60 bg-amber-950/50 text-amber-300";
  if (status === "failed" || status === "partial") return "border-red-800/60 bg-red-950/40 text-red-300";
  return "border-neutral-700 bg-neutral-800 text-neutral-300";
}

function LibraryThumbnail({ connection, jobId, file, audio }: { connection: ServiceConnection; jobId: string; file: DownloadFile; audio: boolean }) {
  const [source, setSource] = useState(file.thumbnailUrl ?? "");

  useEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    const fallback = file.thumbnailUrl ?? "";
    setSource(fallback);
    if (file.thumbnailLocalAvailable) {
      void apiBlob(connection, `/api/jobs/${encodeURIComponent(jobId)}/thumbnail?fileId=${encodeURIComponent(file.id)}`, {
        signal: controller.signal,
      }).then(blob => {
        if (controller.signal.aborted) return;
        objectURL = URL.createObjectURL(blob);
        setSource(objectURL);
      }).catch(() => {
        if (!controller.signal.aborted) setSource(fallback);
      });
    }
    return () => {
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [connection.base, connection.token, file.id, file.thumbnailLocalAvailable, file.thumbnailUrl, jobId]);

  return source
    ? <img src={source} alt="" referrerPolicy="no-referrer" data-local-thumbnail={file.thumbnailLocalAvailable ? "preferred" : undefined} className="aspect-video w-24 shrink-0 rounded-md bg-neutral-900 object-cover" />
    : <div className="grid aspect-video w-24 shrink-0 place-items-center rounded-md bg-neutral-900 text-neutral-600">{audio ? <Activity className="size-5" aria-hidden="true" /> : <Film className="size-5" aria-hidden="true" />}</div>;
}

function durationMetric(job: DownloadJob): string {
  if (job.totalCount === null) return `${job.completedCount} finished`;
  return `${job.completedCount} / ${job.totalCount} files`;
}

function queueItemsFor(job: DownloadJob): QueueItem[] {
  if (job.items?.length) return job.items;
  if (job.kind === "playlist" && job.files.length) {
    return job.files.map((file, index) => {
      const playlistIndex = Number(/^([0-9]+)-/.exec(file.name)?.[1]) || index + 1;
      return {
      index: index + 1,
      playlistIndex,
      videoId: "",
      title: file.title || file.name,
      author: file.author,
      durationSeconds: file.durationSeconds,
      thumbnailUrl: file.thumbnailUrl,
      status: "completed",
      progress: 100,
      downloadedBytes: file.size,
      totalBytes: file.size,
      speedBytesPerSec: 0,
      etaSeconds: 0,
      fileId: file.id,
    };
    });
  }
  return [{
    index: 1,
    title: job.currentItem || job.title,
    status: job.status,
    progress: job.progress,
    downloadedBytes: job.downloadedBytes,
    totalBytes: job.totalBytes,
    speedBytesPerSec: job.speedBytesPerSec,
    etaSeconds: job.etaSeconds,
    error: job.error,
    fileId: job.files.length === 1 ? job.files[0].id : undefined,
  }];
}

function etaLabel(seconds: number): string {
  if (seconds <= 0) return "Estimating…";
  const minutes = Math.floor(seconds / 60);
  return minutes > 0 ? `${minutes}m ${seconds % 60}s left` : `${seconds}s left`;
}

function builtInServiceConnection(): ServiceConnection {
  // `npm run dev` fixes Vite at port 5173 and runs Go separately at 8080.
  // The packaged executable serves both UI and API from the current origin.
  // Vite preview/custom ports are not a supported connection mode.
  const base = window.location.port === "5173"
    ? "http://127.0.0.1:8080"
    : window.location.origin;
  return { base, token: "" };
}

export function HomePage() {
  const [tab, setTab] = useState<Tab>("queue");
  const [connection] = useState<ServiceConnection>(builtInServiceConnection);
  const [serviceReady, setServiceReady] = useState(false);
  const [jobs, setJobs] = useState<DownloadJob[]>([]);
  const [settings, setSettings] = useState<AppSettings>({
    defaultQuality: "best", maxConcurrentDownloads: 3, downloadLocation: "",
    namingPattern: "{channel} - {title} [{resolution}]", subfolderSorting: "channel",
    defaultCategory: "General", userCategories: ["Tech", "Science", "Coding", "Music", "Education", "Gaming", "Podcasts", "Archival", "General"],
    storageMode: "managed-published",
  });
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

  const queueRows = useMemo<QueueRow[]>(
    () => jobs.flatMap(job => queueItemsFor(job).map(item => ({ job, item }))),
    [jobs],
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
      stats.logicalBytes += file.size;
      if (file.managedAvailable !== false) stats.managedBytes += file.size;
      if (file.publishedAvailable !== false && file.outputRelativePath) stats.publishedBytes += file.size;
    }
    return stats;
  }, { files: 0, logicalBytes: 0, managedBytes: 0, publishedBytes: 0 }), [libraryJobs]);
  const visibleLibraryJobs = useMemo(() => libraryJobs.filter(job => {
    const query = librarySearch.trim().toLowerCase();
    const category = job.category || job.files.find(file => file.category)?.category || "Uncategorized";
    const channels = job.files.map(file => file.author?.trim() || "Unknown channel");
    const text = `${job.title} ${job.url} ${job.category ?? ""} ${job.audioFormat ?? ""} ${job.files.map(file => `${file.title ?? ""} ${file.author ?? ""} ${file.category ?? ""} ${file.name} ${file.outputName ?? ""} ${file.outputRelativePath ?? ""}`).join(" ")}`.toLowerCase();
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
        setSettings({
          ...settingResult.settings,
          maxConcurrentDownloads: settingResult.settings.maxConcurrentDownloads || 3,
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
    // Refresh jobs immediately and then about every 1.8 seconds while mounted.
    // A failed poll is shown without stopping later polling attempts.
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const result = await api<{ jobs: DownloadJob[] }>(connection, "/api/jobs", {
          signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        });
        if (!controller.signal.aborted) {
          setJobs(result.jobs);
          setPollError("");
        }
      } catch (error) {
        if (!controller.signal.aborted) setPollError(errorMessage(error));
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(refresh, 1800);
      }
    }
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
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

        {tab === "queue" && (
          <>
            <section id="new-download" className={`${panel} scroll-mt-24 overflow-hidden`} aria-labelledby="new-download-heading">
              <div className="grid md:grid-cols-[4.5rem_1fr]">
                <div className="hidden items-start justify-center bg-rose-600 py-8 text-white md:flex"><DownloadCloud className="size-6" aria-hidden="true" /></div>
                <form onSubmit={addDownloads} className="min-w-0 p-5 sm:p-7">
                  <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <p className="mb-1 flex items-center gap-2 text-[11px] font-bold uppercase tracking-[0.16em] text-rose-400"><Plus className="size-3.5" aria-hidden="true" />Add links & batch process</p>
                      <h2 id="new-download-heading" className="text-2xl font-bold tracking-tight">New download</h2>
                      <p className="mt-1 text-xs text-neutral-400">Inspect real YouTube metadata and supported streams before adding jobs.</p>
                    </div>
                    <div className="flex items-center rounded-lg border border-neutral-800 bg-neutral-950 p-1 text-xs">
                      <button type="button" onClick={() => { setBatchMode(false); setDrafts([]); }} className={`rounded-md px-3 py-1.5 font-semibold ${!batchMode ? "bg-neutral-800 text-white" : "text-neutral-400 hover:text-white"}`}>Single link</button>
                      <button type="button" onClick={() => { setBatchMode(true); setDrafts([]); }} className={`rounded-md px-3 py-1.5 font-semibold ${batchMode ? "bg-neutral-800 text-white" : "text-neutral-400 hover:text-white"}`}>Batch URLs</button>
                    </div>
                  </div>

                  <div className="grid items-start gap-3 sm:grid-cols-[1fr_auto]">
                    <label className="min-w-0 text-xs font-semibold text-neutral-300" htmlFor="video-url">YouTube video or playlist URL{batchMode ? "s" : ""}
                      {batchMode ? <textarea id="video-url" rows={3} value={url} onChange={event => { setUrl(event.target.value); setFormError(""); }} placeholder="One HTTPS YouTube URL per line" autoComplete="off" spellCheck={false} className={`${field} mt-2 resize-y font-mono text-xs`} />
                        : <input id="video-url" type="url" value={url} onChange={event => { setUrl(event.target.value); setFormError(""); }} placeholder="https://www.youtube.com/watch?v=..." autoComplete="off" spellCheck={false} className={`${field} mt-2 min-h-12`} />}
                    </label>
                    <button type="button" onClick={() => void inspectLinks()} disabled={!serviceReady || inspecting || !url.trim()} className={`${button} mt-5 min-h-12 px-4`}>
                      {inspecting ? <LoaderCircle className="size-4 animate-spin" aria-hidden="true" /> : <Search className="size-4 text-rose-400" aria-hidden="true" />}
                      {inspecting ? "Inspecting…" : "Inspect qualities"}
                    </button>
                  </div>
                  <p className="mt-2 flex items-start gap-1.5 text-[11px] text-neutral-500"><CircleHelp className="mt-0.5 size-3 shrink-0" aria-hidden="true" />Playlists are processed in order. Hidden or inaccessible entries cannot be independently verified.</p>

                  {!serviceReady && <div className="mt-4 rounded-xl border border-amber-800/60 bg-amber-950/30 p-3 text-xs text-amber-200">The app is connecting to its built-in Go service. You can queue downloads as soon as startup completes.</div>}
                  {formError && <p role="alert" className="mt-4 text-sm text-red-300">{formError}</p>}

                  {drafts.length > 0 && <div className="mt-5 space-y-3 border-t border-neutral-800 pt-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div><h3 className="text-xs font-bold uppercase tracking-wider text-neutral-300">Inspection results</h3><p className="mt-1 text-[11px] text-neutral-500">Quality options and metadata are returned by the Go backend; no media URLs are exposed.</p></div>
                      <span className="rounded-full bg-neutral-800 px-2 py-1 text-[10px] text-neutral-300">{drafts.length} item{drafts.length === 1 ? "" : "s"}</span>
                    </div>
                    {drafts.length > 1 && <div className="rounded-xl border border-neutral-800 bg-neutral-900/70 p-3">
                      <div className="mb-2 flex items-center justify-between gap-2"><div><p className="text-[11px] font-bold text-neutral-200">Apply to all inspected items</p><p className="mt-0.5 text-[10px] text-neutral-500">Batch values update eligible items now; each card can still be overridden afterward.</p></div><Layers className="size-4 text-rose-400" aria-hidden="true" /></div>
                      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
                        <select aria-label="Apply media type to all" defaultValue="" onChange={event => { if (event.target.value) applyMediaTypeToAll(event.target.value as Draft["mediaType"]); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Media type…</option><option value="video">All video</option><option value="audio">All eligible audio</option>
                        </select>
                        <select aria-label="Apply quality to all" defaultValue="" onChange={event => { if (event.target.value) applyQualityToAll(event.target.value as Quality); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Video quality…</option>{(Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                        </select>
                        <select aria-label="Apply audio format to all" defaultValue="" onChange={event => { if (event.target.value) applyAudioFormatToAll(event.target.value as Draft["audioFormat"]); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Audio format…</option><option value="mp3" disabled={!mp3Supported}>MP3</option><option value="m4a">M4A · original AAC</option>
                        </select>
                        <select aria-label="Apply MP3 bitrate to all" defaultValue="" onChange={event => { if (event.target.value) applyAudioBitrateToAll(event.target.value); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>MP3 bitrate…</option>{["128k", "192k", "256k", "320k"].map(value => <option key={value} value={value}>{value}</option>)}
                        </select>
                        <select aria-label="Apply category to all" defaultValue="" onChange={event => { if (event.target.value) applyCategoryToAll(event.target.value); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Category…</option>{settings.userCategories.map(category => <option key={category} value={category}>{category}</option>)}
                        </select>
                      </div>
                    </div>}
                    {drafts.map((draft, index) => <article key={`${draft.url}-${index}`} className="grid gap-3 rounded-xl border border-neutral-800 bg-neutral-950/60 p-3 sm:grid-cols-[8rem_1fr_12rem] sm:items-center">
                      {draft.thumbnailUrl ? <img src={draft.thumbnailUrl} alt="" referrerPolicy="no-referrer" className="aspect-video w-full rounded-lg bg-neutral-900 object-cover sm:w-32" />
                        : <div className="grid aspect-video w-full place-items-center rounded-lg bg-neutral-900 text-neutral-600 sm:w-32">{draft.kind === "playlist" ? <ListVideo className="size-7" aria-hidden="true" /> : <Film className="size-7" aria-hidden="true" />}</div>}
                      <div className="min-w-0">
                        <div className="mb-1 flex flex-wrap items-center gap-1.5 text-[10px] text-neutral-400"><span className="rounded bg-neutral-800 px-1.5 py-0.5 uppercase">{draft.kind}</span>{draft.author && <span>{draft.author}</span>}{draft.publishDate && <span>· {dateLabel(draft.publishDate)}</span>}</div>
                        <h4 className="break-words text-sm font-semibold text-white">{draft.title || "Untitled YouTube media"}</h4>
                        <p className="mt-1 text-[11px] text-neutral-500">{draft.kind === "playlist" ? `${draft.itemCount ?? 0} exposed entries` : durationLabel(draft.durationSeconds)}</p>
                        {draft.note && <p className="mt-1 text-[10px] leading-relaxed text-amber-300/80">{draft.note}</p>}
                      </div>
                      <div className="space-y-2">
                        <label className="block text-[11px] font-medium text-neutral-400">Download as
                          <select aria-label={`Media type for ${draft.title || `item ${index + 1}`}`} value={draft.mediaType} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, mediaType: event.target.value as Draft["mediaType"], ...(event.target.value === "audio" && !mp3Supported ? { audioFormat: "m4a" as const } : {}) } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            <option value="video">Video</option>
                            <option value="audio" disabled={!draft.audioOnlyAvailable}>Audio only{!draft.audioOnlyAvailable ? " (unavailable)" : ""}</option>
                          </select>
                        </label>
                        {draft.mediaType === "audio" ? <><label className="block text-[11px] font-medium text-neutral-400">Audio format
                          <select aria-label={`Audio format for ${draft.title || `item ${index + 1}`}`} value={draft.audioFormat} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, audioFormat: event.target.value as Draft["audioFormat"] } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            <option value="mp3" disabled={!mp3Supported}>MP3 · tagged</option><option value="m4a">M4A · original AAC</option>
                          </select>
                        </label>{draft.audioFormat === "mp3" && <label className="block text-[11px] font-medium text-neutral-400">MP3 bitrate
                          <select aria-label={`MP3 bitrate for ${draft.title || `item ${index + 1}`}`} value={draft.audioBitrate} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, audioBitrate: event.target.value } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            {["128k", "192k", "256k", "320k"].map(value => <option key={value} value={value}>{value}</option>)}
                          </select>
                        </label>}</> : <label className="block text-[11px] font-medium text-neutral-400">Maximum quality
                          <select aria-label={`Quality for ${draft.title || `item ${index + 1}`}`} value={draft.selectedQuality} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, selectedQuality: event.target.value as Quality } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            {(draft.availableQualities ?? (draft.kind === "playlist"
                              ? (Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => ({ value, label, height: 0 }))
                              : [{ value: "best" as const, label: "Best available", height: 0 }])).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                          </select>
                        </label>}
                        <label className="block text-[11px] font-medium text-neutral-400">Category
                          <select aria-label={`Category for ${draft.title || `item ${index + 1}`}`} value={draft.category} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, category: event.target.value } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            {settings.userCategories.map(category => <option key={category} value={category}>{category}</option>)}
                          </select>
                        </label>
                      </div>
                      {draft.kind === "playlist" && <div className="space-y-2 border-t border-neutral-800 pt-3 sm:col-span-3">
                        {(() => {
                          const selectable = selectablePlaylistEntries(draft);
                          const selected = selectedPlaylistEntries(draft);
                          const estimate = approximateMP3Bytes(draft);
                          return <>
                            <div className="flex flex-wrap items-center justify-between gap-2">
                              <div>
                                <p className="text-[11px] font-semibold text-neutral-200">{selected.length} of {selectable.length} selectable playlist items selected</p>
                                <p className="mt-0.5 text-[10px] text-neutral-500">{draft.itemCount ?? playlistEntries(draft).length} entries exposed by YouTube{estimate > 0 ? ` · Approx. MP3 output ${formatBytes(estimate)}` : ""}</p>
                              </div>
                              <div className="flex flex-wrap gap-1.5">
                                <button type="button" className={button} onClick={() => updatePlaylistSelection(index, selectable.map(item => item.index!))}>Select all</button>
                                <button type="button" className={button} onClick={() => updatePlaylistSelection(index, [])}>Clear all</button>
                                <button type="button" className={button} aria-expanded={draft.playlistExpanded} onClick={() => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, playlistExpanded: !item.playlistExpanded } : item))}>{draft.playlistExpanded ? "Hide items" : "Choose items"}</button>
                              </div>
                            </div>
                            {draft.playlistExpanded && <div className="max-h-72 space-y-1 overflow-y-auto rounded-lg border border-neutral-800 bg-neutral-950 p-2">
                              {playlistEntries(draft).map((entry, entryOffset) => {
                                const originalIndex = entry.index ?? entryOffset + 1;
                                const selectableEntry = Number.isInteger(entry.index) && originalIndex > 0 && inspectedVideoID.test(entry.id);
                                const checked = selectableEntry && draft.selectedPlaylistIndexes.includes(originalIndex);
                                return <label key={`${entry.id || "unavailable"}-${originalIndex}`} className={`flex items-center gap-3 rounded-md px-2 py-2 ${selectableEntry ? "cursor-pointer hover:bg-neutral-900" : "cursor-not-allowed opacity-50"}`}>
                                  <input type="checkbox" aria-label={`Select playlist item ${originalIndex}`} checked={checked} disabled={!selectableEntry} onChange={event => togglePlaylistEntry(index, originalIndex, event.target.checked)} className="size-4 shrink-0 accent-rose-600" />
                                  {entry.thumbnailUrl ? <img src={entry.thumbnailUrl} alt="" referrerPolicy="no-referrer" className="aspect-video w-16 shrink-0 rounded bg-neutral-900 object-cover" /> : <div className="grid aspect-video w-16 shrink-0 place-items-center rounded bg-neutral-900 text-neutral-600"><Film className="size-3.5" aria-hidden="true" /></div>}
                                  <span className="min-w-0 flex-1"><span className="block truncate text-[11px] font-medium text-neutral-200">#{originalIndex} · {entry.title || "Unavailable playlist item"}</span><span className="mt-0.5 block truncate text-[10px] text-neutral-500">{entry.author || (selectableEntry ? "YouTube" : "Unavailable")} {entry.durationSeconds ? `· ${durationLabel(entry.durationSeconds)}` : ""}</span></span>
                                </label>;
                              })}
                            </div>}
                          </>;
                        })()}
                      </div>}
                    </article>)}
                    <label className="flex cursor-pointer items-start gap-2.5 border-t border-neutral-800 pt-3 text-xs text-neutral-300">
                      <input type="checkbox" checked={rightsConfirmed} onChange={event => setRightsConfirmed(event.target.checked)} className="mt-0.5 size-4 shrink-0 accent-rose-600" />
                      <span>I own this content or have permission to download it.</span>
                    </label>
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <button type="button" onClick={() => { setDrafts([]); setRightsConfirmed(false); }} className="px-2 py-2 text-xs text-neutral-400 hover:text-white">Clear inspection</button>
                      <button type="submit" disabled={submitting || !serviceReady || !rightsConfirmed || drafts.some(draft => draft.kind === "playlist" && draft.selectedPlaylistIndexes.length === 0)} className={primaryButton}>
                        {submitting && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}
                        {submitting ? "Adding…" : `Add ${drafts.length} to queue`}<ChevronDown className="size-3.5 -rotate-90" aria-hidden="true" />
                      </button>
                    </div>
                  </div>}
                </form>
              </div>
            </section>

            <section aria-labelledby="queue-heading" className="space-y-3">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div><p className="mb-1 text-[11px] font-bold uppercase tracking-[0.16em] text-neutral-500">Downloads</p><h2 id="queue-heading" className="text-xl font-bold">Active downloads & batch queue <span className="ml-1 text-sm font-medium text-neutral-500">({visibleQueueRows.length})</span></h2><p className="mt-1 text-[11px] text-neutral-500">{activeCount} / {settings.maxConcurrentDownloads} active · {formatBytes(totalCurrentSpeed)}/s combined</p></div>
                <div className="flex flex-wrap gap-2">
                  <button type="button" className={button} onClick={() => void batchAction("pause")} disabled={!serviceReady || !jobs.some(job => job.status === "queued" || job.status === "downloading" || job.status === "processing")}><Pause className="size-3.5" aria-hidden="true" />Pause all</button>
                  <button type="button" className={button} onClick={() => void batchAction("resume")} disabled={!serviceReady || !jobs.some(job => job.status === "paused")}><Play className="size-3.5" aria-hidden="true" />Resume all</button>
                  <button type="button" className={button} onClick={clearCompleted} disabled={!serviceReady || completedQueueCount === 0}><Trash2 className="size-3.5" aria-hidden="true" />Clear completed{completedQueueCount > 0 ? ` (${completedQueueCount})` : ""}</button>
                </div>
              </div>

              <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-rose-900/50 bg-rose-950/20 px-4 py-3 text-xs">
                <div className="flex items-center gap-2 text-neutral-300"><span className={`size-2 rounded-full ${activeCount > 0 ? "animate-pulse bg-rose-500" : "bg-neutral-600"}`} /><span className="font-semibold">{activeCount > 0 ? "Batch in progress" : queuedCount > 0 ? "Queue ready" : "Queue idle"}</span><span className="text-neutral-500">{activeCount} active · {queuedCount} queued</span></div>
                <div className="flex items-center gap-4 font-mono text-[11px]"><span className="text-emerald-300">{formatBytes(totalCurrentSpeed)}/s</span><span className="text-neutral-500">Max parallel: {settings.maxConcurrentDownloads}</span></div>
              </div>

              <div className={`${panel} flex flex-wrap items-center justify-between gap-3 p-3`}>
                <div className="flex flex-wrap gap-1 rounded-lg border border-neutral-800 bg-neutral-950 p-1">
                  {(["all", "active", "queued", "completed"] as const).map(value => {
                    const count = value === "all" ? visibleQueueRows.length : value === "active" ? activeCount : value === "queued" ? queuedCount : completedQueueCount;
                    return <button key={value} type="button" aria-pressed={queueFilter === value} onClick={() => setQueueFilter(value)} className={`rounded-md px-2.5 py-1.5 text-[11px] font-semibold capitalize ${queueFilter === value ? "bg-neutral-800 text-white" : "text-neutral-400 hover:text-neutral-200"}`}>{value} ({count})</button>;
                  })}
                </div>
                <label className="relative min-w-48 flex-1 sm:max-w-xs"><Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-neutral-500" aria-hidden="true" /><input aria-label="Search queue" className={`${field} py-2 pl-9 text-xs`} value={search} onChange={event => setSearch(event.target.value)} placeholder="Search queue…" /></label>
              </div>

              {filteredQueue.length === 0 ? <div className={`${panel} px-5 py-12 text-center`}>
                <div className="mx-auto mb-3 grid size-12 place-items-center rounded-2xl bg-neutral-800 text-neutral-500"><Layers className="size-5" aria-hidden="true" /></div>
                <h3 className="text-sm font-semibold text-neutral-200">{jobs.length === 0 ? "No downloads yet" : visibleQueueRows.length === 0 ? "Queue is clear" : "No videos match this filter"}</h3>
                <p className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-neutral-500">{visibleQueueRows.length === 0 && jobs.length > 0 ? "Cleared downloads remain available in your library. Inspect a YouTube URL above to add another download." : serviceReady ? "Inspect a YouTube URL above to add a real download job. Progress and status are reported by the Go worker." : "The app will load your SQLite-backed history and enable downloads as soon as its built-in Go service is ready."}</p>
                {jobs.length === 0 && <p className="mt-2 text-[10px] text-neutral-600">Video downloads, tagged MP3 conversion, and original AAC/M4A audio use the native Go service.</p>}
              </div> : <div className="space-y-2.5">
                {filteredQueue.map(({ job, item }) => {
                  const itemActive = item.status === "downloading" || item.status === "processing";
                  const batchControls = job.kind === "playlist" && item.index === 1;
                  const itemError = item.error || ((job.kind === "video" || !job.items?.length) ? job.error : "");
                  const label = job.mediaType === "audio" ? (job.audioFormat === "m4a" ? "M4A · original AAC" : `MP3 ${job.audioBitrate ?? "192k"}`) : qualityLabels[job.quality];
                  return <article key={`${job.id}:${item.index}`} className={`rounded-2xl border bg-neutral-900/80 p-4 sm:p-5 ${itemActive ? "border-rose-800/70" : "border-neutral-800"}`}>
                    <div className="flex flex-wrap items-center gap-4">
                      {item.thumbnailUrl ? <img src={item.thumbnailUrl} alt="" referrerPolicy="no-referrer" className="hidden aspect-video w-40 shrink-0 rounded-lg bg-neutral-950 object-cover sm:block" />
                        : <div className="hidden aspect-video w-40 shrink-0 place-items-center rounded-lg bg-neutral-950 text-neutral-600 sm:grid"><Film className="size-6" aria-hidden="true" /></div>}
                      <div className="min-w-0 flex-1">
                        <div className="mb-1.5 flex flex-wrap items-center gap-2">
                          <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold ${statusClass(item.status)}`}>{statusLabels[item.status]}</span>
                          <span className="text-[10px] text-rose-300">{item.author || "YouTube"}</span>
                          <span className="rounded bg-neutral-800 px-2 py-0.5 text-[10px] text-neutral-400">{label}</span>{job.category && <span className="rounded bg-neutral-800 px-2 py-0.5 text-[10px] text-neutral-400">{job.category}</span>}
                        </div>
                        <h3 className="truncate text-sm font-semibold text-white" title={item.title}>{item.title}</h3>
                        <p className="mt-1 truncate text-[10px] text-neutral-500" title={job.url}>{job.kind === "playlist" ? `${job.title} · Playlist item ${item.playlistIndex ?? item.index}${job.totalCount ? ` · selection ${item.index} of ${job.totalCount}` : ""}` : job.url}</p>
                        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-[10px]">
                          <span className={item.status === "failed" ? "text-red-300" : item.status === "completed" ? "text-emerald-300" : "text-rose-300"}>{item.status === "processing" ? "Converting audio to MP3" : item.status === "downloading" ? `Downloading${item.progress === null ? "" : ` ${item.progress.toFixed(0)}%`}` : statusLabels[item.status]}</span>
                          <span className="font-mono text-neutral-400">{item.speedBytesPerSec > 0 ? `${formatBytes(item.speedBytesPerSec)}/s · ETA ${etaLabel(item.etaSeconds)}` : item.downloadedBytes > 0 ? `${formatBytes(item.downloadedBytes)}${item.totalBytes ? ` / ${formatBytes(item.totalBytes)}` : ""}` : item.status === "queued" ? "Waiting for an available slot" : item.status === "completed" ? "Ready to save" : ""}</span>
                        </div>
                        {(itemActive || item.status === "paused" || item.status === "completed") && <div className="mt-2 h-2 overflow-hidden rounded-full bg-neutral-800"><div className={`h-full rounded-full transition-[width] ${item.status === "completed" ? "bg-emerald-500" : "bg-rose-500"} ${itemActive && item.progress === null ? "w-1/3 animate-pulse" : ""}`} style={itemActive && item.progress === null ? undefined : { width: `${Math.max(0, Math.min(100, item.progress ?? 0))}%` }} /></div>}
                      </div>
                      <div className="flex shrink-0 items-center gap-1.5">
                        {job.kind === "video" && (job.status === "downloading" || job.status === "processing" || job.status === "queued") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "pause")} aria-label={`Pause ${item.title}`}><Pause className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">Pause</span></button>}
                        {job.kind === "video" && job.status === "paused" && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "resume")} aria-label={`Resume ${item.title}`}><Play className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">Resume</span></button>}
                        {job.kind === "video" && (isActive(job) || job.status === "paused") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "cancel")} aria-label={`Cancel ${item.title}`}><X className="size-3.5" aria-hidden="true" /></button>}
                        {batchControls && (isActive(job) || job.status === "queued") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "pause")} aria-label={`Pause playlist ${job.title}`}><Pause className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">Pause batch</span></button>}
                        {batchControls && job.status === "paused" && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "resume")} aria-label={`Resume playlist ${job.title}`}><Play className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">Resume batch</span></button>}
                        {batchControls && (isActive(job) || job.status === "paused" || job.status === "queued") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "cancel")} aria-label={`Cancel playlist ${job.title}`}><X className="size-3.5" aria-hidden="true" /></button>}
                        {item.status === "completed" && item.fileId && <button type="button" className={button} disabled={busyAction === `${job.id}:${item.fileId}`} onClick={() => void saveFile(job, item.fileId)} aria-label={`Save ${item.title}`}><ArrowDownToLine className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">Save</span></button>}
                        {job.kind === "video" && (job.status === "failed" || job.status === "cancelled") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "retry")} aria-label={`Retry ${item.title}`}><RefreshCw className="size-3.5" aria-hidden="true" /></button>}
                        {batchControls && (job.status === "failed" || job.status === "partial" || job.status === "cancelled") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "retry")} aria-label={`Retry playlist ${job.title}`}><RefreshCw className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">Retry batch</span></button>}
                        {item.status === "failed" && job.kind === "video" && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "remove")} aria-label={`Remove ${item.title}`}><Trash2 className="size-3.5" aria-hidden="true" /></button>}
                      </div>
                    </div>
                    {itemError && item.status === "failed" && <p className="mt-3 flex items-start gap-1.5 text-[11px] text-red-300"><AlertCircle className="mt-0.5 size-3 shrink-0" aria-hidden="true" />{itemError}</p>}
                    {job.note && item.index === 1 && <p className="mt-3 text-[10px] leading-relaxed text-amber-300/80">{job.note}</p>}
                  </article>;
                })}
              </div>}
            </section>
          </>
        )}

        {tab === "library" && <section aria-labelledby="library-heading" className="space-y-4">
          <div className="flex flex-wrap items-end justify-between gap-3"><div><p className="mb-1 text-[11px] font-bold uppercase tracking-[0.16em] text-neutral-500">Saved output</p><h2 id="library-heading" className="text-2xl font-bold">Download library</h2><p className="mt-1 text-xs text-neutral-400">Browse finalized media by type, category, channel, or tracked output metadata.</p></div><div className="flex items-center gap-2 rounded-lg border border-neutral-800 bg-neutral-900 px-3 py-2 text-xs text-neutral-400"><HardDrive className="size-3.5 text-amber-400" aria-hidden="true" />{visibleLibraryJobs.length} of {libraryJobs.length} jobs visible</div></div>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            <div className={`${panel} px-4 py-3`}><p className="text-[10px] uppercase tracking-wider text-neutral-500">Files</p><p className="mt-1 text-lg font-bold text-white">{libraryStats.files}</p></div>
            <div className={`${panel} px-4 py-3`}><p className="text-[10px] uppercase tracking-wider text-neutral-500">Logical media</p><p className="mt-1 text-lg font-bold text-white">{formatBytes(libraryStats.logicalBytes)}</p></div>
            <div className={`${panel} px-4 py-3`}><p className="text-[10px] uppercase tracking-wider text-neutral-500">Managed copies</p><p className="mt-1 text-lg font-bold text-white">{formatBytes(libraryStats.managedBytes)}</p></div>
            <div className={`${panel} px-4 py-3`}><p className="text-[10px] uppercase tracking-wider text-neutral-500">Published copies</p><p className="mt-1 text-lg font-bold text-white">{formatBytes(libraryStats.publishedBytes)}</p></div>
          </div>
          <div className={`${panel} space-y-3 p-3`}>
            <div className="flex flex-wrap items-center gap-3">
              <div className="flex gap-1 rounded-lg border border-neutral-800 bg-neutral-950 p-1">{(["all", "video", "audio"] as const).map(value => <button key={value} type="button" aria-pressed={libraryFilter === value} onClick={() => setLibraryFilter(value)} className={`rounded-md px-2.5 py-1.5 text-[11px] font-semibold capitalize ${libraryFilter === value ? "bg-neutral-800 text-white" : "text-neutral-400 hover:text-neutral-200"}`}>{value}</button>)}</div>
              <label className="relative min-w-52 flex-1"><Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-neutral-500" aria-hidden="true" /><input aria-label="Search library" className={`${field} py-2 pl-9 text-xs`} value={librarySearch} onChange={event => setLibrarySearch(event.target.value)} placeholder="Search title, channel, category, filename, or path" /></label>
              <div className="flex gap-1 rounded-lg border border-neutral-800 bg-neutral-950 p-1"><button type="button" aria-pressed={libraryLayout === "grid"} onClick={() => setLibraryLayout("grid")} className={`rounded-md px-2.5 py-1.5 text-[11px] font-semibold ${libraryLayout === "grid" ? "bg-neutral-800 text-white" : "text-neutral-400"}`}>Grid</button><button type="button" aria-pressed={libraryLayout === "list"} onClick={() => setLibraryLayout("list")} className={`rounded-md px-2.5 py-1.5 text-[11px] font-semibold ${libraryLayout === "list" ? "bg-neutral-800 text-white" : "text-neutral-400"}`}>List</button></div>
            </div>
            <div className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
              <select aria-label="Filter library by category" value={libraryCategory} onChange={event => setLibraryCategory(event.target.value)} className={`${field} py-2 text-xs`}>
                <option value="all">All categories ({libraryStats.files})</option>{libraryCategories.map(([category, count]) => <option key={category} value={category}>{category} ({count})</option>)}
              </select>
              <select aria-label="Filter library by channel" value={libraryChannel} onChange={event => setLibraryChannel(event.target.value)} className={`${field} py-2 text-xs`}>
                <option value="all">All channels</option>{libraryChannels.map(([channel, count]) => <option key={channel} value={channel}>{channel} ({count})</option>)}
              </select>
              <button type="button" className={button} disabled={!librarySearch && libraryFilter === "all" && libraryCategory === "all" && libraryChannel === "all"} onClick={() => { setLibrarySearch(""); setLibraryFilter("all"); setLibraryCategory("all"); setLibraryChannel("all"); }}>Reset filters</button>
            </div>
          </div>
          {libraryJobs.length === 0 ? <div className={`${panel} px-5 py-14 text-center`}><Film className="mx-auto mb-3 size-8 text-neutral-600" aria-hidden="true" /><h3 className="text-sm font-semibold text-neutral-200">Your library is empty</h3><p className="mt-1 text-xs text-neutral-500">Finalized downloads will appear here, with metadata and secure save links.</p></div>
            : visibleLibraryJobs.length === 0 ? <div className={`${panel} px-5 py-12 text-center text-xs text-neutral-400`}>No saved downloads match this search and media filter.</div>
            : <div className={`grid gap-4 ${libraryLayout === "grid" ? "md:grid-cols-2" : "grid-cols-1"}`}>
              {visibleLibraryJobs.map(job => <article key={job.id} className={`${panel} overflow-hidden`}>
                <div className="flex items-start justify-between gap-3 border-b border-neutral-800 p-4">
                  <div className="min-w-0"><div className="mb-1.5 flex flex-wrap gap-2"><span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold ${statusClass(job.status)}`}>{statusLabels[job.status]}</span><span className="text-[10px] text-neutral-500">{job.kind} · {job.mediaType === "audio" ? (job.audioFormat === "m4a" ? "M4A original" : `MP3 ${job.audioBitrate ?? "192k"}`) : qualityLabels[job.quality]}{job.category ? ` · ${job.category}` : ""}</span></div><h3 className="truncate text-sm font-bold text-white">{job.title}</h3><p className="mt-1 text-[10px] text-neutral-500">{dateLabel(job.createdAt)} · {job.files.length} file{job.files.length === 1 ? "" : "s"}</p></div>
                  <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "remove")} title="Remove from Library while preserving published output"><Trash2 className="size-3.5" aria-hidden="true" /><span className="hidden sm:inline">Remove</span></button>
                </div>
                <div className="space-y-2 p-3">
                  {job.files.map(file => <div key={file.id} className="flex items-center gap-3 rounded-xl border border-neutral-800/80 bg-neutral-950/70 p-2.5">
                    <LibraryThumbnail connection={connection} jobId={job.id} file={file} audio={job.mediaType === "audio"} />
                    <div className="min-w-0 flex-1"><h4 className="truncate text-xs font-semibold text-neutral-200" title={file.title || file.outputName || file.name}>{file.title || file.outputName || file.name}</h4><p className="mt-1 truncate font-mono text-[10px] text-neutral-500" title={file.outputRelativePath || file.author || file.name}>{file.outputRelativePath || file.author || file.name}</p><p className="mt-1 text-[10px] text-neutral-600">{file.height ? `${file.height}p · ` : ""}{formatBytes(file.size)}{file.durationSeconds ? ` · ${durationLabel(file.durationSeconds)}` : ""}</p><div className="mt-1 flex flex-wrap gap-1">{file.managedAvailable === false && <span className="rounded bg-amber-950/60 px-1.5 py-0.5 text-[9px] text-amber-300">Managed copy removed</span>}{file.outputRelativePath && file.publishedAvailable === false && <span className="rounded bg-red-950/50 px-1.5 py-0.5 text-[9px] text-red-300">Published copy removed</span>}</div></div>
                    <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                      {file.publishedAvailable !== false && file.outputRelativePath && <><button type="button" className={button} disabled={busyAction === `${job.id}:${file.id}:reveal`} onClick={() => void filesystemAction(job, file.id, "reveal")} aria-label={`Reveal ${file.title || file.name} in folder`} title="Reveal published file in its folder"><FolderTree className="size-3.5" aria-hidden="true" /><span className="hidden xl:inline">Reveal</span></button><button type="button" className={button} disabled={busyAction === `${job.id}:${file.id}:open-folder`} onClick={() => void filesystemAction(job, file.id, "open-folder")} aria-label={`Open folder for ${file.title || file.name}`} title="Open published output folder"><Folder className="size-3.5" aria-hidden="true" /><span className="hidden xl:inline">Folder</span></button><button type="button" className={button} disabled={busyAction === `${job.id}:${file.id}:copy-path`} onClick={() => void filesystemAction(job, file.id, "copy-path")} aria-label={`Copy path for ${file.title || file.name}`} title="Copy absolute published path"><FileText className="size-3.5" aria-hidden="true" /><span className="hidden xl:inline">Path</span></button></>}
                      <button type="button" className={button} disabled={busyAction === `${job.id}:${file.id}:preview` || file.managedAvailable === false} onClick={() => void previewFile(job, file)} aria-label={`Preview ${file.title || file.name}`} title={file.managedAvailable === false ? "Preview requires an app-managed copy" : "Preview local media"}><Play className="size-3.5" aria-hidden="true" /><span className="hidden sm:inline">Preview</span></button>
                      <button type="button" className={button} disabled={busyAction === `${job.id}:${file.id}` || file.managedAvailable === false} onClick={() => void saveFile(job, file.id)} aria-label={`Save ${file.title || file.name}`} title={file.managedAvailable === false ? "The app-managed copy has been removed" : "Save a copy through the browser"}><ArrowDownToLine className="size-3.5" aria-hidden="true" /><span className="hidden sm:inline">Save</span></button>
                    </div>
                  </div>)}
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 border-t border-neutral-800 px-4 py-3">
                  {job.error && <p className="max-w-sm text-[10px] text-amber-300/80">{job.error}</p>}
                  <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
                    {(job.status === "partial" || job.status === "failed" || job.status === "cancelled") && <button type="button" className={button} onClick={() => void jobAction(job, "retry")} disabled={busyAction === job.id}><RefreshCw className="size-3.5" aria-hidden="true" />Retry job</button>}
                    {job.files.length > 1 && job.files.every(file => file.managedAvailable !== false) && <button type="button" className={primaryButton} onClick={() => void saveFile(job)} disabled={busyAction === `${job.id}:zip`}><ArrowDownToLine className="size-3.5" aria-hidden="true" />Save all as ZIP</button>}
                    {job.files.some(file => file.managedAvailable !== false) && <button type="button" className={button} onClick={() => void jobAction(job, "delete-managed")} disabled={busyAction === job.id} title="Delete only app-managed copies"><Trash2 className="size-3.5" aria-hidden="true" />Managed copy</button>}
                    {job.files.some(file => file.publishedAvailable !== false && Boolean(file.outputRelativePath)) && <button type="button" className={button} onClick={() => void jobAction(job, "delete-published")} disabled={busyAction === job.id} title="Delete only files in the configured output folder"><Trash2 className="size-3.5" aria-hidden="true" />Published copy</button>}
                    <button type="button" className={`${button} border-red-900/70 text-red-300 hover:border-red-700 hover:bg-red-950/40`} onClick={() => void jobAction(job, "delete-all")} disabled={busyAction === job.id}><Trash2 className="size-3.5" aria-hidden="true" />Delete everywhere</button>
                  </div>
                </div>
              </article>)}
            </div>}
        </section>}

        {tab === "settings" && <div className="mx-auto max-w-4xl space-y-5">
          <div><p className="mb-1 text-[11px] font-bold uppercase tracking-[0.16em] text-neutral-500">Configuration</p><h2 className="text-2xl font-bold">Service & preferences</h2><p className="mt-1 text-xs text-neutral-400">The Go service is the source of truth for downloads and SQLite-backed preferences.</p></div>

          <section className={`${panel} p-5 sm:p-6`} aria-labelledby="service-heading">
            <div className="mb-4 flex items-start justify-between gap-3"><div><h3 id="service-heading" className="text-sm font-bold">Built-in Go download service</h3><p className="mt-1 max-w-2xl text-xs leading-relaxed text-neutral-400">This single-executable app connects automatically to its local Go backend at <span className="font-mono text-neutral-300">{connection.base}</span>. Downloads, history, and preferences use the same service and SQLite database.</p></div><span className={`rounded-full border px-2.5 py-1 text-[10px] font-semibold ${serviceReady ? "border-emerald-700/60 bg-emerald-950/40 text-emerald-300" : "border-amber-700/60 bg-amber-950/40 text-amber-200"}`}>{serviceReady ? "Connected" : serviceError ? "Reconnecting" : "Connecting"}</span></div>
            {serviceError && <p role="alert" className="mt-3 text-xs text-red-300">{serviceError}</p>}
            {!serviceReady && <p className="mt-2 text-[10px] text-neutral-500">The app retries the local service automatically while it starts.</p>}
          </section>

          <form onSubmit={savePreferences} className="space-y-5">
            <section className={`${panel} space-y-4 p-5 sm:p-6`}>
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-neutral-800 pb-4">
                <div><h3 className="text-sm font-bold">Preferences & output configuration</h3><p className="mt-1 text-xs text-neutral-400">Choose where finished files go, how they are named, and how folders are organized.</p></div>
                <button type="submit" className={primaryButton} disabled={!serviceReady || savingSettings}>{savingSettings && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}{settingsSaved ? <Check className="size-4" aria-hidden="true" /> : null}{savingSettings ? "Saving…" : settingsSaved ? "Saved" : "Save preferences"}</button>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-amber-700/40 bg-amber-950/30 text-amber-300"><Folder className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Download directory location</h4><p className="mt-0.5 text-[11px] text-neutral-400">Finished media is copied to this local or external folder.</p></div></div>
                <label className="block text-[11px] font-medium text-neutral-300" htmlFor="download-location">Absolute folder path</label>
                <div className="mt-1 flex gap-2"><input id="download-location" type="text" autoComplete="off" className={`${field} font-mono text-xs`} value={settings.downloadLocation} onChange={event => changeSetting("downloadLocation", event.target.value)} placeholder="C:\\Users\\you\\Downloads\\YouTube_Vault" /><button type="button" className={button} onClick={() => void selectDownloadFolder()} disabled={!serviceReady} title="Choose a folder with the operating system picker"><Folder className="size-3.5 text-amber-400" aria-hidden="true" />Browse</button></div>
                <p className="mt-2 text-[10px] text-neutral-500">Enter an absolute path. The app creates the folder when the first download finishes. Existing files stay in their original locations.</p>
                <div className="mt-3 flex flex-wrap items-center gap-1.5 text-[10px]">
                  <span className="mr-1 text-neutral-500">Quick paths:</span>
                  {["YouTube_Vault", "Media", "YouTube"].map(name => {
                    const path = settings.downloadLocation;
                    const lastSlash = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
                    const separator = path.includes("\\") ? "\\" : "/";
                    const parent = lastSlash >= 0 ? path.slice(0, lastSlash) : path;
                    const preset = `${parent}${parent.endsWith(separator) || !parent ? "" : separator}${name}`;
                    return <button key={name} type="button" onClick={() => changeSetting("downloadLocation", preset)} className="rounded-md border border-neutral-800 bg-neutral-900 px-2.5 py-1 font-mono text-neutral-300 hover:border-neutral-600">{name}</button>;
                  })}
                </div>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-emerald-700/40 bg-emerald-950/30 text-emerald-300"><HardDrive className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Storage policy</h4><p className="mt-0.5 text-[11px] text-neutral-400">Choose which durable copy each newly queued job keeps after finalization.</p></div></div>
                <div className="grid gap-2 md:grid-cols-3">
                  {([
                    ["managed-published", "Managed + Published", "Keep a private Library copy and a copy in your configured output folder. Uses the most disk space."],
                    ["published-only", "Published only", "Keep only the configured output copy after publishing. Library metadata remains, but in-app Save links are unavailable."],
                    ["managed-only", "Managed only", "Keep only the private Library copy and do not publish to the output folder. Managed media follows app retention."],
                  ] as const).map(([value, label, description]) => <label key={value} className={`cursor-pointer rounded-lg border p-3 ${settings.storageMode === value ? "border-rose-600/70 bg-rose-950/20" : "border-neutral-800 bg-neutral-900/50"}`}><span className="flex items-center justify-between gap-2 text-[11px] font-semibold text-neutral-200">{label}<input type="radio" name="storage-mode" value={value} checked={settings.storageMode === value} onChange={() => changeSetting("storageMode", value)} className="accent-rose-600" /></span><span className="mt-1 block text-[10px] leading-relaxed text-neutral-500">{description}</span></label>)}
                </div>
                <p className="mt-3 text-[10px] leading-relaxed text-neutral-500">The selected policy is captured when a job is queued. Changing this setting later does not alter existing jobs.</p>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-blue-700/40 bg-blue-950/30 text-blue-300"><FileText className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">File naming format preferences</h4><p className="mt-0.5 text-[11px] text-neutral-400">Use tokens to build the saved filename.</p></div></div>
                <label className="block text-[11px] font-medium text-neutral-300" htmlFor="naming-pattern">Naming pattern template</label>
                <input id="naming-pattern" type="text" className={`${field} mt-1 font-mono text-xs`} value={settings.namingPattern} onChange={event => changeSetting("namingPattern", event.target.value)} />
                <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px]"><span className="mr-1 text-neutral-500">Insert token:</span>{["{channel}", "{title}", "{resolution}", "{category}"].map(token => <button key={token} type="button" onClick={() => changeSetting("namingPattern", `${settings.namingPattern}${settings.namingPattern ? " " : ""}${token}`)} className="rounded-md border border-neutral-800 bg-neutral-900 px-2.5 py-1 font-mono text-neutral-300 hover:border-neutral-600"><Plus className="mr-1 inline size-3 text-rose-400" aria-hidden="true" />{token}</button>)}</div>
                <div className="mt-3 rounded-lg border border-neutral-800 bg-neutral-900/70 p-3"><div className="flex items-center gap-1.5 text-[10px] font-semibold text-neutral-300"><Sparkles className="size-3.5 text-amber-300" aria-hidden="true" />Live file generation preview</div>
                  {(() => {
                    const sample = settings.namingPattern.replaceAll("{channel}", "Marques Brownlee").replaceAll("{title}", "M3 Max MacBook Pro Deep Dive").replaceAll("{resolution}", "1080p").replaceAll("{category}", settings.defaultCategory || "General");
                    const safePreview = sample || "Untitled";
                    const separator = settings.downloadLocation.includes("\\") ? "\\" : "/";
                    const subfolder = settings.subfolderSorting === "channel" ? "Marques Brownlee" : settings.subfolderSorting === "category" ? settings.defaultCategory : "";
                    return <><p className="mt-1 break-all font-mono text-xs text-emerald-300">{safePreview}.mp4</p><p className="mt-1 break-all font-mono text-[10px] text-neutral-400">{settings.downloadLocation}{subfolder ? `${separator}${subfolder}` : ""}{separator}{safePreview}.mp4</p></>;
                  })()}
                </div>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-purple-700/40 bg-purple-950/30 text-purple-300"><FolderTree className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Subfolder sorting organization</h4><p className="mt-0.5 text-[11px] text-neutral-400">Organize finished downloads into structured folders.</p></div></div>
                <div className="grid gap-2 sm:grid-cols-3">
                  {([ ["channel", "Sort by YouTube channel"], ["category", "Sort by category"], ["flat", "No subfolders"] ] as const).map(([value, label]) => <label key={value} className={`cursor-pointer rounded-lg border p-3 ${settings.subfolderSorting === value ? "border-rose-600/70 bg-rose-950/20" : "border-neutral-800 bg-neutral-900/50"}`}><span className="flex items-center justify-between text-[11px] font-semibold text-neutral-200">{label}<input type="radio" name="subfolder-sorting" checked={settings.subfolderSorting === value} onChange={() => changeSetting("subfolderSorting", value)} className="accent-rose-600" /></span><span className="mt-1 block text-[10px] text-neutral-500">{value === "channel" ? "Files grouped by creator." : value === "category" ? "Files grouped by your selected category." : "Save directly in the destination."}</span></label>)}
                </div>
                <label className="mt-3 block max-w-sm text-[11px] font-medium text-neutral-300">Default category for new downloads
                  <select className={`${field} mt-1.5 text-xs`} value={settings.defaultCategory} onChange={event => changeSetting("defaultCategory", event.target.value)}>{settings.userCategories.map(category => <option key={category} value={category}>{category}</option>)}</select>
                </label>
                <div className="mt-3 rounded-lg border border-neutral-800 bg-neutral-900/50 p-3">
                  <div className="flex items-center justify-between text-[11px]"><span className="font-semibold text-neutral-300">User-defined categories</span><span className="text-neutral-500">{settings.userCategories.length} active</span></div>
                  <div className="mt-2 flex flex-wrap gap-1.5">{settings.userCategories.map(category => <span key={category} className="inline-flex items-center gap-1 rounded-md border border-neutral-800 bg-neutral-950 px-2 py-1 text-[10px] text-neutral-200">{category}<button type="button" onClick={() => removeCategory(category)} disabled={settings.userCategories.length <= 1} aria-label={`Remove ${category}`} className="text-neutral-500 hover:text-rose-300 disabled:opacity-40"><X className="size-3" aria-hidden="true" /></button></span>)}</div>
                  <div className="mt-2 flex gap-2"><input type="text" aria-label="New category" maxLength={40} value={newCategoryInput} onChange={event => setNewCategoryInput(event.target.value)} onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); addCategory(); } }} placeholder="Add a category" className={`${field} py-2 text-xs`} /><button type="button" onClick={addCategory} className={button}><Plus className="size-3.5" aria-hidden="true" />Add</button></div>
                </div>
              </div>

              <div className="grid gap-4 border-t border-neutral-800 pt-4 sm:grid-cols-2">
                <label className="block text-xs font-medium text-neutral-300">Default maximum video quality
                  <select aria-label="Default maximum video quality" className={`${field} mt-1.5`} value={settings.defaultQuality} onChange={event => changeSetting("defaultQuality", event.target.value as Quality)}>{(Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
                </label>
                <label className="block text-xs font-medium text-neutral-300">Maximum concurrent downloads <span className="float-right font-mono text-rose-300">{settings.maxConcurrentDownloads}</span>
                  <input aria-label="Maximum concurrent downloads" type="range" min="1" max="6" step="1" value={settings.maxConcurrentDownloads} onChange={event => changeSetting("maxConcurrentDownloads", Number(event.target.value))} className="mt-2 w-full accent-rose-600" />
                  <span className="mt-1 flex justify-between text-[10px] text-neutral-500"><span>1 stream</span><span>6 streams</span></span>
                </label>
              </div>
              {serviceError && <p role="alert" className="text-xs text-red-300">{serviceError}</p>}
              <div className="flex flex-wrap items-center justify-between gap-3 border-t border-neutral-800 pt-4">
                <p className="max-w-lg text-[10px] leading-relaxed text-neutral-500">Settings apply to new jobs. Each download also stays in private app storage for the library and secure save links. {mp3Supported ? "Built-in Go MP3 conversion is ready." : "This backend does not support MP3 conversion."}</p>
                <button type="submit" className={primaryButton} disabled={!serviceReady || savingSettings}>{savingSettings && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}{settingsSaved ? <Check className="size-4" aria-hidden="true" /> : null}{savingSettings ? "Saving…" : settingsSaved ? "Saved to SQLite" : "Save preferences"}</button>
              </div>
            </section>
          </form>

            <section className={`${panel} p-5`}><div className="flex items-start gap-3"><div className="grid size-9 shrink-0 place-items-center rounded-xl border border-blue-800/50 bg-blue-950/30 text-blue-300"><Gauge className="size-4" aria-hidden="true" /></div><div><h3 className="text-xs font-bold">What this backend supports</h3><ul className="mt-2 space-y-1.5 text-[11px] leading-relaxed text-neutral-400"><li>Video and playlist downloads, including adaptive MP4 remuxing and pure-Go MP3 conversion for AAC audio.</li><li>Up to six concurrent jobs, multi-routine stream transfers, pause/resume, retries, and live speed and ETA.</li><li>Quality ceilings: best, 1080p, 720p, or 480p. Actual output quality is reported after completion.</li><li>Files are copied to the selected destination and remain available in the private SQLite-backed library.</li></ul>{!mp3Supported && <p className="mt-3 flex items-start gap-1.5 text-[10px] leading-relaxed text-amber-300"><ShieldCheck className="mt-0.5 size-3 shrink-0" aria-hidden="true" />This backend does not support MP3 conversion.</p>}</div></div></section>
        </div>}
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
