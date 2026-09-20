import { VideoItem, ResolutionOption, AudioOption, DownloadSettings } from '../types';

export const DEFAULT_RESOLUTIONS: ResolutionOption[] = [
  { id: '2160p', label: '2160p (4K UHD) 60fps', height: 2160, fps: 60, format: 'mp4', estimatedSizeMb: 1450, isHdr: true },
  { id: '1440p', label: '1440p (2K QHD) 60fps', height: 1440, fps: 60, format: 'mp4', estimatedSizeMb: 780 },
  { id: '1080p', label: '1080p (Full HD) 60fps', height: 1080, fps: 60, format: 'mp4', estimatedSizeMb: 340 },
  { id: '720p', label: '720p (HD) 60fps', height: 720, fps: 60, format: 'mp4', estimatedSizeMb: 180 },
  { id: '480p', label: '480p (Standard)', height: 480, fps: 30, format: 'mp4', estimatedSizeMb: 95 },
  { id: '360p', label: '360p (Data Saver)', height: 360, fps: 30, format: 'mp4', estimatedSizeMb: 52 },
];

export const DEFAULT_AUDIO_OPTIONS: AudioOption[] = [
  { id: 'mp3-320', label: 'MP3 (320 kbps Extreme)', bitrate: '320k', format: 'mp3', estimatedSizeMb: 24 },
  { id: 'mp3-256', label: 'MP3 (256 kbps High)', bitrate: '256k', format: 'mp3', estimatedSizeMb: 18 },
  { id: 'm4a-256', label: 'M4A / AAC (256 kbps Apple Audio)', bitrate: '256k', format: 'm4a', estimatedSizeMb: 16 },
  { id: 'flac-lossless', label: 'FLAC (Lossless Studio Master)', bitrate: 'Lossless', format: 'flac', estimatedSizeMb: 68 },
];

export const DEFAULT_SETTINGS: DownloadSettings = {
  downloadLocation: '/Users/alex/Downloads/YouTube_Vault',
  namingPattern: '{channel} - {title} [{resolution}]',
  subfolderSorting: 'channel',
  defaultVideoQuality: '1080p',
  defaultAudioFormat: 'mp3',
  maxConcurrentDownloads: 3,
  speedLimitMbps: 0, // 0 = unlimited
  embedThumbnails: true,
  embedSubtitles: true,
  saveMetadataJson: true,
  userCategories: ['Tech', 'Science', 'Coding', 'Music', 'Education', 'Gaming', 'Podcasts', 'Archival'],
};

