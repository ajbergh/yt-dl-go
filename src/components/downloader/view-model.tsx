import { useEffect, useState, type ReactNode } from "react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Activity, Film, GripVertical } from "lucide-react";
import {
  apiBlob, formatBytes,
  type DownloadFile, type DownloadJob, type Inspection, type QueueItem, type Quality,
  type ServiceConnection, type VideoStrategy,
} from "../../lib/downloader";

export type Tab = "queue" | "library" | "settings";
export type Draft = Inspection & {
  selectedQuality: Quality;
  selectedVideoStrategy: VideoStrategy;
  mediaType: "video" | "audio";
  audioFormat: "mp3" | "m4a";
  audioBitrate: string;
  subtitleLanguage: string;
  subtitleFormat: "vtt" | "srt";
  category: string;
  selectedPlaylistIndexes: number[];
  playlistExpanded: boolean;
};
export type QueueFilter = "all" | "active" | "queued" | "completed";
export type LibraryFilter = "all" | "video" | "audio";
export type QueueRow = { job: DownloadJob; item: QueueItem };

export const clearedQueueItemsKey = "yt-dl-go:cleared-completed-queue-items";
export const libraryLayoutKey = "yt-dl-go:library-layout";

export function queueItemKey({ job, item }: QueueRow): string {
  return `${job.id}:${item.index}`;
}

export const qualityLabels: Record<Quality, string> = {
  best: "Best available",
  "2160": "Up to 2160p (4K)",
  "1440": "Up to 1440p",
  "1080": "Up to 1080p",
  "720": "Up to 720p",
  "480": "Up to 480p",
};

export const videoStrategyLabels: Record<VideoStrategy, string> = {
  best: "Best quality · automatic",
  compatibility: "Compatibility MP4 · H.264/AAC",
  vp9: "Prefer VP9 · WebM",
  av1: "Prefer AV1 · WebM",
};

export const statusLabels: Record<DownloadJob["status"], string> = {
  queued: "Queued",
  downloading: "Downloading",
  processing: "Converting to MP3",
  paused: "Paused",
  completed: "Completed",
  partial: "Partial",
  failed: "Failed",
  cancelled: "Cancelled",
};

export const panel = "rounded-2xl border border-neutral-800 bg-neutral-900/70";
export const button = "inline-flex min-h-9 items-center justify-center gap-2 rounded-lg border border-neutral-700 bg-neutral-800 px-3 py-2 text-xs font-semibold text-neutral-200 transition-colors hover:border-neutral-600 hover:bg-neutral-700 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-rose-500 disabled:cursor-not-allowed disabled:opacity-50";
export const primaryButton = "inline-flex min-h-10 items-center justify-center gap-2 rounded-lg bg-rose-600 px-4 py-2 text-xs font-bold text-white transition-colors hover:bg-rose-500 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-rose-400 disabled:cursor-not-allowed disabled:opacity-50";
export const field = "w-full rounded-xl border border-neutral-700 bg-neutral-950 px-3.5 py-2.5 text-sm text-neutral-100 outline-none placeholder:text-neutral-500 focus:border-rose-500 focus:ring-1 focus:ring-rose-500";

export function errorMessage(error: unknown): string {
  return error instanceof TypeError
    ? "Could not reach the built-in Go service. Retrying automatically."
    : error instanceof Error ? error.message : "Something went wrong. Please try again.";
}

export function notificationAPI(): typeof Notification | null {
  return typeof window !== "undefined" && "Notification" in window ? window.Notification : null;
}

export function terminalNotification(job: DownloadJob): { title: string; body: string } | null {
  const error = (job.error || "").trim();
  if ((job.status === "failed" || job.status === "partial") && /storage|output|disk|filesystem/i.test(error)) {
    return { title: "Download storage error", body: `${job.title}: ${error || "A storage/output error stopped the download."}`.slice(0, 240) };
  }
  if (job.status === "completed") {
    return {
      title: job.kind === "playlist" ? "Batch completed" : "Download completed",
      body: `${job.title} finished successfully.`.slice(0, 240),
    };
  }
  if (job.status === "partial") {
    const count = job.totalCount == null ? `${job.completedCount} files completed` : `${job.completedCount} of ${job.totalCount} files completed`;
    return { title: "Playlist partially completed", body: `${job.title}: ${count}. ${error}`.trim().slice(0, 240) };
  }
  if (job.status === "failed") {
    return { title: "Download failed", body: `${job.title}: ${error || "The download could not be completed."}`.slice(0, 240) };
  }
  return null;
}

