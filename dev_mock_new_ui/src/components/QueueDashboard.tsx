import React, { useState } from 'react';
import { 
  Play, 
  Pause, 
  X, 
  RotateCcw, 
  CheckCircle2, 
  Clock, 
  Activity, 
  Folder, 
  Trash2, 
  Sliders, 
  HardDrive, 
  Check, 
  Film, 
  Music,
  ExternalLink,
  ChevronDown,
  Sparkles
} from 'lucide-react';
import { VideoItem, DownloadStatus, DownloadSettings } from '../types';
import { formatBytes, formatSpeed, formatEta, DEFAULT_RESOLUTIONS } from '../data/mockData';

interface QueueDashboardProps {
  queueItems: VideoItem[];
  onTogglePause: (id: string) => void;
  onCancelItem: (id: string) => void;
  onRetryItem: (id: string) => void;
  onStartAll: () => void;
  onPauseAll: () => void;
  onClearCompleted: () => void;
  onChangeItemResolution: (id: string, resId: string) => void;
  onOpenLibrary: () => void;
  onOpenFolderModal: (path: string) => void;
  settings: DownloadSettings;
}

export const QueueDashboard: React.FC<QueueDashboardProps> = ({
  queueItems,
  onTogglePause,
  onCancelItem,
  onRetryItem,
  onStartAll,
  onPauseAll,
  onClearCompleted,
  onChangeItemResolution,
  onOpenLibrary,
  onOpenFolderModal,
  settings,
}) => {
  const [filter, setFilter] = useState<'all' | 'active' | 'queued' | 'paused' | 'completed'>('all');
  const [searchQuery, setSearchQuery] = useState('');

  const activeDownloads = queueItems.filter((i) => i.status === 'downloading' || i.status === 'processing');
  const queuedDownloads = queueItems.filter((i) => i.status === 'queued');
  const completedDownloads = queueItems.filter((i) => i.status === 'completed');
  const pausedDownloads = queueItems.filter((i) => i.status === 'paused');

  const totalSpeed = activeDownloads.reduce((acc, curr) => acc + curr.speedBytesPerSec, 0);

  const filteredItems = queueItems.filter((item) => {
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase();
      const matchText = (item.title + ' ' + item.channel + ' ' + item.category).toLowerCase();
      if (!matchText.includes(q)) return false;
    }

    if (filter === 'active') return item.status === 'downloading' || item.status === 'processing';
    if (filter === 'queued') return item.status === 'queued';
    if (filter === 'paused') return item.status === 'paused';
    if (filter === 'completed') return item.status === 'completed';
    return true;
  });

  return (
    <div className="space-y-4">
      {/* Batch Control Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-neutral-800 bg-neutral-900/70 p-3.5 backdrop-blur-md">
        {/* Left: Filter Pills & Search */}
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex items-center rounded-lg bg-neutral-950 p-1 border border-neutral-800 text-xs font-medium">
            <button
              id="filter-all"
              onClick={() => setFilter('all')}
              className={`rounded px-2.5 py-1 transition-all ${
                filter === 'all'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              All ({queueItems.length})
            </button>
            <button
              id="filter-active"
              onClick={() => setFilter('active')}
              className={`rounded px-2.5 py-1 transition-all ${
                filter === 'active'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              Active ({activeDownloads.length})
            </button>
            <button
              id="filter-queued"
              onClick={() => setFilter('queued')}
              className={`rounded px-2.5 py-1 transition-all ${
                filter === 'queued'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              Queued ({queuedDownloads.length})
            </button>
            <button
              id="filter-completed"
              onClick={() => setFilter('completed')}
              className={`rounded px-2.5 py-1 transition-all ${
                filter === 'completed'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              Completed ({completedDownloads.length})
            </button>
          </div>

          <input
            type="text"
            placeholder="Search queue..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-1.5 text-xs text-neutral-100 placeholder-neutral-500 focus:border-rose-500 focus:outline-none"
          />
        </div>

        {/* Right: Batch Actions (Start All, Pause All, Clear Completed) */}
        <div className="flex items-center gap-2">
          {activeDownloads.length > 0 && (
            <button
              id="btn-pause-all"
              onClick={onPauseAll}
              className="flex items-center gap-1.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs font-semibold text-amber-300 hover:bg-amber-500/20 transition-colors"
            >
              <Pause className="h-3.5 w-3.5" />
              <span>Pause All</span>
            </button>
          )}

          {(queuedDownloads.length > 0 || pausedDownloads.length > 0) && (
            <button
              id="btn-start-all"
              onClick={onStartAll}
              className="flex items-center gap-1.5 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-500 shadow-sm transition-colors"
            >
              <Play className="h-3.5 w-3.5" />
              <span>Start All</span>
            </button>
          )}

          {completedDownloads.length > 0 && (
            <button
              id="btn-clear-completed"
              onClick={onClearCompleted}
              className="flex items-center gap-1.5 rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-1.5 text-xs font-medium text-neutral-400 hover:text-neutral-200 hover:bg-neutral-800 transition-colors"
            >
              <Trash2 className="h-3.5 w-3.5" />
              <span>Clear Done</span>
            </button>
          )}
        </div>
      </div>

      {/* Global Speed & Status banner when downloading */}
      {activeDownloads.length > 0 && (
        <div className="flex items-center justify-between rounded-xl border border-rose-900/30 bg-gradient-to-r from-rose-950/40 to-neutral-900/80 px-4 py-2.5 text-xs text-neutral-300">
          <div className="flex items-center gap-2">
            <span className="relative flex h-2.5 w-2.5">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-rose-400 opacity-75"></span>
              <span className="relative inline-flex rounded-full h-2.5 w-2.5 bg-rose-500"></span>
            </span>
            <span className="font-semibold text-white">
              Batch In Progress:
            </span>
            <span>
              {activeDownloads.length} threads active, {queuedDownloads.length} queued
            </span>
          </div>
          <div className="flex items-center gap-4 font-mono">
            <span className="text-emerald-400 font-bold">
              {formatSpeed(totalSpeed)}
            </span>
            <span className="text-neutral-400 hidden sm:inline">
              Max Parallel: {settings.maxConcurrentDownloads}
            </span>
          </div>
        </div>
      )}

      {/* Empty State */}
      {filteredItems.length === 0 && (
        <div className="rounded-2xl border border-neutral-800/80 bg-neutral-900/40 p-12 text-center">
          <Film className="mx-auto h-10 w-10 text-neutral-600" />
          <h3 className="mt-3 text-sm font-semibold text-neutral-300">
            {searchQuery ? 'No matching downloads found' : 'Download queue is empty'}
          </h3>
          <p className="mt-1 text-xs text-neutral-400 max-w-sm mx-auto">
            {searchQuery
              ? 'Try adjusting your search keywords or filter tab.'
              : 'Enter a YouTube link above or click "Test presets" to batch download video and audio.'}
          </p>
        </div>
      )}

      {/* Queue Items List */}
      <div className="space-y-3">
        {filteredItems.map((item) => {
          const isDownloading = item.status === 'downloading';
          const isProcessing = item.status === 'processing';
          const isCompleted = item.status === 'completed';
          const isPaused = item.status === 'paused';
          const isQueued = item.status === 'queued';
          const isFailed = item.status === 'failed';

          return (
            <div
              key={item.id}
              id={`queue-card-${item.id}`}
              className={`group relative rounded-2xl border p-4 transition-all ${
                isDownloading
                  ? 'border-rose-900/50 bg-neutral-900/90 shadow-md shadow-rose-950/20'
                  : isProcessing
                  ? 'border-purple-900/50 bg-neutral-900/90'
                  : isCompleted
                  ? 'border-emerald-900/30 bg-neutral-900/50'
                  : isPaused
                  ? 'border-amber-900/30 bg-neutral-900/60'
                  : 'border-neutral-800/80 bg-neutral-900/60'
              }`}
            >
              <div className="flex flex-col gap-3.5 md:flex-row md:items-center">
                {/* Visual Thumbnail with overlay badges */}
                <div className="relative h-24 w-full shrink-0 overflow-hidden rounded-xl bg-neutral-950 md:w-44">
                  <img
                    src={item.thumbnailUrl}
                    alt={item.title}
                    className="h-full w-full object-cover transition-transform duration-300 group-hover:scale-105"
                    referrerPolicy="no-referrer"
                  />
                  {/* Duration badge */}
                  <span className="absolute bottom-1.5 right-1.5 rounded-md bg-black/85 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-neutral-200 backdrop-blur-xs">
                    {item.duration}
                  </span>

                  {/* Format tag badge */}
                  <span className="absolute top-1.5 left-1.5 flex items-center gap-1 rounded-md bg-black/85 px-1.5 py-0.5 text-[10px] font-bold tracking-tight text-white backdrop-blur-xs">
                    {item.mediaType === 'video' ? (
                      <>
                        <Film className="h-3 w-3 text-rose-400" />
                        <span>{item.selectedResolution.label.split(' ')[0]}</span>
                      </>
                    ) : (
                      <>
                        <Music className="h-3 w-3 text-amber-400" />
                        <span>MP3</span>
                      </>
                    )}
                  </span>
                </div>

                {/* Details & Target Subfolder */}
                <div className="min-w-0 flex-1 space-y-2">
                  <div className="flex flex-wrap items-center gap-2 text-xs">
                    <span className="font-semibold text-rose-400">
                      {item.channel}
                    </span>
                    <span className="text-neutral-500">•</span>
                    <span className="rounded bg-neutral-800 px-1.5 py-0.5 text-[10px] font-medium text-neutral-300">
                      {item.category}
                    </span>
                    {item.playlistTitle && (
                      <span className="rounded bg-rose-950/60 border border-rose-800/40 px-1.5 py-0.5 text-[10px] font-medium text-rose-300">
                        {item.playlistTitle}
                      </span>
                    )}
                  </div>

                  <h3 className="text-sm font-semibold text-white line-clamp-1" title={item.title}>
                    {item.title}
                  </h3>

                  {/* Target Destination & Subfolder preview */}
                  <div className="flex items-center gap-1.5 text-xs text-neutral-400">
                    <Folder className="h-3.5 w-3.5 shrink-0 text-amber-400/90" />
                    <span className="truncate font-mono text-[11px]">
                      {item.subfolderPath.split('/').slice(-2).join('/')}/{item.fileName}
                    </span>
                    <button
                      onClick={() => onOpenFolderModal(item.subfolderPath)}
                      title="Open Subfolder location"
                      className="ml-1 text-neutral-400 hover:text-white"
                    >
                      <ExternalLink className="h-3 w-3" />
                    </button>
                  </div>

                  {/* Progress Bar & Real-time stats */}
                  <div className="space-y-1.5 pt-1">
                    <div className="flex items-center justify-between text-xs font-mono">
                      <div className="flex items-center gap-2">
                        {isDownloading && (
                          <span className="flex items-center gap-1 font-sans font-semibold text-rose-400">
                            <Activity className="h-3.5 w-3.5 animate-pulse" />
                            Downloading
                          </span>
                        )}
                        {isProcessing && (
                          <span className="flex items-center gap-1 font-sans font-semibold text-purple-400">
                            <Sparkles className="h-3.5 w-3.5 animate-spin" />
                            Merging & Audio Muxing
                          </span>
                        )}
                        {isQueued && (
                          <span className="flex items-center gap-1 font-sans font-medium text-neutral-400">
                            <Clock className="h-3.5 w-3.5" />
                            Queued in Batch
                          </span>
                        )}
                        {isPaused && (
                          <span className="flex items-center gap-1 font-sans font-medium text-amber-400">
                            <Pause className="h-3.5 w-3.5" />
                            Paused
                          </span>
                        )}
                        {isCompleted && (
                          <span className="flex items-center gap-1 font-sans font-semibold text-emerald-400">
                            <CheckCircle2 className="h-3.5 w-3.5" />
                            Downloaded & Sorted
                          </span>
                        )}

                        {/* Progress percentage */}
                        <span className="font-bold text-neutral-200">
                          {Math.round(item.progress)}%
                        </span>
                      </div>

                      {/* Speed & ETA metrics */}
                      <div className="flex items-center gap-3 text-neutral-400">
                        {isDownloading && (
                          <>
                            <span className="font-semibold text-emerald-400">
                              {formatSpeed(item.speedBytesPerSec)}
                            </span>
                            <span>ETA: {formatEta(item.etaSeconds)}</span>
                          </>
                        )}
                        <span>
                          {formatBytes(item.downloadedBytes)} / {formatBytes(item.totalBytes)}
                        </span>
                      </div>
                    </div>

                    {/* The Visual Progress Track */}
                    <div className="relative h-2 w-full overflow-hidden rounded-full bg-neutral-800">
                      <div
                        className={`h-full transition-all duration-300 ${
                          isCompleted
                            ? 'bg-emerald-500'
                            : isProcessing
                            ? 'bg-purple-500 animate-pulse'
                            : isPaused
                            ? 'bg-amber-500'
                            : 'bg-gradient-to-r from-rose-600 via-red-500 to-rose-400'
                        }`}
                        style={{ width: `${Math.min(100, Math.max(1, item.progress))}%` }}
                      />
                    </div>
                  </div>
                </div>

                {/* Right Action Buttons */}
                <div className="flex items-center justify-between border-t border-neutral-800/80 pt-2 md:flex-col md:items-end md:justify-center md:border-t-0 md:pt-0 gap-2">
                  {/* Quality dropdown if queued or paused */}
                  {(isQueued || isPaused) && item.mediaType === 'video' && (
                    <select
                      value={item.selectedResolution.id}
                      onChange={(e) => onChangeItemResolution(item.id, e.target.value)}
                      className="rounded-lg border border-neutral-700 bg-neutral-950 px-2 py-1 text-xs text-neutral-200 focus:outline-none"
                    >
                      {DEFAULT_RESOLUTIONS.map((res) => (
                        <option key={res.id} value={res.id}>
                          {res.label.split(' ')[0]} ({formatBytes(res.estimatedSizeMb * 1024 * 1024)})
                        </option>
                      ))}
                    </select>
                  )}

                  <div className="flex items-center gap-1.5">
                    {/* Pause / Resume Button */}
                    {(isDownloading || isProcessing) && (
                      <button
                        id={`btn-pause-${item.id}`}
                        onClick={() => onTogglePause(item.id)}
                        className="flex h-8 w-8 items-center justify-center rounded-lg border border-neutral-700 bg-neutral-800 text-neutral-200 hover:bg-neutral-700 hover:text-white transition-colors"
                        title="Pause download"
                      >
                        <Pause className="h-4 w-4" />
                      </button>
                    )}

                    {(isPaused || isQueued) && (
                      <button
                        id={`btn-resume-${item.id}`}
                        onClick={() => onTogglePause(item.id)}
                        className="flex h-8 w-8 items-center justify-center rounded-lg bg-rose-600 text-white hover:bg-rose-500 transition-colors shadow-sm"
                        title="Start / Resume"
                      >
                        <Play className="h-4 w-4" />
                      </button>
                    )}

                    {isFailed && (
                      <button
                        id={`btn-retry-${item.id}`}
                        onClick={() => onRetryItem(item.id)}
                        className="flex h-8 w-8 items-center justify-center rounded-lg bg-amber-600 text-white hover:bg-amber-500 transition-colors"
                        title="Retry download"
                      >
                        <RotateCcw className="h-4 w-4" />
                      </button>
                    )}

                    {isCompleted ? (
                      <button
                        onClick={onOpenLibrary}
                        className="flex items-center gap-1 rounded-lg bg-emerald-600/20 border border-emerald-500/40 px-3 py-1.5 text-xs font-semibold text-emerald-300 hover:bg-emerald-600/30 transition-colors"
                      >
                        <span>In Library</span>
                      </button>
                    ) : (
                      <button
                        id={`btn-cancel-${item.id}`}
                        onClick={() => onCancelItem(item.id)}
                        className="flex h-8 w-8 items-center justify-center rounded-lg border border-neutral-800 bg-neutral-950 text-neutral-400 hover:bg-rose-950/40 hover:text-rose-400 hover:border-rose-900/50 transition-colors"
                        title="Cancel and remove"
                      >
                        <X className="h-4 w-4" />
                      </button>
                    )}
                  </div>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};
