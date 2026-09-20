/**
 * Shared frontend types and helpers for the Go download API. The executable's
 * React UI uses its own origin for the service; address selection lives in the
 * page bootstrap, not in this client module.
 */
export type Quality = "best" | "1080" | "720" | "480";
export type JobStatus = "queued" | "downloading" | "processing" | "paused" | "completed" | "partial" | "failed" | "cancelled";
export interface DownloadFile {
  id: string;
  name: string;
  size: number;
  height?: number;
  mimeType?: string;
  title?: string;
  author?: string;
  durationSeconds?: number;
  thumbnailUrl?: string;
  publishDate?: string;
  category?: string;
  mediaType?: "video" | "audio";
  outputName?: string;
  outputRelativePath?: string;
}
export interface QueueItem {
  index: number;
  videoId?: string;
  title: string;
  author?: string;
  durationSeconds?: number;
  thumbnailUrl?: string;
  status: JobStatus;
  progress: number | null;
  downloadedBytes: number;
  totalBytes: number;
  speedBytesPerSec: number;
  etaSeconds: number;
  error?: string;
  fileId?: string;
}
export interface DownloadJob {
  id: string;
  url: string;
  kind: "video" | "playlist";
  quality: Quality;
  mediaType: "video" | "audio";
  audioBitrate?: string;
  status: JobStatus;
  title: string;
  progress: number | null;
  currentItem: string;
  completedCount: number;
  totalCount: number | null;
  files: DownloadFile[];
  items?: QueueItem[];
  error: string;
  createdAt: string;
  note?: string;
  failures?: { index: number; error: string }[];
  downloadedBytes: number;
  totalBytes: number;
  speedBytesPerSec: number;
  etaSeconds: number;
  activeItemCount?: number;
}
export interface ServiceHealth {
  ready: boolean;
  missing: string[];
  engine?: string;
  capabilities?: {
    combinedStreamsOnly: boolean;
    adaptiveStreamsSupported?: boolean;
    externalBinariesRequired: boolean;
    mp3AudioSupported?: boolean;
    maxConcurrentDownloads?: number;
  };
}
export interface ServiceConnection {
  base: string;
  token: string;
}

export interface AppSettings {
  defaultQuality: Quality;
  maxConcurrentDownloads: number;
  downloadLocation: string;
  namingPattern: string;
  subfolderSorting: "channel" | "category" | "flat";
  defaultCategory: string;
  userCategories: string[];
}

export interface InspectedQuality {
  value: Quality;
  label: string;
  height: number;
}

export interface InspectedItem {
  index?: number;
  id: string;
  title: string;
  author?: string;
  durationSeconds?: number;
  thumbnailUrl?: string;
}

export interface Inspection {
  url: string;
  kind: "video" | "playlist";
  title: string;
  author?: string;
  durationSeconds?: number;
  thumbnailUrl?: string;
  publishDate?: string;
  availableQualities?: InspectedQuality[];
  audioOnlyAvailable?: boolean;
  itemCount?: number;
  items?: InspectedItem[];
  entries?: InspectedItem[];
  note?: string;
}

const videoID = /^[A-Za-z0-9_-]{11}$/;
const playlistID = /^[A-Za-z0-9_-]{2,200}$/;
const hosts = new Set(["youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be", "www.youtu.be"]);

/** Validate a supported HTTPS YouTube link and return its canonical video or playlist URL. */
export function parseYouTubeURL(value: string): { canonical: string; kind: "video" | "playlist" } {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new Error("Enter a complete YouTube URL beginning with https://.");
  }
  if (url.protocol !== "https:" || !hosts.has(url.hostname) || url.username || url.password || url.port) {
    throw new Error("Use an HTTPS link from youtube.com or youtu.be.");
  }
  const short = url.hostname === "youtu.be" || url.hostname === "www.youtu.be";
  const parts = url.pathname.split("/").filter(Boolean);
  const show = !short && parts.length === 2 && parts[0] === "show";
  const pathAllowed = short
    ? parts.length === 1
    : url.pathname === "/watch" || url.pathname === "/playlist" || show || (parts.length === 2 && ["shorts", "live", "embed"].includes(parts[0]));
  if (!pathAllowed) throw new Error("Paste a video or playlist link, not a channel or search page.");
  if (show) {
    const list = parts[1].startsWith("VL") ? parts[1].slice(2) : "";
    if (!playlistID.test(list)) throw new Error("This playlist ID is not valid.");
    return { canonical: `https://www.youtube.com/playlist?list=${encodeURIComponent(list)}`, kind: "playlist" };
  }
  const list = url.searchParams.get("list");
  if (list !== null) {
    if (!playlistID.test(list)) throw new Error("This playlist ID is not valid.");
    return { canonical: `https://www.youtube.com/playlist?list=${encodeURIComponent(list)}`, kind: "playlist" };
  }
  const id = short ? parts[0] : url.pathname === "/watch" ? url.searchParams.get("v") : parts[1];
  if (!id || !videoID.test(id)) throw new Error("This link does not contain a valid video or playlist ID.");
  return { canonical: `https://www.youtube.com/watch?v=${id}`, kind: "video" };
}

/** Validate and normalize a standalone Go service origin; the bundled UI does not call this helper. */
export function serviceURL(value: string): string {
  let url: URL;
  try { url = new URL(value.trim()); } catch { throw new Error("Enter the complete address of your Go download service."); }
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if ((url.protocol !== "https:" && !(local && url.protocol === "http:")) || url.username || url.password || url.search || url.hash || (url.pathname !== "/" && url.pathname !== "")) {
    throw new Error("Use an HTTPS service origin, or HTTP on localhost. Do not include a path or credentials.");
  }
  return url.origin;
}

/**
 * Call a Go API route without browser cookies. Adds JSON content type for a
 * body and bearer authorization only when the supplied connection has a token;
 * 204 responses resolve to `undefined`, while other responses must be JSON.
 */
export async function api<T>(connection: ServiceConnection, path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (connection.token) headers.set("Authorization", `Bearer ${connection.token}`);
  if (init.body) headers.set("Content-Type", "application/json");
  const response = await fetch(`${connection.base}${path}`, { ...init, headers, credentials: "omit" });
  if (response.status === 204) return undefined as T;
  const payload: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const error = payload && typeof payload === "object" && "error" in payload ? String(payload.error) : `The download service could not complete this request (${response.status}).`;
    throw new Error(error);
  }
  if (!payload) throw new Error("The service returned an unexpected response.");
  return payload as T;
}

export function isActive(job: DownloadJob): boolean {
  return job.status === "queued" || job.status === "downloading" || job.status === "processing";
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[index]}`;
}