export const SAMPLE_VIDEOS: VideoItem[] = [
  {
    id: 'yt-101',
    url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
    title: 'The Insane Engineering of the James Webb Space Telescope',
    channel: 'Veritasium',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1451187580459-43490279c0fa?w=800&auto=format&fit=crop&q=80',
    duration: '22:14',
    durationSeconds: 1334,
    publishDate: '2024-05-18',
    category: 'Science',
    mediaType: 'video',
    selectedResolution: DEFAULT_RESOLUTIONS[0], // 4K
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/Veritasium',
    fileName: 'Veritasium - The Insane Engineering of the James Webb Space Telescope [2160p].mp4',
    status: 'downloading',
    progress: 68,
    downloadedBytes: 1012 * 1024 * 1024,
    totalBytes: 1488 * 1024 * 1024,
    speedBytesPerSec: 18.4 * 1024 * 1024,
    etaSeconds: 26,
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  },
  {
    id: 'yt-102',
    url: 'https://www.youtube.com/watch?v=MKBHD987123',
    title: 'M3 Max MacBook Pro Deep Dive: Worth The Upgrade?',
    channel: 'Marques Brownlee',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1507003211169-0a1dd7228f2d?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1517336714731-489689fd1ca8?w=800&auto=format&fit=crop&q=80',
    duration: '16:45',
    durationSeconds: 1005,
    publishDate: '2024-04-12',
    category: 'Tech',
    mediaType: 'video',
    selectedResolution: DEFAULT_RESOLUTIONS[2], // 1080p
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/Marques Brownlee',
    fileName: 'Marques Brownlee - M3 Max MacBook Pro Deep Dive [1080p].mp4',
    status: 'downloading',
    progress: 34,
    downloadedBytes: 115 * 1024 * 1024,
    totalBytes: 340 * 1024 * 1024,
    speedBytesPerSec: 12.1 * 1024 * 1024,
    etaSeconds: 19,
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  },
  {
    id: 'yt-103',
    url: 'https://www.youtube.com/watch?v=KZ45129988',
    title: 'What If We Detonated All Nuclear Bombs at Once?',
    channel: 'Kurzgesagt – In a Nutshell',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1535713875002-d1d0cf377fde?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1446776811953-b23d57bd21aa?w=800&auto=format&fit=crop&q=80',
    duration: '11:08',
    durationSeconds: 668,
    publishDate: '2024-03-29',
    category: 'Science',
    mediaType: 'video',
    selectedResolution: DEFAULT_RESOLUTIONS[1], // 1440p
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/Kurzgesagt – In a Nutshell',
    fileName: 'Kurzgesagt – In a Nutshell - What If We Detonated All Nuclear Bombs at Once [1440p].mp4',
    status: 'queued',
    progress: 0,
    downloadedBytes: 0,
    totalBytes: 780 * 1024 * 1024,
    speedBytesPerSec: 0,
    etaSeconds: 0,
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  },
  {
    id: 'yt-104',
    url: 'https://www.youtube.com/watch?v=LOFI992381',
    title: 'synthwave radio - chill beats to code / relax to',
    channel: 'Lofi Girl',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1494790108377-be9c29b29330?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1518709268805-4e9042af9f23?w=800&auto=format&fit=crop&q=80',
    duration: '48:30',
    durationSeconds: 2910,
    publishDate: '2024-06-01',
    category: 'Music',
    mediaType: 'audio',
    selectedResolution: DEFAULT_RESOLUTIONS[2],
    selectedAudio: DEFAULT_AUDIO_OPTIONS[0], // MP3 320k
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/Lofi Girl',
    fileName: 'Lofi Girl - synthwave radio - chill beats to code [320kbps].mp3',
    status: 'processing',
    progress: 98,
    downloadedBytes: 76 * 1024 * 1024,
    totalBytes: 78 * 1024 * 1024,
    speedBytesPerSec: 2.2 * 1024 * 1024,
    etaSeconds: 2,
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  },
  // Completed items in Library
  {
    id: 'yt-105',
    url: 'https://www.youtube.com/watch?v=FS10928374',
    title: 'React 19 in 100 Seconds // What You Need to Know',
    channel: 'Fireship',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1570295999919-56ceb5ecca61?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1633356122544-f134324a6cee?w=800&auto=format&fit=crop&q=80',
    duration: '02:44',
    durationSeconds: 164,
    publishDate: '2024-05-10',
    category: 'Coding',
    mediaType: 'video',
    selectedResolution: DEFAULT_RESOLUTIONS[0], // 4K
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/Fireship',
    fileName: 'Fireship - React 19 in 100 Seconds [2160p].mp4',
    status: 'completed',
    progress: 100,
    downloadedBytes: 210 * 1024 * 1024,
    totalBytes: 210 * 1024 * 1024,
    speedBytesPerSec: 0,
    etaSeconds: 0,
    downloadedAt: 'Today, 2:15 PM',
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  },
  {
    id: 'yt-106',
    url: 'https://www.youtube.com/watch?v=HB77889911',
    title: 'How Dopamine Detox Actually Resets Brain Plasticity',
    channel: 'Andrew Huberman',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1500648767791-00dcc994a43e?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1507679799987-c73779587ccf?w=800&auto=format&fit=crop&q=80',
    duration: '1:32:15',
    durationSeconds: 5535,
    publishDate: '2024-02-14',
    category: 'Education',
    mediaType: 'audio',
    selectedResolution: DEFAULT_RESOLUTIONS[2],
    selectedAudio: DEFAULT_AUDIO_OPTIONS[0],
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/Andrew Huberman',
    fileName: 'Andrew Huberman - How Dopamine Detox Actually Resets Brain Plasticity [320kbps].mp3',
    status: 'completed',
    progress: 100,
    downloadedBytes: 185 * 1024 * 1024,
    totalBytes: 185 * 1024 * 1024,
    speedBytesPerSec: 0,
    etaSeconds: 0,
    downloadedAt: 'Yesterday, 8:40 PM',
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  },
  {
    id: 'yt-107',
    url: 'https://www.youtube.com/watch?v=GEO998231',
    title: 'Why Cities Need Better Public Transit Infrastructure',
    channel: 'City Beautiful',
    channelAvatarUrl: 'https://images.unsplash.com/photo-1472099645785-5658abf4ff4e?w=100&auto=format&fit=crop&q=80',
    thumbnailUrl: 'https://images.unsplash.com/photo-1477959858617-67f30bc75b82?w=800&auto=format&fit=crop&q=80',
    duration: '14:20',
    durationSeconds: 860,
    publishDate: '2024-04-02',
    category: 'Education',
    mediaType: 'video',
    selectedResolution: DEFAULT_RESOLUTIONS[2], // 1080p
    subfolderPath: '/Users/alex/Downloads/YouTube_Vault/City Beautiful',
    fileName: 'City Beautiful - Why Cities Need Better Public Transit Infrastructure [1080p].mp4',
    status: 'completed',
    progress: 100,
    downloadedBytes: 310 * 1024 * 1024,
    totalBytes: 310 * 1024 * 1024,
    speedBytesPerSec: 0,
    etaSeconds: 0,
    downloadedAt: 'Sep 17, 2024',
    availableResolutions: DEFAULT_RESOLUTIONS,
    availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
  }
];

