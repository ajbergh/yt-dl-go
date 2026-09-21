import {
  ArrowDownToLine, FileText, Film, Folder, FolderTree, HardDrive, Play, RefreshCw, Search, Trash2,
} from "lucide-react";
import type { DownloadFile, DownloadJob, ServiceConnection } from "../lib/downloader";
import {
  LibraryThumbnail, button, dateLabel, formatBytes, panel, type LibraryFilter,
} from "../components/downloader/view-model";

type LibraryStats = {
  files: number;
  logicalBytes: number;
  managedBytes: number;
  publishedBytes: number;
};

type LibraryJobAction = "remove" | "delete-managed" | "delete-published" | "delete-all";

type LibraryPageProps = {
  visibleLibraryJobs: DownloadJob[];
  librarySearch: string;
  setLibrarySearch: (value: string) => void;
  libraryFilter: LibraryFilter;
  setLibraryFilter: (value: LibraryFilter) => void;
  libraryCategory: string;
  setLibraryCategory: (value: string) => void;
  libraryChannel: string;
  setLibraryChannel: (value: string) => void;
  libraryCategories: [string, number][];
  libraryChannels: [string, number][];
  libraryLayout: "grid" | "list";
  setLibraryLayout: (value: "grid" | "list") => void;
  libraryStats: LibraryStats;
  connection: ServiceConnection;
  busyAction: string;
  previewFile: (job: DownloadJob, file: DownloadFile) => void | Promise<void>;
  saveFile: (job: DownloadJob, fileId?: string) => void | Promise<void>;
  filesystemAction: (job: DownloadJob, fileId: string, action: "copy-path" | "reveal" | "open-folder") => void | Promise<void>;
  jobAction: (job: DownloadJob, action: LibraryJobAction) => void | Promise<void>;
};

export function LibraryPage({
  visibleLibraryJobs, librarySearch, setLibrarySearch, libraryFilter, setLibraryFilter,
  libraryCategory, setLibraryCategory, libraryChannel, setLibraryChannel, libraryCategories,
  libraryChannels, libraryLayout, setLibraryLayout, libraryStats, connection, busyAction,
  previewFile, saveFile, filesystemAction, jobAction,
}: LibraryPageProps) {
  return (
<section aria-labelledby="library-heading" className="space-y-4">
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
                    <div className="min-w-0 flex-1"><h4 className="truncate text-xs font-semibold text-neutral-200" title={file.title || file.outputName || file.name}>{file.title || file.outputName || file.name}</h4><p className="mt-1 truncate font-mono text-[10px] text-neutral-500" title={file.outputRelativePath || file.author || file.name}>{file.outputRelativePath || file.author || file.name}</p><p className="mt-1 text-[10px] text-neutral-600">{file.height ? `${file.height}p · ` : ""}{formatBytes(file.size)}{file.durationSeconds ? ` · ${durationLabel(file.durationSeconds)}` : ""}{file.subtitle ? ` · CC ${file.subtitle.label || file.subtitle.languageCode} ${file.subtitle.format.toUpperCase()}` : ""}</p><div className="mt-1 flex flex-wrap gap-1">{file.subtitleError && <span className="rounded bg-amber-950/60 px-1.5 py-0.5 text-[9px] text-amber-300" title={file.subtitleError}>Caption warning</span>}{file.managedAvailable === false && <span className="rounded bg-amber-950/60 px-1.5 py-0.5 text-[9px] text-amber-300">Managed copy removed</span>}{file.outputRelativePath && file.publishedAvailable === false && <span className="rounded bg-red-950/50 px-1.5 py-0.5 text-[9px] text-red-300">Published copy removed</span>}</div></div>
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
        </section>
  );
}
