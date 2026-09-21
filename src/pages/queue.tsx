import { useSensor, useSensors, KeyboardSensor, PointerSensor, DndContext, closestCenter, type DragEndEvent } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from "@dnd-kit/sortable";
import type { Dispatch, FormEvent, SetStateAction } from "react";
import {
  AlertCircle, ArrowDownToLine, ChevronDown, CircleHelp, DownloadCloud, Film, Layers, ListVideo,
  LoaderCircle, Pause, Play, Plus, RefreshCw, Search, Trash2, X,
} from "lucide-react";
import { formatBytes, isActive, type AppSettings, type DownloadJob, type QueueItem, type Quality } from "../lib/downloader";
import {
  SortableQueueOrderRow, approximateMP3Bytes, button, dateLabel, durationLabel, etaLabel, field,
  inspectedVideoID, panel, playlistEntries, primaryButton, qualityLabels, selectablePlaylistEntries,
  selectedPlaylistEntries, statusClass, statusLabels, videoStrategyLabels, type Draft, type QueueFilter, type QueueRow,
} from "../components/downloader/view-model";

type QueueJobAction = "pause" | "resume" | "cancel" | "retry" | "remove";

type QueuePageProps = {
  url: string;
  setUrl: Dispatch<SetStateAction<string>>;
  batchMode: boolean;
  setBatchMode: Dispatch<SetStateAction<boolean>>;
  drafts: Draft[];
  setDrafts: Dispatch<SetStateAction<Draft[]>>;
  serviceReady: boolean;
  inspecting: boolean;
  inspectLinks: () => void | Promise<void>;
  formError: string;
  setFormError: Dispatch<SetStateAction<string>>;
  mp3Supported: boolean;
  settings: AppSettings;
  applyMediaTypeToAll: (mediaType: Draft["mediaType"]) => void;
  applyQualityToAll: (quality: Quality) => void;
  applyVideoStrategyToAll: (strategy: Draft["selectedVideoStrategy"]) => void;
  applyAudioFormatToAll: (format: Draft["audioFormat"]) => void;
  applyAudioBitrateToAll: (bitrate: string) => void;
  applyCategoryToAll: (category: string) => void;
  updatePlaylistSelection: (draftIndex: number, indexes: number[]) => void;
  togglePlaylistEntry: (draftIndex: number, playlistIndex: number, selected: boolean) => void;
  rightsConfirmed: boolean;
  setRightsConfirmed: Dispatch<SetStateAction<boolean>>;
  submitting: boolean;
  addDownloads: (event: FormEvent) => void | Promise<void>;
  visibleQueueRows: QueueRow[];
  activeCount: number;
  totalCurrentSpeed: number;
  completedQueueCount: number;
  queuedCount: number;
  jobs: DownloadJob[];
  batchAction: (action: "pause" | "resume") => void | Promise<void>;
  clearCompleted: () => void;
  queuedJobsOrdered: DownloadJob[];
  reorderQueuedJobs: (event: DragEndEvent) => void | Promise<void>;
  reorderPlaylistItems: (job: DownloadJob, event: DragEndEvent) => void | Promise<void>;
  busyAction: string;
  downloadNext: (job: DownloadJob) => void | Promise<void>;
  queueFilter: QueueFilter;
  setQueueFilter: Dispatch<SetStateAction<QueueFilter>>;
  search: string;
  setSearch: Dispatch<SetStateAction<string>>;
  filteredQueue: QueueRow[];
  retryPlaylistItem: (job: DownloadJob, item: QueueItem) => void | Promise<void>;
  saveFile: (job: DownloadJob, fileId?: string) => void | Promise<void>;
  jobAction: (job: DownloadJob, action: QueueJobAction) => void | Promise<void>;
};

