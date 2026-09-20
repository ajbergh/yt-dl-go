import React, { useState } from 'react';
import { 
  X, 
  Play, 
  Pause, 
  Volume2, 
  VolumeX, 
  Maximize2, 
  Folder, 
  Copy, 
  Check, 
  Film, 
  Music, 
  Calendar, 
  FileText,
  HardDrive
} from 'lucide-react';
import { VideoItem } from '../types';
import { formatBytes } from '../data/mockData';

interface VideoPlayerModalProps {
  video: VideoItem | null;
  onClose: () => void;
  onOpenFolder: (path: string) => void;
}

export const VideoPlayerModal: React.FC<VideoPlayerModalProps> = ({
  video,
  onClose,
  onOpenFolder,
}) => {
  if (!video) return null;

  const [isPlaying, setIsPlaying] = useState(true);
  const [isMuted, setIsMuted] = useState(false);
  const [currentTimeSec, setCurrentTimeSec] = useState(18);
  const [copied, setCopied] = useState(false);

  const durationSec = video.durationSeconds || 300;
  const progressPercent = Math.min(100, (currentTimeSec / durationSec) * 100);

  const handleCopyPath = () => {
    navigator.clipboard.writeText(`${video.subfolderPath}/${video.fileName}`);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-md">
      <div className="relative w-full max-w-4xl overflow-hidden rounded-2xl border border-neutral-800 bg-neutral-950 shadow-2xl">
        {/* Modal Top Bar */}
        <div className="flex items-center justify-between border-b border-neutral-800/80 px-4 py-3 bg-neutral-900/80">
          <div className="flex items-center gap-2 text-xs font-semibold text-neutral-300">
            {video.mediaType === 'video' ? (
              <Film className="h-4 w-4 text-rose-500" />
            ) : (
              <Music className="h-4 w-4 text-amber-500" />
            )}
            <span className="truncate max-w-md">{video.title}</span>
          </div>

          <button
            onClick={onClose}
            className="rounded-lg p-1.5 text-neutral-400 hover:bg-neutral-800 hover:text-white transition-colors"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Video Canvas / Player Simulation */}
        <div className="relative aspect-video w-full bg-black overflow-hidden flex items-center justify-center">
          <img
            src={video.thumbnailUrl}
            alt={video.title}
            className={`h-full w-full object-cover transition-opacity duration-300 ${
              isPlaying ? 'opacity-90' : 'opacity-60 brightness-75'
            }`}
            referrerPolicy="no-referrer"
          />

          {/* Playing overlay animation / badge */}
          {isPlaying && (
            <div className="absolute top-4 right-4 flex items-center gap-1.5 rounded-md bg-black/75 px-2 py-1 font-mono text-[10px] text-emerald-400 backdrop-blur-xs">
              <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse"></span>
              <span>DIRECT PLAYBACK (OFFLINE MEDIA)</span>
            </div>
          )}

          {/* Center Play/Pause button overlay if paused */}
          {!isPlaying && (
            <button
              onClick={() => setIsPlaying(true)}
              className="absolute flex h-16 w-16 items-center justify-center rounded-full bg-rose-600/90 text-white shadow-xl hover:scale-105 transition-transform"
            >
              <Play className="h-7 w-7 fill-white ml-1" />
            </button>
          )}

          {/* Player Controls Bar */}
          <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/50 to-transparent p-4 space-y-2">
            {/* Scrubber track */}
            <div
              className="group relative h-1.5 w-full cursor-pointer rounded-full bg-white/30 transition-all hover:h-2.5"
              onClick={(e) => {
                const rect = e.currentTarget.getBoundingClientRect();
                const clickPos = (e.clientX - rect.left) / rect.width;
                setCurrentTimeSec(Math.round(clickPos * durationSec));
              }}
            >
              <div
                className="h-full rounded-full bg-rose-600"
                style={{ width: `${progressPercent}%` }}
              />
            </div>

            <div className="flex items-center justify-between text-xs text-white">
              <div className="flex items-center gap-3">
                <button
                  onClick={() => setIsPlaying(!isPlaying)}
                  className="text-white hover:text-rose-400 transition-colors"
                >
                  {isPlaying ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4 fill-white" />}
                </button>

                <button
                  onClick={() => setIsMuted(!isMuted)}
                  className="text-white hover:text-neutral-300 transition-colors"
                >
                  {isMuted ? <VolumeX className="h-4 w-4" /> : <Volume2 className="h-4 w-4" />}
                </button>

                <span className="font-mono text-[11px] text-neutral-300">
                  {Math.floor(currentTimeSec / 60)}:{(currentTimeSec % 60).toString().padStart(2, '0')} / {video.duration}
                </span>
              </div>

              <div className="flex items-center gap-2">
                <span className="rounded bg-black/60 px-1.5 py-0.5 font-mono text-[10px] text-neutral-300">
                  {video.selectedResolution.label.split(' ')[0]}
                </span>
                <span className="rounded bg-black/60 px-1.5 py-0.5 font-mono text-[10px] text-neutral-300">
                  {formatBytes(video.totalBytes)}
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Video Metadata & Subfolder Inspector */}
        <div className="p-4 bg-neutral-900/70 border-t border-neutral-800 space-y-3">
          <div className="flex items-start justify-between gap-4">
            <div>
              <div className="flex items-center gap-2 text-xs">
                <img
                  src={video.channelAvatarUrl}
                  alt={video.channel}
                  className="h-5 w-5 rounded-full object-cover"
                />
                <span className="font-bold text-rose-400">{video.channel}</span>
                <span className="text-neutral-500">•</span>
                <span className="rounded bg-neutral-800 px-1.5 py-0.5 text-[10px] text-neutral-300">
                  {video.category}
                </span>
                <span className="text-neutral-400 text-xs">{video.publishDate}</span>
              </div>

              <h3 className="mt-1 text-sm font-semibold text-white">
                {video.title}
              </h3>
            </div>

            <div className="flex items-center gap-2">
              <button
                onClick={handleCopyPath}
                className="flex items-center gap-1.5 rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-1.5 text-xs text-neutral-300 hover:bg-neutral-800 hover:text-white transition-colors"
              >
                {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
                <span>{copied ? 'Copied' : 'Copy File Path'}</span>
              </button>

              <button
                onClick={() => onOpenFolder(video.subfolderPath)}
                className="flex items-center gap-1.5 rounded-lg bg-rose-600/20 border border-rose-500/30 px-3 py-1.5 text-xs font-semibold text-rose-300 hover:bg-rose-600/30 transition-colors"
              >
                <Folder className="h-3.5 w-3.5 text-amber-400" />
                <span>Show in Subfolder</span>
              </button>
            </div>
          </div>

          {/* Subfolder location info card */}
          <div className="rounded-xl border border-neutral-800 bg-neutral-950 p-2.5 font-mono text-[11px] text-neutral-300 flex items-center justify-between">
            <div className="flex items-center gap-2 truncate">
              <Folder className="h-4 w-4 text-amber-400 shrink-0" />
              <span className="truncate">{video.subfolderPath}/{video.fileName}</span>
            </div>
            <span className="text-neutral-500 shrink-0 ml-2">Local File (H.264/AAC)</span>
          </div>
        </div>
      </div>
    </div>
  );
};
