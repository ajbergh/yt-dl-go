import React, { useState } from 'react';
import { 
  Play, 
  Folder, 
  Copy, 
  Check, 
  Trash2, 
  Search, 
  LayoutGrid, 
  List, 
  Film, 
  Music, 
  ExternalLink, 
  HardDrive,
  Filter,
  CheckCircle2,
  Share2
} from 'lucide-react';
import { VideoItem, DownloadSettings } from '../types';
import { formatBytes } from '../data/mockData';

interface CompletedLibraryProps {
  completedVideos: VideoItem[];
  onPlayVideo: (video: VideoItem) => void;
  onDeleteVideo: (id: string) => void;
  onOpenFolderModal: (path: string) => void;
  settings: DownloadSettings;
}

export const CompletedLibrary: React.FC<CompletedLibraryProps> = ({
  completedVideos,
  onPlayVideo,
  onDeleteVideo,
  onOpenFolderModal,
  settings,
}) => {
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid');
  const [selectedSubfolder, setSelectedSubfolder] = useState<string>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [mediaFilter, setMediaFilter] = useState<'all' | 'video' | 'audio'>('all');
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Group subfolders by channel or category based on settings
  const subfolderGroups = Array.from(
    new Set(
      completedVideos.map((v) =>
        settings.subfolderSorting === 'category' ? v.category : v.channel
      )
    )
  ).sort();

  // Filtered list
  const filteredVideos = completedVideos.filter((video) => {
    if (mediaFilter === 'video' && video.mediaType !== 'video') return false;
    if (mediaFilter === 'audio' && video.mediaType !== 'audio') return false;

    if (selectedSubfolder !== 'all') {
      const matchKey = settings.subfolderSorting === 'category' ? video.category : video.channel;
      if (matchKey !== selectedSubfolder) return false;
    }

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase();
      const match = (video.title + ' ' + video.channel + ' ' + video.category + ' ' + video.fileName).toLowerCase();
      if (!match.includes(q)) return false;
    }

    return true;
  });

  const handleCopyPath = (video: VideoItem) => {
    navigator.clipboard.writeText(`${video.subfolderPath}/${video.fileName}`);
    setCopiedId(video.id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  const totalLibraryBytes = completedVideos.reduce((acc, curr) => acc + curr.totalBytes, 0);

  return (
    <div className="space-y-5">
      {/* Top Header & Search Bar */}
      <div className="flex flex-col gap-4 rounded-2xl border border-neutral-800 bg-neutral-900/70 p-4 backdrop-blur-md md:flex-row md:items-center md:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-emerald-400">
            <Film className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-base font-bold text-white">
              Downloaded Media Library
            </h2>
            <p className="text-xs text-neutral-400">
              {completedVideos.length} items preserved with high-res thumbnails ({formatBytes(totalLibraryBytes)})
            </p>
          </div>
        </div>

        {/* Search & Layout Toggles */}
        <div className="flex flex-wrap items-center gap-2.5">
          <div className="relative min-w-[220px]">
            <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-neutral-500" />
            <input
              type="text"
              placeholder="Search title, channel, file..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="w-full rounded-xl border border-neutral-800 bg-neutral-950 py-2 pl-9 pr-3 text-xs text-neutral-100 placeholder-neutral-500 focus:border-rose-500 focus:outline-none"
            />
          </div>

          {/* Media filter */}
          <div className="flex items-center rounded-lg bg-neutral-950 p-1 border border-neutral-800 text-xs">
            <button
              onClick={() => setMediaFilter('all')}
              className={`rounded px-2.5 py-1 transition-all ${
                mediaFilter === 'all'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              All
            </button>
            <button
              onClick={() => setMediaFilter('video')}
              className={`rounded px-2.5 py-1 transition-all ${
                mediaFilter === 'video'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              Videos
            </button>
            <button
              onClick={() => setMediaFilter('audio')}
              className={`rounded px-2.5 py-1 transition-all ${
                mediaFilter === 'audio'
                  ? 'bg-neutral-800 text-white font-semibold'
                  : 'text-neutral-400 hover:text-neutral-200'
              }`}
            >
              Audio
            </button>
          </div>

          {/* View mode toggle */}
          <div className="flex items-center rounded-lg bg-neutral-950 p-1 border border-neutral-800">
            <button
              id="btn-view-grid"
              onClick={() => setViewMode('grid')}
              className={`p-1.5 rounded transition-all ${
                viewMode === 'grid' ? 'bg-neutral-800 text-white' : 'text-neutral-500 hover:text-neutral-300'
              }`}
              title="Grid View"
            >
              <LayoutGrid className="h-4 w-4" />
            </button>
            <button
              id="btn-view-list"
              onClick={() => setViewMode('list')}
              className={`p-1.5 rounded transition-all ${
                viewMode === 'list' ? 'bg-neutral-800 text-white' : 'text-neutral-500 hover:text-neutral-300'
              }`}
              title="List View"
            >
              <List className="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>

      {/* Main Content Area: Subfolder sidebar + Media items */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-4">
        {/* Left Subfolders Organization Explorer */}
        <div className="lg:col-span-1 space-y-3">
          <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-3.5">
            <div className="mb-2.5 flex items-center justify-between">
              <h3 className="text-xs font-bold uppercase tracking-wider text-neutral-400 flex items-center gap-1.5">
                <Folder className="h-3.5 w-3.5 text-amber-400" />
                <span>
                  {settings.subfolderSorting === 'category' ? 'Category Subfolders' : 'Channel Subfolders'}
                </span>
              </h3>
              <span className="text-[10px] text-neutral-400">
                {subfolderGroups.length} folders
              </span>
            </div>

            <div className="space-y-1">
              <button
                id="subfolder-all"
                onClick={() => setSelectedSubfolder('all')}
                className={`w-full flex items-center justify-between rounded-lg px-3 py-2 text-xs transition-all ${
                  selectedSubfolder === 'all'
                    ? 'bg-rose-600/15 border border-rose-500/30 text-rose-300 font-semibold'
                    : 'text-neutral-300 hover:bg-neutral-800/60'
                }`}
              >
                <div className="flex items-center gap-2 truncate">
                  <Folder className="h-4 w-4 text-amber-400/90 shrink-0" />
                  <span className="truncate">All Saved Media</span>
                </div>
                <span className="text-[11px] font-mono text-neutral-400">
                  {completedVideos.length}
                </span>
              </button>

              {subfolderGroups.map((group) => {
                const count = completedVideos.filter((v) =>
                  settings.subfolderSorting === 'category' ? v.category === group : v.channel === group
                ).length;

                return (
                  <button
                    key={group}
                    id={`subfolder-${group.replace(/\s+/g, '-').toLowerCase()}`}
                    onClick={() => setSelectedSubfolder(group)}
                    className={`w-full flex items-center justify-between rounded-lg px-3 py-2 text-xs transition-all ${
                      selectedSubfolder === group
                        ? 'bg-rose-600/15 border border-rose-500/30 text-rose-300 font-semibold'
                        : 'text-neutral-300 hover:bg-neutral-800/60'
                    }`}
                  >
                    <div className="flex items-center gap-2 truncate">
                      <Folder className="h-4 w-4 text-neutral-500 shrink-0" />
                      <span className="truncate">{group}</span>
                    </div>
                    <span className="text-[11px] font-mono text-neutral-400">
                      {count}
                    </span>
                  </button>
                );
              })}
            </div>

            <div className="mt-4 pt-3 border-t border-neutral-800/80">
              <button
                onClick={() => onOpenFolderModal(settings.downloadLocation)}
                className="w-full flex items-center justify-center gap-1.5 rounded-lg border border-neutral-800 bg-neutral-950 py-1.5 text-xs text-neutral-300 hover:bg-neutral-800 hover:text-white transition-colors"
              >
                <HardDrive className="h-3.5 w-3.5 text-neutral-400" />
                <span>Open Root Directory</span>
              </button>
            </div>
          </div>
        </div>

        {/* Right Media Grid / List */}
        <div className="lg:col-span-3">
          {filteredVideos.length === 0 ? (
            <div className="rounded-2xl border border-neutral-800/80 bg-neutral-900/30 p-12 text-center">
              <Film className="mx-auto h-8 w-8 text-neutral-600" />
              <p className="mt-2 text-sm font-semibold text-neutral-300">
                No downloaded files in this view
              </p>
              <p className="text-xs text-neutral-400 mt-0.5">
                Download a video or select another subfolder filter.
              </p>
            </div>
          ) : viewMode === 'grid' ? (
            /* Visual Grid View with Preserved Thumbnails */
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {filteredVideos.map((video) => (
                <div
                  key={video.id}
                  id={`library-card-${video.id}`}
                  className="group relative flex flex-col rounded-2xl border border-neutral-800/90 bg-neutral-900/60 overflow-hidden transition-all duration-200 hover:border-neutral-700 hover:shadow-xl hover:shadow-black/40"
                >
                  {/* Thumbnail with overlay play trigger */}
                  <div
                    onClick={() => onPlayVideo(video)}
                    className="relative aspect-video w-full overflow-hidden bg-neutral-950 cursor-pointer"
                  >
                    <img
                      src={video.thumbnailUrl}
                      alt={video.title}
                      className="h-full w-full object-cover transition-transform duration-500 group-hover:scale-105"
                      referrerPolicy="no-referrer"
                    />

                    {/* Dark gradient overlay on hover */}
                    <div className="absolute inset-0 bg-black/30 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-rose-600 text-white shadow-lg transform group-hover:scale-110 transition-transform">
                        <Play className="h-5 w-5 fill-white ml-0.5" />
                      </div>
                    </div>

                    {/* Duration badge */}
                    <span className="absolute bottom-2 right-2 rounded bg-black/85 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-neutral-200 backdrop-blur-xs">
                      {video.duration}
                    </span>

                    {/* Quality / format badge */}
                    <span className="absolute top-2 left-2 flex items-center gap-1 rounded bg-black/85 px-2 py-0.5 text-[10px] font-bold text-white backdrop-blur-xs">
                      {video.mediaType === 'video' ? (
                        <>
                          <Film className="h-3 w-3 text-rose-400" />
                          <span>{video.selectedResolution.label.split(' ')[0]}</span>
                        </>
                      ) : (
                        <>
                          <Music className="h-3 w-3 text-amber-400" />
                          <span>MP3 Audio</span>
                        </>
                      )}
                    </span>
                  </div>

                  {/* Body details */}
                  <div className="flex flex-1 flex-col justify-between p-3.5 space-y-2.5">
                    <div>
                      <div className="flex items-center gap-2 text-xs">
                        <img
                          src={video.channelAvatarUrl}
                          alt={video.channel}
                          className="h-4 w-4 rounded-full object-cover"
                        />
                        <span className="font-semibold text-rose-400 truncate">
                          {video.channel}
                        </span>
                        <span className="text-neutral-500">•</span>
                        <span className="text-neutral-400 text-[11px] truncate">
                          {video.category}
                        </span>
                      </div>

                      <h4
                        onClick={() => onPlayVideo(video)}
                        className="mt-1.5 text-xs font-semibold text-neutral-100 line-clamp-2 hover:text-rose-400 cursor-pointer transition-colors"
                        title={video.title}
                      >
                        {video.title}
                      </h4>
                    </div>

                    {/* Subfolder location & File metrics */}
                    <div className="space-y-1 text-[11px] text-neutral-400 border-t border-neutral-800/80 pt-2">
                      <div className="flex items-center justify-between">
                        <span className="text-neutral-300 font-mono">
                          {formatBytes(video.totalBytes)}
                        </span>
                        <span className="text-neutral-400">
                          {video.downloadedAt || 'Saved'}
                        </span>
                      </div>

                      <div className="flex items-center gap-1 text-[10px] text-neutral-400 truncate font-mono">
                        <Folder className="h-3 w-3 shrink-0 text-amber-400/80" />
                        <span className="truncate">{video.subfolderPath.split('/').pop()}</span>
                      </div>
                    </div>

                    {/* Card Actions Footer */}
                    <div className="flex items-center justify-between border-t border-neutral-800/80 pt-2 text-xs">
                      <button
                        onClick={() => onPlayVideo(video)}
                        className="flex items-center gap-1 text-neutral-300 hover:text-white transition-colors"
                      >
                        <Play className="h-3 w-3 text-rose-500 fill-rose-500" />
                        <span>Play</span>
                      </button>

                      <div className="flex items-center gap-1.5">
                        <button
                          onClick={() => handleCopyPath(video)}
                          title="Copy file path"
                          className="p-1 rounded text-neutral-400 hover:text-white hover:bg-neutral-800 transition-colors"
                        >
                          {copiedId === video.id ? (
                            <Check className="h-3.5 w-3.5 text-emerald-400" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
                          )}
                        </button>
                        <button
                          onClick={() => onOpenFolderModal(video.subfolderPath)}
                          title="Reveal in folder"
                          className="p-1 rounded text-neutral-400 hover:text-white hover:bg-neutral-800 transition-colors"
                        >
                          <Folder className="h-3.5 w-3.5 text-amber-400" />
                        </button>
                        <button
                          onClick={() => onDeleteVideo(video.id)}
                          title="Delete from library"
                          className="p-1 rounded text-neutral-400 hover:text-rose-400 hover:bg-neutral-800 transition-colors"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            /* Compact List View */
            <div className="overflow-hidden rounded-2xl border border-neutral-800 bg-neutral-900/60">
              <table className="w-full text-left text-xs text-neutral-300">
                <thead className="border-b border-neutral-800 bg-neutral-950/60 font-medium text-neutral-400">
                  <tr>
                    <th className="py-3 px-4">Media</th>
                    <th className="py-3 px-4 hidden md:table-cell">Subfolder / Channel</th>
                    <th className="py-3 px-4 hidden sm:table-cell">Quality / Format</th>
                    <th className="py-3 px-4">Size</th>
                    <th className="py-3 px-4 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-neutral-800/60 font-sans">
                  {filteredVideos.map((video) => (
                    <tr key={video.id} className="hover:bg-neutral-800/40 transition-colors">
                      <td className="py-2.5 px-4">
                        <div className="flex items-center gap-3">
                          <div
                            onClick={() => onPlayVideo(video)}
                            className="relative h-12 w-20 shrink-0 overflow-hidden rounded-lg bg-neutral-950 cursor-pointer group"
                          >
                            <img
                              src={video.thumbnailUrl}
                              alt={video.title}
                              className="h-full w-full object-cover"
                              referrerPolicy="no-referrer"
                            />
                            <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                              <Play className="h-4 w-4 fill-white text-white" />
                            </div>
                            <span className="absolute bottom-0.5 right-0.5 rounded bg-black/80 px-1 font-mono text-[9px] text-neutral-300">
                              {video.duration}
                            </span>
                          </div>
                          <div className="min-w-0">
                            <p
                              onClick={() => onPlayVideo(video)}
                              className="font-semibold text-white truncate max-w-[240px] cursor-pointer hover:text-rose-400"
                              title={video.title}
                            >
                              {video.title}
                            </p>
                            <p className="text-[11px] text-rose-400 truncate">
                              {video.channel}
                            </p>
                          </div>
                        </div>
                      </td>
                      <td className="py-2.5 px-4 hidden md:table-cell">
                        <div className="flex items-center gap-1 text-neutral-400 font-mono text-[11px] truncate max-w-[180px]">
                          <Folder className="h-3 w-3 text-amber-400 shrink-0" />
                          <span className="truncate">{video.subfolderPath.split('/').pop()}</span>
                        </div>
                      </td>
                      <td className="py-2.5 px-4 hidden sm:table-cell">
                        <span className="rounded bg-neutral-800 px-2 py-0.5 text-[11px] font-medium text-neutral-200">
                          {video.mediaType === 'video'
                            ? video.selectedResolution.label.split(' ')[0]
                            : 'MP3 320k'}
                        </span>
                      </td>
                      <td className="py-2.5 px-4 font-mono text-neutral-300">
                        {formatBytes(video.totalBytes)}
                      </td>
                      <td className="py-2.5 px-4 text-right">
                        <div className="flex items-center justify-end gap-2">
                          <button
                            onClick={() => onPlayVideo(video)}
                            className="rounded bg-rose-600/20 border border-rose-500/30 px-2.5 py-1 text-xs font-medium text-rose-300 hover:bg-rose-600/30"
                          >
                            Play
                          </button>
                          <button
                            onClick={() => handleCopyPath(video)}
                            title="Copy path"
                            className="p-1 text-neutral-400 hover:text-white"
                          >
                            {copiedId === video.id ? (
                              <Check className="h-4 w-4 text-emerald-400" />
                            ) : (
                              <Copy className="h-4 w-4" />
                            )}
                          </button>
                          <button
                            onClick={() => onDeleteVideo(video.id)}
                            title="Delete"
                            className="p-1 text-neutral-400 hover:text-rose-400"
                          >
                            <Trash2 className="h-4 w-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