export function QueuePage({
  url, setUrl, batchMode, setBatchMode, drafts, setDrafts, serviceReady, inspecting, inspectLinks,
  formError, setFormError, mp3Supported, settings, applyMediaTypeToAll, applyQualityToAll, applyVideoStrategyToAll, applyAudioFormatToAll,
  applyAudioBitrateToAll, applyCategoryToAll, updatePlaylistSelection, togglePlaylistEntry,
  rightsConfirmed, setRightsConfirmed, submitting, addDownloads, visibleQueueRows, activeCount,
  totalCurrentSpeed, completedQueueCount, queuedCount, jobs, batchAction, clearCompleted,
  queuedJobsOrdered, reorderQueuedJobs, reorderPlaylistItems, busyAction, downloadNext, queueFilter,
  setQueueFilter, search, setSearch, filteredQueue, retryPlaylistItem, saveFile, jobAction,
}: QueuePageProps) {
  const queueSensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  return (
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
                      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-6">
                        <select aria-label="Apply media type to all" defaultValue="" onChange={event => { if (event.target.value) applyMediaTypeToAll(event.target.value as Draft["mediaType"]); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Media type…</option><option value="video">All video</option><option value="audio">All eligible audio</option>
                        </select>
                        <select aria-label="Apply quality to all" defaultValue="" onChange={event => { if (event.target.value) applyQualityToAll(event.target.value as Quality); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Video quality…</option>{(Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                        </select>
                        <select aria-label="Apply video format to all" defaultValue="" onChange={event => { if (event.target.value) applyVideoStrategyToAll(event.target.value as Draft["selectedVideoStrategy"]); event.currentTarget.value = ""; }} className={`${field} py-2 text-xs`}>
                          <option value="" disabled>Video format…</option>{(Object.entries(videoStrategyLabels) as [Draft["selectedVideoStrategy"], string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
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
                        </label>
                        <label className="block text-[11px] font-medium text-neutral-400">Video format
                          <select aria-label={`Video format for ${draft.title || `item ${index + 1}`}`} value={draft.selectedVideoStrategy} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, selectedVideoStrategy: event.target.value as Draft["selectedVideoStrategy"] } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            {(Object.entries(videoStrategyLabels) as [Draft["selectedVideoStrategy"], string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                          </select>
                          <span className="mt-1 block text-[10px] leading-relaxed text-neutral-500">{draft.selectedVideoStrategy === "compatibility" ? "Strict MP4 output; may use a lower resolution when H.264/AAC is the highest compatible option." : draft.selectedVideoStrategy === "best" ? "Automatically chooses the highest-quality supported MP4 or WebM representation." : `Prefers ${draft.selectedVideoStrategy.toUpperCase()} WebM; falls back transparently when unavailable.`}</span>

                        <label className="block text-[11px] font-medium text-neutral-400">Captions
                          <select aria-label={`Captions for ${draft.title || `item ${index + 1}`}`} value={draft.subtitleLanguage} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, subtitleLanguage: event.target.value } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            <option value="">None</option>
                            {(draft.captionTracks ?? []).map(track => <option key={track.languageCode} value={track.languageCode}>{track.label}{track.autoGenerated ? " · auto" : ""}</option>)}
                          </select>
                        </label>
                        {draft.subtitleLanguage && <label className="block text-[11px] font-medium text-neutral-400">Caption format
                          <select aria-label={`Caption format for ${draft.title || `item ${index + 1}`}`} value={draft.subtitleFormat} onChange={event => setDrafts(previous => previous.map((item, itemIndex) => itemIndex === index ? { ...item, subtitleFormat: event.target.value as Draft["subtitleFormat"] } : item))} className={`${field} mt-1.5 py-2 text-xs`}>
                            <option value="vtt">WebVTT (.vtt)</option>
                            <option value="srt">SubRip (.srt)</option>
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
                      <input type="checkbox" aria-label="Confirm download rights" checked={rightsConfirmed} onChange={event => setRightsConfirmed(event.target.checked)} className="mt-0.5 size-4 shrink-0 accent-rose-600" />
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

              {queuedJobsOrdered.length > 0 && <div className={`${panel} space-y-3 p-3`}>
                <div className="flex flex-wrap items-start justify-between gap-2"><div><p className="text-[11px] font-bold uppercase tracking-wider text-neutral-300">Queued work order</p><p className="mt-1 text-[10px] text-neutral-500">Drag queued jobs or playlist items. Active downloads stay in place.</p></div><span className="rounded-full bg-neutral-800 px-2 py-1 text-[10px] text-neutral-300">{queuedJobsOrdered.length} queued job{queuedJobsOrdered.length === 1 ? "" : "s"}</span></div>
                <DndContext sensors={queueSensors} collisionDetection={closestCenter} onDragEnd={event => void reorderQueuedJobs(event)}>
                  <SortableContext items={queuedJobsOrdered.map(job => `job:${job.id}`)} strategy={verticalListSortingStrategy}>
                    <div className="space-y-2">
                      {queuedJobsOrdered.map((job, jobIndex) => {
                        const playlistItems = (job.items ?? []).filter(item => (item.playlistIndex ?? 0) > 0);
                        const nested = job.kind === "playlist" && playlistItems.length > 1
                          ? <DndContext sensors={queueSensors} collisionDetection={closestCenter} onDragEnd={event => void reorderPlaylistItems(job, event)}>
                              <SortableContext items={playlistItems.map(item => `item:${job.id}:${item.playlistIndex}`)} strategy={verticalListSortingStrategy}>
                                <div className="space-y-1.5">
                                  {playlistItems.map(item => <SortableQueueOrderRow key={item.playlistIndex} id={`item:${job.id}:${item.playlistIndex}`} label={item.title} detail={`Original playlist #${item.playlistIndex} · queue item ${item.index}`} />)}
                                </div>
                              </SortableContext>
                            </DndContext>
                          : undefined;
                        return <SortableQueueOrderRow key={job.id} id={`job:${job.id}`} label={job.title} detail={`Queue #${jobIndex + 1} · ${job.kind === "playlist" ? `${job.items?.length ?? 0} selected items` : "single video"}`} below={nested}>
                          <button type="button" className={button} disabled={jobIndex === 0 || busyAction === `${job.id}:next`} onClick={() => void downloadNext(job)}>Download next</button>
                        </SortableQueueOrderRow>;
                      })}
                    </div>
                  </SortableContext>
                </DndContext>
              </div>}

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
                  const label = job.mediaType === "audio" ? (job.audioFormat === "m4a" ? "M4A · original AAC" : `MP3 ${job.audioBitrate ?? "192k"}`) : `${qualityLabels[job.quality]} · ${videoStrategyLabels[job.videoStrategy ?? "best"]}`;
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
                        {job.kind === "playlist" && (job.status === "partial" || job.status === "failed" || job.status === "cancelled") && (item.status === "failed" || item.status === "cancelled") && <button type="button" className={button} disabled={busyAction === `${job.id}:retry-item:${item.index}` || item.retryRequested === true} onClick={() => void retryPlaylistItem(job, item)} aria-label={`Retry item ${item.title}`}><RefreshCw className="size-3.5" aria-hidden="true" /><span className="hidden md:inline">{item.retryRequested ? "Retry queued" : "Retry item"}</span></button>}
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
  );
}
