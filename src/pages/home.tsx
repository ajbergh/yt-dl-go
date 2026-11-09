/**
 * Shared desktop/browser screen coordinator. Service and action hooks own API
 * state; this page owns navigation, inspection drafts, filters, and previews.
 * Library rows are loaded file pages; navigation totals come from server metadata.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import { MP3Preview } from "../components/downloader/mp3-preview";
import { LibraryPage } from "./library";
import { QueuePage } from "./queue";
import { SettingsPage } from "./settings";
import { useService } from "../hooks/use-service";
import { useSettings } from "../hooks/use-settings";
import { useJobs } from "../hooks/use-jobs";
import {
  Activity, AlertCircle, Check, Clock3, Film, HardDrive, Layers, Plus, Settings, X,
} from "lucide-react";
import {
  api, parseYouTubeURL,
  type DownloadJob, type Inspection, type LibraryQuery, type Quality,
} from "../lib/downloader";

import {
  button, errorMessage, inspectedVideoID, libraryLayoutKey, primaryButton,
  queueItemKey, queueItemsFor, selectedPlaylistEntries,
  type Draft, type LibraryFilter, type QueueFilter, type QueueRow, type Tab,
} from "../components/downloader/view-model";

export function HomePage() {
  const previewMedia = useRef<HTMLMediaElement | null>(null);
  const previewDialog = useRef<HTMLDivElement | null>(null);
  const [tab, setTab] = useState<Tab>("queue");
  const {
    connection, serviceReady, jobs, setJobs, libraryJobs, libraryPage, setLibraryQuery, loadMoreLibrary, loadingLibraryMore, refreshLibrary, settings, setSettings, runtimeSettingSources, setRuntimeSettingSources, runtimeSettingValues, setRuntimeSettingValues, mp3Supported,
    buildInfo, updateStatus, updateError, checkingUpdates, checkForUpdates,
    serviceError, setServiceError, pollError, storageWarnings,
  } = useService();
  const [url, setUrl] = useState("");
  const [batchMode, setBatchMode] = useState(false);
  const [rightsConfirmed, setRightsConfirmed] = useState(false);
  const [startPaused, setStartPaused] = useState(false);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [inspecting, setInspecting] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const [notice, setNotice] = useState("");
  const {
    newCategoryInput, setNewCategoryInput, savingSettings, settingsSaved, settingsDirty, changeSetting,
    selectDownloadFolder, savePreferences, toggleNotifications, addCategory, removeCategory,
  } = useSettings({
    connection, serviceReady, settings, setSettings, setRuntimeSettingSources, setRuntimeSettingValues, setServiceError, setNotice,
  });
  const [queueFilter, setQueueFilter] = useState<QueueFilter>("all");
  const [search, setSearch] = useState("");
  const [libraryFilter, setLibraryFilter] = useState<LibraryFilter>("all");
  const [librarySearch, setLibrarySearch] = useState("");
  const [libraryCategory, setLibraryCategory] = useState("all");
  const [libraryChannel, setLibraryChannel] = useState("all");
  const [libraryView, setLibraryView] = useState<LibraryQuery>(() => {
    try {
      const sort = window.localStorage.getItem("yt-dl-go:library-sort-v2");
      return { sort: sort === "oldest" || sort === "title" || sort === "size" || sort === "duration" ? sort : "recent" };
    } catch {
      return { sort: "recent" };
    }
  });
  const [libraryLayout, setLibraryLayout] = useState<"grid" | "list">(() => {
    try { return window.localStorage.getItem(libraryLayoutKey) === "list" ? "list" : "grid"; }
    catch { return "grid"; }
  });
  const queuedJobsOrdered = useMemo(
    () => jobs.filter(job => job.status === "queued").sort((a, b) => {
      const left = a.queuePosition ?? Number.MAX_SAFE_INTEGER;
      const right = b.queuePosition ?? Number.MAX_SAFE_INTEGER;
      return left === right ? a.createdAt.localeCompare(b.createdAt) : left - right;
    }),
    [jobs],
  );

  const {
    busyAction, actionError, preview, setPreview, clearedQueueItems, jobAction,
    retryPlaylistItem, retryAllFailedItems, updateJobConfiguration, bulkJobAction, bulkForgetLibraryItems, previewFile, saveFile, filesystemAction, reorderQueuedJobs,
    downloadNext, reorderPlaylistItems, batchAction, clearCompleted,
  } = useJobs({
    connection, serviceReady, jobs, setJobs, queuedJobsOrdered, setNotice, refreshLibrary,
  });

  const previewOpen = Boolean(preview);
  useEffect(() => {
    if (!previewOpen) return;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    previewDialog.current?.querySelector<HTMLButtonElement>("button")?.focus();
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        setPreview(null);
      } else if (event.key === "Tab") {
        const controls = previewDialog.current?.querySelectorAll<HTMLElement>('button:not([disabled]), video[controls], audio[controls], [tabindex="0"]');
        if (!controls?.length) return;
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.body.style.overflow = previousOverflow;
      previousFocus?.focus();
    };
  }, [previewOpen, setPreview]);

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
  const totalCurrentSpeed = queueRows.reduce((sum, { item }) => sum + ((item.status === "downloading" || item.status === "processing") ? item.speedBytesPerSec : 0), 0);
  const libraryCategories = useMemo(() => libraryPage.categories.map(facet => [facet.value, facet.count] as [string, number]), [libraryPage.categories]);
  const libraryChannels = useMemo(() => libraryPage.channels.map(facet => [facet.value, facet.count] as [string, number]), [libraryPage.channels]);
  const libraryStats = libraryPage.stats;
  // Filter the loaded rows immediately while the debounced server query catches
  // up. This subset must never be used to calculate whole-Vault Browse counts.
  const visibleLibraryJobs = useMemo(() => libraryJobs.filter(job => {
    const query = librarySearch.trim().toLowerCase();
    const category = job.category || job.files.find(file => file.category)?.category || "Uncategorized";
    const channels = job.files.map(file => file.author?.trim() || "Unknown channel");
    const text = `${job.title} ${job.url} ${job.category ?? ""} ${job.audioFormat ?? ""} ${job.subtitleLanguage ?? ""} ${job.files.map(file => `${file.title ?? ""} ${file.author ?? ""} ${file.category ?? ""} ${file.name} ${file.outputName ?? ""} ${file.outputRelativePath ?? ""} ${file.subtitle?.label ?? ""} ${file.subtitle?.languageCode ?? ""}`).join(" ")}`.toLowerCase();
    return (!query || text.includes(query))
      && (libraryFilter === "all" || job.files.some(file => (file.mediaType || job.mediaType || "video") === libraryFilter))
      && (libraryCategory === "all" || category === libraryCategory || job.files.some(file => file.category === libraryCategory))
      && (libraryChannel === "all" || channels.includes(libraryChannel));
  }), [libraryJobs, libraryFilter, librarySearch, libraryCategory, libraryChannel]);
  const libraryTotalJobs = libraryPage.totalJobs;

  useEffect(() => {
    const timer = setTimeout(() => setLibraryQuery({
      q: librarySearch.trim() || undefined,
      type: libraryFilter === "all" ? undefined : libraryFilter,
      category: libraryCategory === "all" ? undefined : libraryCategory,
      channel: libraryChannel === "all" ? undefined : libraryChannel,
      ...libraryView,
    }), 250);
    return () => clearTimeout(timer);
  }, [librarySearch, libraryFilter, libraryCategory, libraryChannel, libraryView, setLibraryQuery]);

  useEffect(() => {
    try { window.localStorage.setItem(libraryLayoutKey, libraryLayout); }
    catch { /* Layout persistence is optional; keep the current session state. */ }
  }, [libraryLayout]);

  async function inspectLinks() {
    setFormError("");
    setNotice("");
    setDrafts([]);
    if (!serviceReady) {
      setFormError("The app is still starting. Please try again in a moment.");
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
          ...result, selectedQuality, selectedVideoStrategy: settings.defaultVideoStrategy, mediaType: "video" as const, audioFormat: "mp3" as const, audioBitrate: "192k", splitByChapter: false,
          subtitleLanguage: "", subtitleFormat: "vtt" as const,
          category: settings.defaultCategory || settings.userCategories[0] || "General",
          selectedPlaylistIndexes, playlistExpanded: false,
        };
      }));
      setDrafts(results);
      if (results.length === 1 && results[0].kind === "video") setNotice("Video details and available qualities are ready.");
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

  function applyVideoStrategyToAll(videoStrategy: Draft["selectedVideoStrategy"]) {
    setDrafts(previous => previous.map(item =>
      item.mediaType === "video" ? { ...item, selectedVideoStrategy: videoStrategy } : item,
    ));
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
    if (!serviceReady) { setFormError("The app is still starting. Please try again in a moment."); return; }
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
            ...(draft.mediaType === "video" ? { videoStrategy: draft.selectedVideoStrategy } : {}),
            ...(draft.mediaType === "audio" ? { audioFormat: draft.audioFormat } : {}),
            ...(draft.mediaType === "audio" && draft.audioFormat === "mp3" && draft.splitByChapter ? { splitByChapter: true } : {}),
            ...(draft.mediaType === "audio" && draft.audioFormat === "mp3" ? { audioBitrate: draft.audioBitrate } : {}),
            ...(draft.subtitleLanguage ? { subtitleLanguage: draft.subtitleLanguage, subtitleFormat: draft.subtitleFormat } : {}),
            category: draft.category, rightsConfirmed: true,
            ...(startPaused ? { startPaused: true } : {}),
            ...(draft.kind === "playlist" ? { items: selectedPlaylistEntries(draft) } : {}),
          }),
          signal: AbortSignal.timeout(15000),
        });
        added.push(job);
      }
      setJobs(previous => [...added, ...previous.filter(item => !added.some(value => value.id === item.id))]);
      const addedTasks = drafts.reduce((count, draft) => count + (draft.kind === "playlist" ? selectedPlaylistEntries(draft).length : 1), 0);
      setNotice(`${addedTasks} download${addedTasks === 1 ? "" : "s"} added to the queue.`);
      setUrl("");
      setDrafts([]);
      setRightsConfirmed(false);
      setBatchMode(false);
      setStartPaused(false);
      setTab("queue");
    } catch (error) {
      if (added.length) setJobs(previous => [...added, ...previous]);
      setFormError(added.length ? `${errorMessage(error)} ${added.length} earlier link(s) were queued successfully.` : errorMessage(error));
    } finally { setSubmitting(false); }
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
          <div className="flex min-w-0 items-center gap-2 sm:gap-3">
            <div className="relative flex size-10 items-center justify-center">
              <img src="./app-icon.svg" className="size-10" alt="" aria-hidden="true" />
              {activeCount > 0 && <span className="absolute -right-1 -top-1 grid size-4 place-items-center rounded-full bg-emerald-500 text-[9px] font-bold text-black">{activeCount}</span>}
            </div>
            <div>
              <h1 className="text-sm font-bold tracking-tight min-[360px]:text-base sm:text-lg">YouTube Vault</h1>
              <p className="hidden text-xs text-neutral-400 sm:block">Video and playlist downloads</p>
            </div>
          </div>

          <nav aria-label="Main navigation" className="order-3 flex w-full items-center gap-1 rounded-xl border border-neutral-800 bg-neutral-900 p-1 sm:order-none sm:w-auto">
            {([
              ["queue", Layers, "Queue"], ["library", Film, "Vault"], ["settings", Settings, "Settings"],
            ] as const).map(([value, Icon, label]) => (
              <button key={value} type="button" onClick={() => setTab(value)} aria-current={tab === value ? "page" : undefined}
                className={`flex min-w-0 flex-1 items-center justify-center gap-1.5 rounded-lg px-2 py-2 sm:px-3 text-xs font-semibold transition-colors sm:flex-none ${tab === value ? "bg-neutral-800 text-white shadow-sm" : "text-neutral-400 hover:text-neutral-200"}`}>
                <Icon className="hidden size-3.5 shrink-0 text-rose-400 min-[360px]:block" aria-hidden="true" />{label}
                {value === "queue" && visibleQueueRows.length > 0 && <span className="rounded-full bg-rose-600 px-1.5 text-xs text-white">{visibleQueueRows.length}</span>}
              </button>
            ))}
          </nav>

          <div className="flex items-center gap-2">
            <div role="status" aria-label={serviceReady ? "App ready" : serviceError ? "App reconnecting" : "App starting"} className="flex items-center gap-1.5 rounded-lg border border-neutral-800 bg-neutral-900/70 px-2 py-2 text-xs">
              {serviceReady ? <><Activity className="size-3.5 text-emerald-400" aria-hidden="true" /><span className="text-emerald-300">Ready</span></>
                : <><AlertCircle className="size-3.5 text-amber-400" aria-hidden="true" /><span className="text-neutral-300">{serviceError ? "Retrying…" : "Starting…"}</span></>}
            </div>
            <button type="button" className={primaryButton} onClick={() => { setTab("queue"); window.requestAnimationFrame(() => { document.getElementById("new-download")?.scrollIntoView({ behavior: "smooth", block: "start" }); document.getElementById("video-url")?.focus({ preventScroll: true }); }); }}>
              <Plus className="size-4" aria-hidden="true" /><span className="hidden sm:inline">Add links</span><span className="sm:hidden">Add</span>
            </button>
          </div>
        </div>
      </header>

      <main className={"mx-auto w-full space-y-6 px-4 py-6 sm:px-6 " + (tab === "library" ? "max-w-[1800px]" : "max-w-7xl")}>
        {notice && <div role="status" className="flex items-center gap-2 rounded-xl border border-emerald-800/60 bg-emerald-950/40 px-4 py-3 text-sm text-emerald-200"><Check className="size-4 shrink-0" aria-hidden="true" /><span className="flex-1">{notice}</span><button type="button" aria-label="Dismiss notification" className="grid size-9 shrink-0 place-items-center rounded-lg hover:bg-emerald-900/50" onClick={() => setNotice("")}><X className="size-4" aria-hidden="true" /></button></div>}
        {!serviceReady && tab !== "settings" && <div role={serviceError ? "alert" : "status"} className="flex items-start gap-2 rounded-xl border border-amber-800/60 bg-amber-950/40 px-4 py-3 text-sm text-amber-200"><AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />{serviceError ? `${serviceError} The app will retry automatically.` : "Starting the app…"}</div>}
        {storageWarnings.map(warning => <div key={warning.path} role="alert" className="flex items-start gap-2 rounded-xl border border-amber-800/60 bg-amber-950/40 px-4 py-3 text-sm text-amber-200"><HardDrive className="mt-0.5 size-4 shrink-0" aria-hidden="true" /><span className="min-w-0 break-words">Low disk space: {warning.path} has less than 5% free. Downloads will continue. Free up space on this disk.</span></div>)}
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
          setFormError={setFormError}
          mp3Supported={mp3Supported}
          settings={settings}
          applyMediaTypeToAll={applyMediaTypeToAll}
          applyQualityToAll={applyQualityToAll}
          applyVideoStrategyToAll={applyVideoStrategyToAll}
          applyAudioFormatToAll={applyAudioFormatToAll}
          applyAudioBitrateToAll={applyAudioBitrateToAll}
          applyCategoryToAll={applyCategoryToAll}
          updatePlaylistSelection={updatePlaylistSelection}
          togglePlaylistEntry={togglePlaylistEntry}
          rightsConfirmed={rightsConfirmed}
          setRightsConfirmed={setRightsConfirmed}
          startPaused={startPaused}
          setStartPaused={setStartPaused}
          submitting={submitting}
          addDownloads={addDownloads}
          visibleQueueRows={visibleQueueRows}
          activeCount={activeCount}
          totalCurrentSpeed={totalCurrentSpeed}
          completedQueueCount={completedQueueCount}
          queuedCount={queuedCount}
          jobs={jobs}
          batchAction={batchAction}
          clearCompleted={() => clearCompleted(visibleQueueRows)}
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
          retryAllFailedItems={retryAllFailedItems}
          updateJobConfiguration={updateJobConfiguration}
          bulkJobAction={bulkJobAction}
          saveFile={saveFile}
          jobAction={jobAction}
        />}

        {tab === "library" && <LibraryPage
          serviceReady={serviceReady}
          libraryJobs={libraryJobs}
          visibleLibraryJobs={visibleLibraryJobs}
          libraryTotalJobs={libraryTotalJobs}
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
          mediaCounts={libraryPage.mediaCounts}
          onOrganizationSaved={() => { void refreshLibrary().catch(error => setServiceError(errorMessage(error))); }}
          libraryTotalFiles={libraryPage.totalFiles ?? 0}
          onViewChange={setLibraryView}
          hasMoreLibrary={Boolean(libraryPage.nextCursor)}
          loadingLibraryMore={loadingLibraryMore}
          loadMoreLibrary={() => void loadMoreLibrary().catch(error => setServiceError(errorMessage(error)))}
          connection={connection}
          busyAction={busyAction}
          previewFile={previewFile}
          filesystemAction={filesystemAction}
          jobAction={jobAction}
          bulkForgetLibraryItems={bulkForgetLibraryItems}
        />}

        {tab === "settings" && <SettingsPage
          connection={connection}
          serviceReady={serviceReady}
          serviceError={serviceError}
          settings={settings}
          runtimeSettingSources={runtimeSettingSources}
          runtimeSettingValues={runtimeSettingValues}
          savingSettings={savingSettings}
          settingsSaved={settingsSaved}
          settingsDirty={settingsDirty}
          mp3Supported={mp3Supported}
          buildInfo={buildInfo}
          updateStatus={updateStatus}
          updateError={updateError}
          checkingUpdates={checkingUpdates}
          checkForUpdates={checkForUpdates}
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
      {preview && <div ref={previewDialog} role="dialog" aria-modal="true" aria-labelledby="media-preview-title" className="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/80 p-4" onMouseDown={event => { if (event.target === event.currentTarget) setPreview(null); }}>
        <div className="w-full max-w-4xl overflow-hidden rounded-2xl border border-neutral-700 bg-neutral-950 shadow-2xl">
          <div className="flex items-center justify-between gap-3 border-b border-neutral-800 px-4 py-3"><div className="min-w-0"><p className="text-xs font-bold uppercase tracking-wider text-rose-400">Local preview</p><h2 id="media-preview-title" className="truncate text-sm font-semibold text-white">{preview.title}</h2></div><button type="button" className={button} onClick={() => setPreview(null)} aria-label="Close media preview"><X className="size-4" aria-hidden="true" />Close</button></div>
          <div className="bg-black p-3 sm:p-5">
            {preview.mimeType === "audio/mpeg" ? <MP3Preview key={preview.url} url={preview.url} mediaRef={element => { previewMedia.current = element; }} />
              : preview.mimeType.startsWith("audio/") ? <audio ref={element => { previewMedia.current = element; }} controls preload="metadata" src={preview.url} className="w-full">This audio format cannot be played here.</audio>
              : <video ref={element => { previewMedia.current = element; }} controls preload="metadata" src={preview.url} className="max-h-[70vh] w-full rounded-lg bg-black">This video format cannot be played here.</video>}
            {preview.chapters.length > 0 && <div className="mt-4 max-h-48 overflow-y-auto rounded-lg border border-neutral-800 p-2">
              <h3 className="px-2 pb-2 text-xs font-semibold text-neutral-300">Chapters</h3>
              <div className="grid gap-1 sm:grid-cols-2">
                {preview.chapters.map(chapter => <button key={`${chapter.startMs}-${chapter.title}`} type="button" className="flex min-w-0 items-center gap-3 rounded-md px-2 py-2 text-left text-xs text-neutral-200 hover:bg-neutral-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-rose-400" onClick={() => { if (previewMedia.current) previewMedia.current.currentTime = chapter.startMs / 1000; }}>
                  <span className="shrink-0 font-mono text-neutral-400">{Math.floor(chapter.startMs / 60000)}:{String(Math.floor(chapter.startMs / 1000) % 60).padStart(2, "0")}</span>
                  <span className="truncate">{chapter.title}</span>
                </button>)}
              </div>
            </div>}
            <p className="mt-3 text-xs text-neutral-400">Preview your saved media. Playback starts only when you choose it.</p>
          </div>
        </div>
      </div>}
      <footer className="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 pb-8 text-xs text-neutral-400 sm:px-6"><span tabIndex={0} title="Download progress updates automatically while the app is connected." className="flex items-center gap-1.5"><Clock3 className="size-3" aria-hidden="true" />Live download updates</span><span tabIndex={0} title="Completed downloads are kept in your Vault between app sessions." className="flex items-center gap-1.5"><HardDrive className="size-3" aria-hidden="true" />Saved history</span></footer>
    </div>
  );
}