export function durationLabel(seconds?: number): string {
  if (!seconds || seconds < 0) return "Duration unavailable";
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainder = Math.floor(seconds % 60);
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(remainder).padStart(2, "0")}`
    : `${minutes}:${String(remainder).padStart(2, "0")}`;
}

export function dateLabel(value?: string): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleDateString();
}

export const inspectedVideoID = /^[A-Za-z0-9_-]{11}$/;

export function playlistEntries(draft: Draft) {
  return draft.entries ?? draft.items ?? [];
}

export function selectablePlaylistEntries(draft: Draft) {
  return playlistEntries(draft).filter(item =>
    Number.isInteger(item.index) && (item.index ?? 0) > 0 && inspectedVideoID.test(item.id),
  );
}

export function selectedPlaylistEntries(draft: Draft) {
  const selected = new Set(draft.selectedPlaylistIndexes);
  return selectablePlaylistEntries(draft).filter(item => selected.has(item.index!));
}

export function approximateMP3Bytes(draft: Draft): number {
  if (draft.kind !== "playlist" || draft.mediaType !== "audio" || draft.audioFormat !== "mp3") return 0;
  const bitrate = Number.parseInt(draft.audioBitrate, 10);
  if (!Number.isFinite(bitrate) || bitrate <= 0) return 0;
  const seconds = selectedPlaylistEntries(draft).reduce((sum, item) => sum + Math.max(0, item.durationSeconds ?? 0), 0);
  return Math.round(seconds * bitrate * 1000 / 8);
}

export function statusClass(status: DownloadJob["status"]): string {
  if (status === "completed") return "border-emerald-700/60 bg-emerald-950/50 text-emerald-300";
  if (status === "downloading" || status === "processing") return "border-rose-700/60 bg-rose-950/50 text-rose-300";
  if (status === "paused") return "border-amber-700/60 bg-amber-950/50 text-amber-300";
  if (status === "failed" || status === "partial") return "border-red-800/60 bg-red-950/40 text-red-300";
  return "border-neutral-700 bg-neutral-800 text-neutral-300";
}

export function SortableQueueOrderRow({ id, label, detail, children, below }: { id: string; label: string; detail?: string; children?: ReactNode; below?: ReactNode }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id });
  return <div ref={setNodeRef} style={{ transform: CSS.Transform.toString(transform), transition }} className={`rounded-lg border border-neutral-800 bg-neutral-950/80 p-2 ${isDragging ? "z-20 opacity-70 shadow-xl" : ""}`}>
    <div className="flex items-center gap-2">
      <button type="button" aria-label={`Drag ${label}`} title="Drag to reorder" className="grid size-8 shrink-0 cursor-grab place-items-center rounded-md border border-neutral-800 bg-neutral-900 text-neutral-500 hover:text-neutral-200 active:cursor-grabbing" {...attributes} {...listeners}><GripVertical className="size-4" aria-hidden="true" /></button>
      <div className="min-w-0 flex-1"><p className="truncate text-[11px] font-semibold text-neutral-200">{label}</p>{detail && <p className="mt-0.5 truncate text-[10px] text-neutral-500">{detail}</p>}</div>
      {children}
    </div>
    {below && <div className="mt-2 border-t border-neutral-800 pt-2">{below}</div>}
  </div>;
}

export function LibraryThumbnail({ connection, jobId, file, audio }: { connection: ServiceConnection; jobId: string; file: DownloadFile; audio: boolean }) {
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

export function durationMetric(job: DownloadJob): string {
  if (job.totalCount === null) return `${job.completedCount} finished`;
  return `${job.completedCount} / ${job.totalCount} files`;
}

export function queueItemsFor(job: DownloadJob): QueueItem[] {
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

export function etaLabel(seconds: number): string {
  if (seconds <= 0) return "Estimating…";
  const minutes = Math.floor(seconds / 60);
  return minutes > 0 ? `${minutes}m ${seconds % 60}s left` : `${seconds}s left`;
}

export function builtInServiceConnection(): ServiceConnection {
  // `npm run dev` fixes Vite at port 5173 and runs Go separately at 8080.
  // The packaged executable serves both UI and API from the current origin.
  // Vite preview/custom ports are not a supported connection mode.
  const base = window.location.port === "5173"
    ? "http://127.0.0.1:8080"
    : window.location.origin;
  return { base, token: "" };
}

export { formatBytes };
