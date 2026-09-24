import { useState, type Dispatch, type SetStateAction } from "react";
import type { DragEndEvent } from "@dnd-kit/core";
import { arrayMove } from "@dnd-kit/sortable";
import {
  api,
  type DownloadFile,
  type DownloadJob,
  type QueueItem,
  type ServiceConnection,
} from "../lib/downloader";
import {
  clearedQueueItemsKey,
  errorMessage,
  queueItemKey,
  type QueueRow,
} from "../components/downloader/view-model";

export type JobAction = "pause" | "resume" | "cancel" | "retry" | "remove" | "forget-history" | "delete-managed" | "delete-published" | "delete-all";

export function mergeJobActionResult(current: DownloadJob, result: DownloadJob, action: JobAction): DownloadJob {
  // Pause/cancel responses are acknowledgements: the worker reaches its final
  // state asynchronously and can publish that newer state over SSE before the
  // HTTP response is applied. Never let the older acknowledgement regress it.
  if (action === "pause" && current.status === "paused" && result.status !== "paused") return current;
  if (action === "cancel" && current.status === "cancelled" && result.status !== "cancelled") return current;

  // Resume returns "queued", but the scheduler may already have started the
  // job and published a newer active/terminal state by the time HTTP resolves.
  if (action === "resume" && result.status === "queued"
      && ["downloading", "processing", "completed", "partial", "failed", "cancelled"].includes(current.status)) {
    return current;
  }
  return result;
}

type UseJobsOptions = {
  connection: ServiceConnection;
  serviceReady: boolean;
  jobs: DownloadJob[];
  setJobs: Dispatch<SetStateAction<DownloadJob[]>>;
  refreshLibrary: () => Promise<DownloadJob[]>;
  queuedJobsOrdered: DownloadJob[];
  setNotice: Dispatch<SetStateAction<string>>;
};

