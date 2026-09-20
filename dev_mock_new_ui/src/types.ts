export type DownloadStatus = 
  | 'idle'
  | 'fetching_info'
  | 'queued'
  | 'downloading'
  | 'processing' // merging audio/video or converting
  | 'paused'
  | 'completed'
  | 'failed';

export type MediaType = 'video' | 'audio';

export interface ResolutionOption {
  id: string;
  label: string; // e.g. "2160p (4K UHD) 60fps"
  height: number;
  fps: number;
  format: 'mp4' | 'mkv' | 'webm';
  estimatedSizeMb: number;
  isHdr?: boolean;
}

export interface AudioOption {
  id: string;
  label: string; // e.g. "MP3 (320 kbps)"
  bitrate: string;
  format: 'mp3' | 'm4a' | 'flac' | 'wav';
  estimatedSizeMb: number;
}

export interface VideoItem {
  id: string;
  url: string;
  title: string;
  channel: string;
  channelAvatarUrl: string;
  thumbnailUrl: string;
  duration: string; // e.g. "14:28"
  durationSeconds: number;
  publishDate: string;
  category: 'Tech' | 'Science' | 'Education' | 'Music' | 'Coding' | 'Gaming' | 'General';
  playlistTitle?: string;
  
  // Selected configuration
  mediaType: MediaType;
  selectedResolution: ResolutionOption;
  selectedAudio?: AudioOption;
  customCategory?: string;
  subfolderPath: string;
  fileName: string;

  // Download state
  status: DownloadStatus;
  progress: number; // 0 - 100
  downloadedBytes: number;
  totalBytes: number;
  speedBytesPerSec: number; // e.g. 15_000_000 = ~15 MB/s
  etaSeconds: number;
  downloadedAt?: string;
  error?: string;

  // Available options fetched automatically
  availableResolutions: ResolutionOption[];
  availableAudioOptions: AudioOption[];
}

export interface DownloadSettings {
  downloadLocation: string;
  namingPattern: string; // e.g. "{channel} - {title} [{resolution}]"
  subfolderSorting: 'channel' | 'category' | 'flat' | 'playlist';
  defaultVideoQuality: string; // 'highest' | '1080p' | '720p' | 'audio_only'
  defaultAudioFormat: 'mp3' | 'm4a' | 'flac';
  maxConcurrentDownloads: number;
  speedLimitMbps: number; // 0 = unlimited
  embedThumbnails: boolean;
  embedSubtitles: boolean;
  saveMetadataJson: boolean;
  userCategories: string[];
}

export type ActiveTab = 'queue' | 'library' | 'settings';