// Presets that the user can test with 1-click
export const SAMPLE_PRESETS = [
  {
    id: 'single',
    label: 'Single 4K Video',
    description: 'Veritasium: JWST Deep Space Optics (4K 60fps)',
    type: 'single',
    urls: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
  },
  {
    id: 'batch',
    label: 'Batch: 3 Tech Videos',
    description: 'MKBHD, Fireship & Kurzgesagt simultaneously',
    type: 'batch',
    urls: `https://www.youtube.com/watch?v=MKBHD987123\nhttps://www.youtube.com/watch?v=FS10928374\nhttps://www.youtube.com/watch?v=KZ45129988`,
  },
  {
    id: 'playlist',
    label: 'Playlist: 4 Coding Lessons',
    description: 'TypeScript & React Architecture Masterclass (Playlist)',
    type: 'playlist',
    urls: 'https://www.youtube.com/playlist?list=PL4cUxeGndAe7_3xT2U_R40zY9H2yXg-3m',
  }
];

// Helper to format bytes
export function formatBytes(bytes: number, decimals = 1): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

// Helper to format speed
export function formatSpeed(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec === 0) return '0 KB/s';
  return `${formatBytes(bytesPerSec, 1)}/s`;
}

// Helper to format ETA
export function formatEta(seconds: number): string {
  if (!seconds || seconds <= 0) return '--';
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  return `${mins}m ${secs}s`;
}

// Compute file path given settings
export function computeDestination(
  title: string,
  channel: string,
  resolutionLabel: string,
  category: string,
  mediaType: 'video' | 'audio',
  settings: DownloadSettings,
  audioFormat = 'mp3'
): { subfolder: string; fileName: string; fullPath: string } {
  // Sanitize
  const cleanTitle = title.replace(/[/\\?%*:|"<>]/g, '').trim();
  const cleanChannel = channel.replace(/[/\\?%*:|"<>]/g, '').trim();
  const resTag = resolutionLabel.split(' ')[0] || '1080p';
  const ext = mediaType === 'video' ? 'mp4' : audioFormat;

  let subfolderName = '';
  if (settings.subfolderSorting === 'channel') {
    subfolderName = cleanChannel;
  } else if (settings.subfolderSorting === 'category') {
    subfolderName = category;
  } else if (settings.subfolderSorting === 'flat') {
    subfolderName = '';
  }

  const subfolder = subfolderName 
    ? `${settings.downloadLocation}/${subfolderName}`
    : settings.downloadLocation;

  // Substitute tokens in namingPattern: {channel}, {title}, {resolution}, {category}
  let generatedName = settings.namingPattern
    .replace('{channel}', cleanChannel)
    .replace('{title}', cleanTitle)
    .replace('{resolution}', resTag)
    .replace('{category}', category);

  if (!generatedName.trim()) {
    generatedName = `${cleanChannel} - ${cleanTitle}`;
  }

  const fileName = `${generatedName}.${ext}`;
  const fullPath = `${subfolder}/${fileName}`;

  return { subfolder, fileName, fullPath };
}