export function useJobs({
  connection, serviceReady, jobs, setJobs, queuedJobsOrdered, setNotice, refreshLibrary,
}: UseJobsOptions) {
  const [busyAction, setBusyAction] = useState("");
  const [actionError, setActionError] = useState("");
  const [preview, setPreview] = useState<{ title: string; url: string; mimeType: string; chapters: NonNullable<DownloadFile["chapters"]> } | null>(null);
  const [clearedQueueItems, setClearedQueueItems] = useState<string[]>(() => {
    try {
      const stored = window.localStorage.getItem(clearedQueueItemsKey);
      const parsed: unknown = stored ? JSON.parse(stored) : [];
      return Array.isArray(parsed) && parsed.every(item => typeof item === "string") ? parsed : [];
    } catch {
      return [];
    }
  });

  async function jobAction(job: DownloadJob, action: JobAction) {
    if (!serviceReady) return;
    if (action === "retry" && !window.confirm("Retry this URL? Confirm that you own the content or have permission to download it.")) return;
    if (action === "remove" && !window.confirm("Remove this item from the Library? The app-managed copy and history will be removed, but published files in your configured download folder will be preserved.")) return;
    if (action === "forget-history" && !window.confirm("Remove finished job history from the Queue? Library items and media files will remain available.")) return;
    if (action === "delete-managed" && !window.confirm("Delete the app-managed media copy? Published files in your configured download folder will be preserved, but in-app Save links will no longer work.")) return;
    if (action === "delete-published" && !window.confirm("Delete the published media from your configured download folder? The app-managed Library copy will be preserved.")) return;
    if (action === "delete-all" && !window.confirm("Delete this media everywhere? This removes the app-managed copy, published output files, and Library history. This cannot be undone.")) return;

    setBusyAction(job.id);
    setActionError("");
    try {
      if (action === "remove" || action === "forget-history" || action === "delete-all") {
        const suffix = action === "delete-all" ? "/all" : action === "forget-history" ? "/history" : "";
        await api<void>(connection, `/api/jobs/${encodeURIComponent(job.id)}${suffix}`, {
          method: "DELETE",
          signal: AbortSignal.timeout(15000),
        });
        setJobs(previous => previous.filter(item => item.id !== job.id));
        setNotice(action === "delete-all"
          ? "Media, published output, and Library history deleted."
          : action === "forget-history"
            ? "Job history removed. Library items and media remain available."
            : "Removed from Library. Published output files were preserved.");
      } else if (action === "delete-managed" || action === "delete-published") {
        const scope = action === "delete-managed" ? "managed" : "published";
        const result = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/${scope}`, {
          method: "DELETE",
          signal: AbortSignal.timeout(15000),
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
          : previous.map(item => item.id === job.id ? mergeJobActionResult(item, result, action) : item));
      }
      void refreshLibrary().catch(error => setActionError(errorMessage(error)));
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
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
      void refreshLibrary().catch(error => setActionError(errorMessage(error)));
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
        chapters: file.chapters ?? [],
      });
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function saveFile(job: DownloadJob, fileId?: string | string[]) {
    if (!serviceReady) return;
    const key = `${job.id}:${fileId ?? "zip"}`;
    setBusyAction(key);
    setActionError("");
    try {
      const ticket = await api<{ path: string }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/ticket`, {
        method: "POST",
        body: JSON.stringify(Array.isArray(fileId) ? { fileIds: fileId } : fileId ? { fileId } : {}),
        signal: AbortSignal.timeout(15000),
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
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function filesystemAction(job: DownloadJob, fileId: string, action: "copy-path" | "reveal" | "open-folder") {
    if (!serviceReady) return;
    const key = `${job.id}:${fileId}:${action}`;
    setBusyAction(key);
    setActionError("");
    try {
      const result = await api<{ path: string }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/filesystem`, {
        method: "POST",
        body: JSON.stringify({ fileId, action }),
        signal: AbortSignal.timeout(15000),
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
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
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
        method: "PUT",
        body: JSON.stringify({ jobIds: reordered.map(job => job.id) }),
        signal: AbortSignal.timeout(15000),
      });
      mergeQueuedSnapshots(result.jobs);
      setNotice("Queued job order updated.");
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function downloadNext(job: DownloadJob) {
    if (job.status !== "queued") return;
    setBusyAction(`${job.id}:next`);
    setActionError("");
    try {
      const result = await api<{ jobs: DownloadJob[] }>(connection, `/api/jobs/${encodeURIComponent(job.id)}/next`, {
        method: "POST",
        signal: AbortSignal.timeout(15000),
      });
      mergeQueuedSnapshots(result.jobs);
      setNotice(`"${job.title}" will be the next queued job to start.`);
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
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
        method: "PUT",
        body: JSON.stringify({ playlistIndexes: reordered.map(item => item.playlistIndex) }),
        signal: AbortSignal.timeout(15000),
      });
      setJobs(previous => previous.map(item => item.id === job.id ? updated : item));
      setNotice(`Playlist queue order updated for "${job.title}".`);
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function batchAction(action: "pause" | "resume") {
    const eligible = jobs.filter(job => action === "pause"
      ? job.status === "queued" || job.status === "downloading" || job.status === "processing"
      : job.status === "paused");
    for (const job of eligible) await jobAction(job, action);
  }

  function clearCompleted(rows: QueueRow[]) {
    const completedItems = rows.filter(row => row.item.status === "completed").map(queueItemKey);
    if (!serviceReady || completedItems.length === 0) return;
    const updated = [...new Set([...clearedQueueItems, ...completedItems])];
    setClearedQueueItems(updated);
    try {
      window.localStorage.setItem(clearedQueueItemsKey, JSON.stringify(updated));
    } catch {
      // Keep the current queue cleared for this session.
    }
    setNotice("Completed downloads cleared from the queue. Your files remain in the library.");
  }

  return {
    busyAction,
    actionError,
    preview,
    setPreview,
    clearedQueueItems,
    jobAction,
    retryPlaylistItem,
    previewFile,
    saveFile,
    filesystemAction,
    reorderQueuedJobs,
    downloadNext,
    reorderPlaylistItems,
    batchAction,
    clearCompleted,
  };
}
