/**
 * Production downloader screen. The bundled page connects to the Go API served
 * by the same executable, then uses that API for jobs and SQLite preferences.
 */
import { useEffect, useMemo, useState } from "react";
import {
  Activity, AlertCircle, ArrowDownToLine, Check, ChevronDown, CircleHelp, Clock3,
  DownloadCloud, Film, Gauge, HardDrive, Layers, ListVideo, LoaderCircle,
  Pause, Play, Plus, RefreshCw, Search, Settings, ShieldCheck, Trash2, X,
} from "lucide-react";
import {
  api, formatBytes, isActive, parseYouTubeURL,
  type AppSettings, type DownloadJob, type Inspection, type Quality,
  type ServiceConnection, type ServiceHealth,
} from "../lib/downloader";

type Tab = "queue" | "library" | "settings";
type Draft = Inspection & { selectedQuality: Quality };
type QueueFilter = "all" | "active" | "queued" | "paused" | "attention";

const qualityLabels: Record<Quality, string> = {
  best: "Best available",
  "1080": "Up to 1080p",
  "720": "Up to 720p",
  "480": "Up to 480p",
};
const statusLabels: Record<DownloadJob["status"], string> = {
  queued: "Queued",
  downloading: "Downloading",
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

function statusClass(status: DownloadJob["status"]): string {
  if (status === "completed") return "border-emerald-700/60 bg-emerald-950/50 text-emerald-300";
  if (status === "downloading") return "border-rose-700/60 bg-rose-950/50 text-rose-300";
  if (status === "paused") return "border-amber-700/60 bg-amber-950/50 text-amber-300";
  if (status === "failed" || status === "partial") return "border-red-800/60 bg-red-950/40 text-red-300";
  return "border-neutral-700 bg-neutral-800 text-neutral-300";
}

function durationMetric(job: DownloadJob): string {
  if (job.totalCount === null) return `${job.completedCount} finished`;
  return `${job.completedCount} / ${job.totalCount} files`;
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
  const [settings, setSettings] = useState<AppSettings>({ defaultQuality: "best" });
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
  const [queueFilter, setQueueFilter] = useState<QueueFilter>("all");
  const [search, setSearch] = useState("");

  const activeCount = jobs.filter(isActive).length;
  const finishedFiles = jobs.reduce((sum, job) => sum + job.files.length, 0);
  const queueJobs = useMemo(
    () => jobs.filter(job => job.status === "queued" || job.status === "downloading" || job.status === "paused" || ((job.status === "failed" || job.status === "cancelled") && job.files.length === 0)),
    [jobs],
  );
  const libraryJobs = useMemo(
    () => jobs.filter(job => ["completed", "partial", "failed", "cancelled"].includes(job.status) && job.files.length > 0),
    [jobs],
  );

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
        setSettings(settingResult.settings);
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
        return { ...result, selectedQuality };
      }));
      setDrafts(results);
      if (results.length === 1 && results[0].kind === "video") setNotice("Video metadata and supported qualities loaded from YouTube.");
    } catch (error) { setFormError(errorMessage(error)); }
    finally { setInspecting(false); }
  }

  async function addDownloads(event: React.FormEvent) {
    event.preventDefault();
    setFormError("");
    setNotice("");
    if (!serviceReady) { setFormError("The built-in Go service is still starting. It will connect automatically."); return; }
    if (!rightsConfirmed) { setFormError("Confirm you have permission to download this content."); return; }
    if (drafts.length === 0) { setFormError("Inspect at least one link before adding it to the queue."); return; }
    setSubmitting(true);
    const added: DownloadJob[] = [];
    try {
      for (const draft of drafts) {
        const job = await api<DownloadJob>(connection, "/api/jobs", {
          method: "POST",
          body: JSON.stringify({ url: draft.url, quality: draft.selectedQuality, rightsConfirmed: true }),
          signal: AbortSignal.timeout(15000),
        });
        added.push(job);
      }
      setJobs(previous => [...added, ...previous.filter(item => !added.some(value => value.id === item.id))]);
      setNotice(added.length === 1 && added[0].kind === "playlist"
        ? "The entire playlist was added to the queue."
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

  async function jobAction(job: DownloadJob, action: "pause" | "resume" | "cancel" | "retry" | "remove") {
    if (!serviceReady) return;
    if (action === "retry" && !window.confirm("Retry this URL? Confirm that you own the content or have permission to download it.")) return;
    if (action === "remove" && !window.confirm("Delete this job's history and its downloaded files? This cannot be undone.")) return;
    setBusyAction(job.id);
    setActionError("");
    try {
      if (action === "remove") {
        await api<void>(connection, `/api/jobs/${encodeURIComponent(job.id)}`, { method: "DELETE", signal: AbortSignal.timeout(15000) });
        setJobs(previous => previous.filter(item => item.id !== job.id));
        setNotice("Job history and private output files removed.");
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

  async function batchAction(action: "pause" | "resume") {
    const eligible = jobs.filter(job => action === "pause"
      ? job.status === "queued" || job.status === "downloading"
      : job.status === "paused");
    for (const job of eligible) await jobAction(job, action);
  }

  const filteredQueue = queueJobs.filter(job => {
    const query = search.trim().toLowerCase();
    if (query && !`${job.title} ${job.currentItem} ${job.url}`.toLowerCase().includes(query)) return false;
    if (queueFilter === "active") return job.status === "downloading";
    if (queueFilter === "queued") return job.status === "queued";
    if (queueFilter === "paused") return job.status === "paused";
    if (queueFilter === "attention") return job.status === "failed" || job.status === "cancelled";
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
                {value === "queue" && queueJobs.length > 0 && <span className="rounded-full bg-rose-600 px-1.5 text-[10px] text-white">{queueJobs.length}</span>}
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
                    {drafts.map((draft, index) => <article key={`${draft.url}-${index}`} className="grid gap-3 rounded-xl border border-neutral-800 bg-neutral-950/60 p-3 sm:grid-cols-[8rem_1fr_12rem] sm:items-center">
                      {draft.thumbnailUrl ? <img src={draft.thumbnailUrl} alt="" referrerPolicy="no-referrer" className="aspect-video w-full rounded-lg bg-neutral-900 object-cover sm:w-32" />
                        : <div className="grid aspect-video w-full place-items-center rounded-lg bg-neutral-900 text-neutral-600 sm:w-32">{draft.kind === "playlist" ? <ListVideo className="size-7" aria-hidden="true" /> : <Film className="size-7" aria-hidden="true" />}</div>}
                      <div className="min-w-0">
                        <div className="mb-1 flex flex-wrap items-center gap-1.5 text-[10px] text-neutral-400"><span className="rounded bg-neutral-800 px-1.5 py-0.5 uppercase">{draft.kind}</span>{draft.author && <span>{draft.author}</span>}{draft.publishDate && <span>· {dateLabel(draft.publishDate)}</span>}</div>
                        <h4 className="break-words text-sm font-semibold text-white">{draft.title || "Untitled YouTube media"}</h4>
                        <p className="mt-1 text-[11px] text-neutral-500">{draft.kind === "playlist" ? `${draft.itemCount ?? 0} exposed entries` : durationLabel(draft.durationSeconds)}</p>
                        {draft.note && <p className="mt-1 text-[10px] leading-relaxed text-amber-300/80">{draft.note}</p>}
                      </div>
                      <label className="text-[11px] font-medium text-neutral-400">Maximum quality
                        <select aria-label={`Quality for ${draft.title || `item ${index + 1}`}`} value={draft.selectedQuality} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, selectedQuality: event.target.value as Quality } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                          {(draft.availableQualities ?? (draft.kind === "playlist"
                            ? (Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => ({ value, label, height: 0 }))
                            : [{ value: "best" as const, label: "Best available", height: 0 }])).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                        </select>
                      </label>
                    </article>)}
                    <label className="flex cursor-pointer items-start gap-2.5 border-t border-neutral-800 pt-3 text-xs text-neutral-300">
                      <input type="checkbox" checked={rightsConfirmed} onChange={event => setRightsConfirmed(event.target.checked)} className="mt-0.5 size-4 shrink-0 accent-rose-600" />
                      <span>I own this content or have permission to download it.</span>
                    </label>
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <button type="button" onClick={() => { setDrafts([]); setRightsConfirmed(false); }} className="px-2 py-2 text-xs text-neutral-400 hover:text-white">Clear inspection</button>
                      <button type="submit" disabled={submitting || !serviceReady || !rightsConfirmed} className={primaryButton}>
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
                <div><p className="mb-1 text-[11px] font-bold uppercase tracking-[0.16em] text-neutral-500">Downloads</p><h2 id="queue-heading" className="text-xl font-bold">Queue & recent jobs <span className="ml-1 text-sm font-medium text-neutral-500">{queueJobs.length}</span></h2></div>
                <div className="flex gap-2">
                  <button type="button" className={button} onClick={() => void batchAction("pause")} disabled={!serviceReady || !jobs.some(job => job.status === "queued" || job.status === "downloading")}><Pause className="size-3.5" aria-hidden="true" />Pause all</button>
                  <button type="button" className={button} onClick={() => void batchAction("resume")} disabled={!serviceReady || !jobs.some(job => job.status === "paused")}><Play className="size-3.5" aria-hidden="true" />Resume all</button>
                </div>
              </div>

              <div className={`${panel} flex flex-wrap items-center justify-between gap-3 p-3`}>
                <div className="flex flex-wrap gap-1 rounded-lg border border-neutral-800 bg-neutral-950 p-1">
                  {(["all", "active", "queued", "paused", "attention"] as const).map(value => <button key={value} type="button" onClick={() => setQueueFilter(value)} className={`rounded-md px-2.5 py-1.5 text-[11px] font-semibold capitalize ${queueFilter === value ? "bg-neutral-800 text-white" : "text-neutral-400 hover:text-neutral-200"}`}>{value === "attention" ? "Needs attention" : value}</button>)}
                </div>
                <label className="relative min-w-48 flex-1 sm:max-w-xs"><Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-neutral-500" aria-hidden="true" /><input aria-label="Search jobs" className={`${field} py-2 pl-9 text-xs`} value={search} onChange={event => setSearch(event.target.value)} placeholder="Search title or URL" /></label>
              </div>

              {filteredQueue.length === 0 ? <div className={`${panel} px-5 py-12 text-center`}>
                <div className="mx-auto mb-3 grid size-12 place-items-center rounded-2xl bg-neutral-800 text-neutral-500"><Layers className="size-5" aria-hidden="true" /></div>
                <h3 className="text-sm font-semibold text-neutral-200">{queueJobs.length === 0 ? "No downloads yet" : "No jobs match this filter"}</h3>
                <p className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-neutral-500">{serviceReady ? "Inspect a YouTube URL above to add a real download job. Progress and status are reported by the Go worker." : "The app will load your SQLite-backed history and enable downloads as soon as its built-in Go service is ready."}</p>
                {jobs.length === 0 && <p className="mt-2 text-[10px] text-neutral-600">The native service downloads compatible streams without yt-dlp, Python, or FFmpeg.</p>}
              </div> : <div className="space-y-2.5">
                {filteredQueue.map(job => <article key={job.id} className={`${panel} p-4 sm:p-5`}>
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="flex min-w-0 flex-1 gap-3">
                      {job.files[0]?.thumbnailUrl ? <img src={job.files[0].thumbnailUrl} alt="" referrerPolicy="no-referrer" className="hidden aspect-video w-28 rounded-lg bg-neutral-950 object-cover sm:block" />
                        : <div className="hidden aspect-video w-28 shrink-0 place-items-center rounded-lg bg-neutral-950 text-neutral-600 sm:grid">{job.kind === "playlist" ? <ListVideo className="size-6" aria-hidden="true" /> : <Film className="size-6" aria-hidden="true" />}</div>}
                      <div className="min-w-0 flex-1">
                        <div className="mb-1.5 flex flex-wrap items-center gap-2">
                          <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold ${statusClass(job.status)}`}>{statusLabels[job.status]}</span>
                          <span className="text-[10px] text-neutral-500">{job.kind === "playlist" ? "Playlist" : "Video"} · {qualityLabels[job.quality]}</span>
                        </div>
                        <h3 className="truncate text-sm font-semibold text-white" title={job.currentItem || job.title}>{job.currentItem || job.title}</h3>
                        {job.kind === "playlist" && <p className="mt-1 text-[11px] text-neutral-400">{durationMetric(job)}{job.totalCount === null ? " · playlist is being enumerated" : ""}</p>}
                        <p className="mt-1 truncate text-[10px] text-neutral-600" title={job.url}>{job.url}</p>
                      </div>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {job.status === "downloading" || job.status === "queued" ? <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "pause")}><Pause className="size-3.5" aria-hidden="true" />Pause</button> : null}
                      {job.status === "paused" && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "resume")}><Play className="size-3.5" aria-hidden="true" />Resume</button>}
                      {isActive(job) || job.status === "paused" ? <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "cancel")}><X className="size-3.5" aria-hidden="true" />Cancel</button> : null}
                      {(job.status === "failed" || job.status === "cancelled") && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "retry")}><RefreshCw className="size-3.5" aria-hidden="true" />Retry</button>}
                      {!isActive(job) && job.status !== "paused" && <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "remove")}><Trash2 className="size-3.5" aria-hidden="true" />Remove</button>}
                    </div>
                  </div>
                  {(job.status === "downloading" || job.status === "paused") && <div className="mt-4">
                    {job.progress === null ? <div className="h-1.5 overflow-hidden rounded-full bg-neutral-800"><div className="h-full w-1/3 animate-pulse rounded-full bg-rose-500" /></div>
                      : <div className="h-1.5 overflow-hidden rounded-full bg-neutral-800"><div className="h-full rounded-full bg-rose-500 transition-[width]" style={{ width: `${Math.max(0, Math.min(100, job.progress))}%` }} /></div>}
                    <div className="mt-1.5 flex justify-between text-[10px] text-neutral-500"><span>{job.progress === null ? job.currentItem || "Waiting for stream progress" : `${job.progress.toFixed(1)}% of current file`}</span><span>{job.completedCount} completed</span></div>
                  </div>}
                  {job.note && <p className="mt-3 text-[10px] leading-relaxed text-amber-300/80">{job.note}</p>}
                  {job.error && job.status !== "cancelled" && <p className="mt-2 flex items-start gap-1.5 text-[11px] text-red-300"><AlertCircle className="mt-0.5 size-3 shrink-0" aria-hidden="true" />{job.error}</p>}
                  {(job.failures?.length ?? 0) > 0 && <details className="mt-3 text-[11px] text-neutral-400"><summary className="cursor-pointer">Item failures ({job.failures?.length})</summary><ul className="mt-2 space-y-1">{job.failures?.map(failure => <li key={`${failure.index}-${failure.error}`}>Item {failure.index}: {failure.error}</li>)}</ul></details>}
                </article>)}
              </div>}
            </section>
          </>
        )}

        {tab === "library" && <section aria-labelledby="library-heading" className="space-y-4">
          <div className="flex flex-wrap items-end justify-between gap-3"><div><p className="mb-1 text-[11px] font-bold uppercase tracking-[0.16em] text-neutral-500">Saved output</p><h2 id="library-heading" className="text-2xl font-bold">Download library</h2><p className="mt-1 text-xs text-neutral-400">Finalized files are served through short-lived, file- or job-scoped download tickets.</p></div><div className="flex items-center gap-2 rounded-lg border border-neutral-800 bg-neutral-900 px-3 py-2 text-xs text-neutral-400"><HardDrive className="size-3.5 text-amber-400" aria-hidden="true" />{finishedFiles} finalized file{finishedFiles === 1 ? "" : "s"}</div></div>
          {libraryJobs.length === 0 ? <div className={`${panel} px-5 py-14 text-center`}><Film className="mx-auto mb-3 size-8 text-neutral-600" aria-hidden="true" /><h3 className="text-sm font-semibold text-neutral-200">Your library is empty</h3><p className="mt-1 text-xs text-neutral-500">Finalized downloads will appear here, with metadata and secure save links.</p></div>
            : <div className="grid gap-4 md:grid-cols-2">
              {libraryJobs.map(job => <article key={job.id} className={`${panel} overflow-hidden`}>
                <div className="flex items-start justify-between gap-3 border-b border-neutral-800 p-4">
                  <div className="min-w-0"><div className="mb-1.5 flex flex-wrap gap-2"><span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold ${statusClass(job.status)}`}>{statusLabels[job.status]}</span><span className="text-[10px] text-neutral-500">{job.kind} · {qualityLabels[job.quality]}</span></div><h3 className="truncate text-sm font-bold text-white">{job.title}</h3><p className="mt-1 text-[10px] text-neutral-500">{dateLabel(job.createdAt)} · {job.files.length} file{job.files.length === 1 ? "" : "s"}</p></div>
                  <button type="button" className={button} disabled={busyAction === job.id} onClick={() => void jobAction(job, "remove")} title="Delete downloaded files and history"><Trash2 className="size-3.5" aria-hidden="true" /><span className="hidden sm:inline">Delete</span></button>
                </div>
                <div className="space-y-2 p-3">
                  {job.files.map(file => <div key={file.id} className="flex items-center gap-3 rounded-xl border border-neutral-800/80 bg-neutral-950/70 p-2.5">
                    {file.thumbnailUrl ? <img src={file.thumbnailUrl} alt="" referrerPolicy="no-referrer" className="aspect-video w-24 rounded-md bg-neutral-900 object-cover" /> : <div className="grid aspect-video w-24 shrink-0 place-items-center rounded-md bg-neutral-900 text-neutral-600"><Film className="size-5" aria-hidden="true" /></div>}
                    <div className="min-w-0 flex-1"><h4 className="truncate text-xs font-semibold text-neutral-200" title={file.title || file.name}>{file.title || file.name}</h4><p className="mt-1 truncate text-[10px] text-neutral-500">{file.author || file.name}</p><p className="mt-1 text-[10px] text-neutral-600">{file.height ? `${file.height}p · ` : ""}{formatBytes(file.size)}{file.durationSeconds ? ` · ${durationLabel(file.durationSeconds)}` : ""}</p></div>
                    <button type="button" className={button} disabled={busyAction === `${job.id}:${file.id}`} onClick={() => void saveFile(job, file.id)} aria-label={`Save ${file.title || file.name}`}><ArrowDownToLine className="size-3.5" aria-hidden="true" /><span className="hidden sm:inline">Save</span></button>
                  </div>)}
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 border-t border-neutral-800 px-4 py-3">
                  {job.error && <p className="max-w-sm text-[10px] text-amber-300/80">{job.error}</p>}
                  <div className="ml-auto flex items-center gap-2">
                    {(job.status === "partial" || job.status === "failed" || job.status === "cancelled") && <button type="button" className={button} onClick={() => void jobAction(job, "retry")} disabled={busyAction === job.id}><RefreshCw className="size-3.5" aria-hidden="true" />Retry job</button>}
                    {job.files.length > 1 && <button type="button" className={primaryButton} onClick={() => void saveFile(job)} disabled={busyAction === `${job.id}:zip`}><ArrowDownToLine className="size-3.5" aria-hidden="true" />Save all as ZIP</button>}
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

          <form onSubmit={savePreferences} className={`${panel} space-y-4 p-5 sm:p-6`}>
            <div><h3 className="text-sm font-bold">Download defaults</h3><p className="mt-1 text-xs text-neutral-400">Preferences are stored by the service in its state.db file.</p></div>
            <label className="block max-w-md text-xs font-medium text-neutral-300">Default maximum video quality
              <select className={`${field} mt-1.5`} value={settings.defaultQuality} onChange={event => { setSettings({ defaultQuality: event.target.value as Quality }); setSettingsSaved(false); }}>
                {(Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
              </select>
            </label>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-neutral-800 pt-4">
              <p className="max-w-lg text-[10px] leading-relaxed text-neutral-500">The service uses one sequential worker. Output location is controlled with DATA_DIR when the Go service starts; a browser folder picker cannot safely change local paths.</p>
              <button type="submit" className={primaryButton} disabled={!serviceReady || savingSettings}>{savingSettings && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}{settingsSaved ? <Check className="size-4" aria-hidden="true" /> : null}{savingSettings ? "Saving…" : settingsSaved ? "Saved to SQLite" : "Save preferences"}</button>
            </div>
          </form>

          <section className={`${panel} p-5`}><div className="flex items-start gap-3"><div className="grid size-9 shrink-0 place-items-center rounded-xl border border-blue-800/50 bg-blue-950/30 text-blue-300"><Gauge className="size-4" aria-hidden="true" /></div><div><h3 className="text-xs font-bold">What this backend supports</h3><ul className="mt-2 space-y-1.5 text-[11px] leading-relaxed text-neutral-400"><li>Native video/playlist downloads with verified file progress and finalized-file history.</li><li>Pause/resume, cancellation, retries as new jobs, per-file or ZIP tickets, and removal of stopped jobs.</li><li>Quality ceilings: best, 1080p, 720p, or 480p. Actual output quality is reported after completion.</li></ul><p className="mt-3 flex items-start gap-1.5 text-[10px] leading-relaxed text-neutral-600"><ShieldCheck className="mt-0.5 size-3 shrink-0" aria-hidden="true" />Audio conversion, caption/thumbnail embedding, arbitrary naming rules, and filesystem browsing are not implemented by the service and are intentionally not presented as working settings.</p></div></div></section>
        </div>}
      </main>
      <footer className="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 pb-8 text-[10px] text-neutral-600 sm:px-6"><span className="flex items-center gap-1.5"><Clock3 className="size-3" aria-hidden="true" />Live job updates from the Go service</span><span className="flex items-center gap-1.5"><HardDrive className="size-3" aria-hidden="true" />SQLite-backed history</span></footer>
    </div>
  );
}
