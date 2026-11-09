import { userFacingMessage } from "../lib/messages";
import { useRef, useState, type Dispatch, type SetStateAction } from "react";
import type { DragEndEvent } from "@dnd-kit/core";
import { arrayMove } from "@dnd-kit/sortable";
import { confirmAction, desktopBridge } from "../lib/desktop";
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

export type JobAction = "pause" | "resume" | "cancel" | "retry" | "remove" | "remove-library-item" | "delete-library-file" | "forget-history" | "delete-all";
export type JobConfigurationPatch = Partial<Pick<DownloadJob, "quality" | "videoStrategy" | "audioFormat" | "audioBitrate" | "category">>;

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

  const confirmationPending = useRef(false);

  async function confirmJobAction(message: string): Promise<boolean> {
    if (confirmationPending.current) return false;
    confirmationPending.current = true;
    try {
      return await confirmAction(message);
    } catch (error) {
      setActionError(errorMessage(error));
      return false;
    } finally {
      confirmationPending.current = false;
    }
  }

  async function jobAction(job: DownloadJob, action: JobAction, fileId?: string) {
    if (!serviceReady) return;
    if (action === "retry" && !(await confirmJobAction("Retry this URL? Confirm that you own the content or have permission to download it."))) return;
    if (action === "remove" && !(await confirmJobAction("Remove this item from the Vault? History will be removed. Files in your download folder will be preserved."))) return;
    if (action === "remove-library-item" && !(await confirmJobAction("Remove this file from the Vault? The file in your download folder and the job history will remain."))) return;
    if (action === "delete-library-file") {
      const item = job.files.find(file => file.id === fileId);
      if (!item || !(await confirmJobAction(`Permanently delete ONLY "${item.title || item.outputName || item.name}" from your download folder? Other files in this download and the job history will be preserved. This cannot be undone.`))) return;
    }
    if (action === "forget-history" && !(await confirmJobAction("Remove finished job history from the Queue? Vault items and media files will remain available."))) return;
    if (action === "delete-all" && !(await confirmJobAction("Permanently delete ALL files belonging to this download, including media on Vault pages you have not loaded? Files and Vault history will be removed. This cannot be undone."))) return;

    setBusyAction(job.id);
    setActionError("");
    try {
      if (action === "remove-library-item" || action === "delete-library-file") {
        if (!fileId) throw new Error("A Vault file ID is required.");
        await api<void>(connection, `/api/jobs/${encodeURIComponent(job.id)}/${action === "delete-library-file" ? "library-files" : "library-items"}/${encodeURIComponent(fileId)}`, {
          method: "DELETE",
          signal: AbortSignal.timeout(15000),
        });
        setNotice(action === "delete-library-file"
          ? "Selected file permanently deleted. Other media in this download and job history were preserved."
          : "Removed this file from the Vault. Job history and downloaded files were preserved.");
      } else if (action === "remove" || action === "forget-history" || action === "delete-all") {
        const suffix = action === "delete-all" ? "/all" : action === "forget-history" ? "/history" : "";
        await api<void>(connection, `/api/jobs/${encodeURIComponent(job.id)}${suffix}`, {
          method: "DELETE",
          signal: AbortSignal.timeout(15000),
        });
        setJobs(previous => previous.filter(item => item.id !== job.id));
        setNotice(action === "delete-all"
          ? "Files and Vault history deleted."
          : action === "forget-history"
            ? "Job history removed. Vault items and media remain available."
            : "Removed from Vault. Downloaded files were preserved.");
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

  // Library-only bulk removal never unlinks downloaded files. Individual
  // failures remain selected and are reported; successes are refreshed once.
  async function bulkForgetLibraryItems(items: { jobId: string; fileId: string }[]): Promise<string[] | null> {
    const unique = [...new Map(items.filter(item => item.jobId && item.fileId).map(item => [
      item.jobId + ":" + item.fileId, item,
    ])).values()];
    if (!serviceReady || busyAction || unique.length === 0 || unique.length > 100) return null;
    if (!(await confirmJobAction(`Forget ${unique.length} selected Vault file${unique.length === 1 ? "" : "s"}? Only their Vault index entries will be removed. Downloaded files and job history remain on disk. This cannot be undone in the Vault without reimporting.`))) return null;
    setBusyAction("bulk-library");
    setActionError("");
    const removed: string[] = [];
    const failed: string[] = [];
    try {
      for (const item of unique) {
        const key = item.jobId + ":" + item.fileId;
        try {
          await api<void>(connection, `/api/jobs/${encodeURIComponent(item.jobId)}/library-items/${encodeURIComponent(item.fileId)}`, {
            method: "DELETE",
            signal: AbortSignal.timeout(15000),
          });
          removed.push(key);
        } catch (error) {
          failed.push(`${key}: ${errorMessage(error)}`);
        }
      }
      setNotice(`Removed ${removed.length} selected Vault entr${removed.length === 1 ? "y" : "ies"}; downloaded files and job history were preserved.` +
        (failed.length ? ` ${failed.length} could not be removed.` : ""));
      if (failed.length) setActionError(failed.map(message => userFacingMessage(message)).join("; "));
      try {
        await refreshLibrary();
      } catch (error) {
        setActionError(errorMessage(error));
      }
      return removed;
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

  async function retryAllFailedItems(job: DownloadJob) {
    if (!serviceReady || job.kind !== "playlist" || !["partial", "failed", "cancelled"].includes(job.status)) return;
    if (!(await confirmJobAction("Retry every failed or cancelled playlist item? Confirm that you own the content or have permission to download it."))) return;
    setBusyAction(`${job.id}:retry-failed`);
    setActionError("");
    try {
      const updated = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/retry-failed`, {
        method: "POST",
        body: JSON.stringify({}),
        signal: AbortSignal.timeout(15000),
      });
      setJobs(previous => previous.map(value => value.id === job.id ? updated : value));
      void refreshLibrary().catch(error => setActionError(errorMessage(error)));
      setNotice(`Retrying failed items in "${job.title}". Successful playlist files are preserved.`);
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function updateJobConfiguration(job: DownloadJob, patch: JobConfigurationPatch) {
    if (!serviceReady || (job.status !== "queued" && job.status !== "paused")) return;
    setBusyAction(`${job.id}:configuration`);
    setActionError("");
    try {
      const updated = await api<DownloadJob>(connection, `/api/jobs/${encodeURIComponent(job.id)}/configuration`, {
        method: "PUT",
        body: JSON.stringify(patch),
        signal: AbortSignal.timeout(15000),
      });
      setJobs(previous => previous.map(value => value.id === job.id ? updated : value));
      void refreshLibrary().catch(error => setActionError(errorMessage(error)));
      setNotice(`Updated settings for "${job.title}".`);
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusyAction("");
    }
  }

  async function bulkJobAction(action: "pause" | "resume" | "cancel" | "delete", jobIds: string[]): Promise<string[] | null> {
    const uniqueIDs = [...new Set(jobIds)];
    if (!serviceReady || uniqueIDs.length === 0) return null;
    if (action === "cancel" && !(await confirmJobAction(`Cancel ${uniqueIDs.length} selected job${uniqueIDs.length === 1 ? "" : "s"}?`))) return null;
    if (action === "delete" && !(await confirmJobAction(`Remove ${uniqueIDs.length} selected job${uniqueIDs.length === 1 ? "" : "s"} from the Vault? History will be removed; downloaded files will remain.`))) return null;
    setBusyAction("bulk");
    setActionError("");
    try {
      const deleted = new Set<string>();
      const failures: { jobId: string; error: string }[] = [];
      let updatedCount = 0;
      for (let offset = 0; offset < uniqueIDs.length; offset += 100) {
        const batch = uniqueIDs.slice(offset, offset + 100);
        try {
          const result = await api<{ jobs: DownloadJob[]; deletedIds?: string[]; failures?: { jobId: string; error: string }[] }>(connection, "/api/jobs/bulk", {
            method: "POST",
            body: JSON.stringify({ action, jobIds: batch }),
            signal: AbortSignal.timeout(30000),
          });
          const batchDeleted = new Set(result.deletedIds ?? []);
          for (const id of batchDeleted) deleted.add(id);
          const updates = new Map(result.jobs.map(job => [job.id, job]));
          setJobs(previous => previous.flatMap(value => {
            if (batchDeleted.has(value.id)) return [];
            const updated = updates.get(value.id);
            return updated && action !== "delete" ? [mergeJobActionResult(value, updated, action)] : [value];
          }));
          updatedCount += result.jobs.length;
          failures.push(...result.failures ?? []);
        } catch (error) {
          failures.push(...batch.map(jobId => ({ jobId, error: errorMessage(error) })));
        }
      }
      setNotice(action === "delete"
        ? `Removed ${deleted.size} job${deleted.size === 1 ? "" : "s"} from the Vault${failures.length ? `; ${failures.length} could not be removed` : ""}.`
        : failures.length
          ? `${uniqueIDs.length - failures.length} selected jobs updated; ${failures.length} could not be changed.`
          : `Updated ${updatedCount} selected jobs.`);
      if (failures.length) setActionError(failures.map(failure => `${failure.jobId}: ${userFacingMessage(failure.error)}`).join("; "));
      void refreshLibrary().catch(error => setActionError(errorMessage(error)));
      return [...deleted];
    } catch (error) {
      setActionError(errorMessage(error));
      return null;
    } finally {
      setBusyAction("");
    }
  }

  async function previewFile(job: DownloadJob, file: DownloadFile) {
    if (!serviceReady || file.publishedAvailable === false) return;
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
      const desktop = desktopBridge();
      if (desktop) {
        const saved = await desktop.Save(ticket.path);
        setNotice(saved ? "Download saved." : "Save cancelled.");
        return;
      }
      const anchor = document.createElement("a");
      anchor.href = `${connection.base}${ticket.path}`;
      anchor.rel = "noopener noreferrer";
      anchor.referrerPolicy = "no-referrer";
      anchor.target = "_blank";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      setNotice("Your file is ready to save.");
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
          setNotice("Vault file path copied to the clipboard.");
        } catch {
          setNotice(`Vault file path: ${result.path}`);
        }
      } else {
        setNotice(action === "reveal" ? "Opened the vault file location." : "Opened the vault folder.");
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
    await bulkJobAction(action, eligible.map(job => job.id));
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
    setNotice("Completed downloads cleared from the queue. Your files remain in the vault.");
  }

  return {
    busyAction,
    actionError,
    preview,
    setPreview,
    clearedQueueItems,
    jobAction,
    retryPlaylistItem,
    retryAllFailedItems,
    updateJobConfiguration,
    bulkJobAction,
    bulkForgetLibraryItems,
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
