import React, { useState, useEffect } from 'react';
import { 
  Link2, 
  ListPlus, 
  Film, 
  Music, 
  Sparkles, 
  Check, 
  Folder, 
  AlertCircle, 
  Loader2, 
  SlidersHorizontal,
  ChevronDown,
  PlaySquare,
  ArrowRight
} from 'lucide-react';
import { 
  VideoItem, 
  ResolutionOption, 
  AudioOption, 
  MediaType, 
  DownloadSettings 
} from '../types';
import { 
  DEFAULT_RESOLUTIONS, 
  DEFAULT_AUDIO_OPTIONS, 
  SAMPLE_PRESETS,
  computeDestination 
} from '../data/mockData';

interface UrlInputBarProps {
  onAddVideos: (videos: VideoItem[], startImmediately: boolean) => void;
  settings: DownloadSettings;
  isOpenAsModal?: boolean;
  onCloseModal?: () => void;
}

interface FetchedDraft {
  id: string;
  url: string;
  title: string;
  channel: string;
  channelAvatarUrl: string;
  thumbnailUrl: string;
  duration: string;
  durationSeconds: number;
  publishDate: string;
  category: 'Tech' | 'Science' | 'Education' | 'Music' | 'Coding' | 'Gaming' | 'General';
  playlistTitle?: string;
  mediaType: MediaType;
  selectedResolution: ResolutionOption;
  selectedAudio: AudioOption;
  customCategory?: string;
}

export const UrlInputBar: React.FC<UrlInputBarProps> = ({
  onAddVideos,
  settings,
  isOpenAsModal = false,
  onCloseModal,
}) => {
  const [inputMode, setInputMode] = useState<'single' | 'batch' | 'playlist'>('single');
  const [rawUrl, setRawUrl] = useState('');
  const [isFetching, setIsFetching] = useState(false);
  const [fetchedDrafts, setFetchedDrafts] = useState<FetchedDraft[]>([]);
  const [startImmediately, setStartImmediately] = useState(true);
  const [globalResolutionId, setGlobalResolutionId] = useState('1080p');
  const [globalMediaType, setGlobalMediaType] = useState<MediaType>('video');

  // Handle sample preset click
  const handleApplyPreset = (presetId: string) => {
    const preset = SAMPLE_PRESETS.find((p) => p.id === presetId);
    if (!preset) return;

    if (preset.type === 'single') {
      setInputMode('single');
      setRawUrl(preset.urls);
      triggerAutoFetch(preset.urls, 'single');
    } else if (preset.type === 'batch') {
      setInputMode('batch');
      setRawUrl(preset.urls);
      triggerAutoFetch(preset.urls, 'batch');
    } else if (preset.type === 'playlist') {
      setInputMode('playlist');
      setRawUrl(preset.urls);
      triggerAutoFetch(preset.urls, 'playlist');
    }
  };

  // Simulate fetching available video resolutions and metadata from URLs
  const triggerAutoFetch = (text: string, mode: 'single' | 'batch' | 'playlist') => {
    if (!text.trim()) return;
    setIsFetching(true);
    setFetchedDrafts([]);

    setTimeout(() => {
      const drafts: FetchedDraft[] = [];
      const lines = text.split('\n').map((l) => l.trim()).filter(Boolean);

      if (mode === 'playlist' || text.includes('list=')) {
        // Generate simulated playlist items (e.g. 4 coding tutorials)
        const playlistNames = [
          'Episode 1: React 19 Compiler Deep Dive',
          'Episode 2: Server Actions & Next-Gen Hooks',
          'Episode 3: Streaming SSR & Suspense Boundaries',
          'Episode 4: Production Deployment on Cloud Containers',
        ];
        const thumbs = [
          'https://images.unsplash.com/photo-1555066931-4365d14bab8c?w=800&auto=format&fit=crop&q=80',
          'https://images.unsplash.com/photo-1517694712202-14dd9538aa97?w=800&auto=format&fit=crop&q=80',
          'https://images.unsplash.com/photo-1526374965328-7f61d4dc18c5?w=800&auto=format&fit=crop&q=80',
          'https://images.unsplash.com/photo-1618401471353-b98afee0b2eb?w=800&auto=format&fit=crop&q=80',
        ];

        playlistNames.forEach((title, idx) => {
          drafts.push({
            id: `draft-${Date.now()}-${idx}`,
            url: `https://www.youtube.com/watch?v=sample${idx + 1}`,
            title,
            channel: 'Frontend Masters & Arch',
            channelAvatarUrl: 'https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=100&auto=format&fit=crop&q=80',
            thumbnailUrl: thumbs[idx],
            duration: `${14 + idx * 3}:${20 + idx * 11}`,
            durationSeconds: (14 + idx * 3) * 60 + 20,
            publishDate: '2024-06-15',
            category: 'Coding',
            playlistTitle: 'React 19 & Cloud Architecture Series',
            mediaType: 'video',
            selectedResolution: DEFAULT_RESOLUTIONS[2], // 1080p default
            selectedAudio: DEFAULT_AUDIO_OPTIONS[0],
          });
        });
      } else if (lines.length > 1 || mode === 'batch') {
        // Multi-line batch URLs
        const sampleBatchTitles = [
          { title: 'The Ultimate Guide to Quantum Computing in 2024', channel: 'Veritasium', cat: 'Science' as const, thumb: 'https://images.unsplash.com/photo-1635070041078-e363dbe005cb?w=800&auto=format&fit=crop&q=80', dur: '18:42' },
          { title: 'Clean Architecture in Modern Web Apps', channel: 'Fireship', cat: 'Coding' as const, thumb: 'https://images.unsplash.com/photo-1555066931-4365d14bab8c?w=800&auto=format&fit=crop&q=80', dur: '12:15' },
          { title: 'Next-Gen Camera Sensor Breakthroughs', channel: 'Marques Brownlee', cat: 'Tech' as const, thumb: 'https://images.unsplash.com/photo-1516035069371-29a1b244cc32?w=800&auto=format&fit=crop&q=80', dur: '15:30' },
          { title: 'Morning Lo-Fi Beats & Rain Atmosphere', channel: 'Lofi Girl', cat: 'Music' as const, thumb: 'https://images.unsplash.com/photo-1518709268805-4e9042af9f23?w=800&auto=format&fit=crop&q=80', dur: '45:00' },
        ];

        lines.forEach((url, i) => {
          const sample = sampleBatchTitles[i % sampleBatchTitles.length];
          drafts.push({
            id: `draft-${Date.now()}-${i}`,
            url: url || `https://youtube.com/watch?v=sample${i}`,
            title: sample.title,
            channel: sample.channel,
            channelAvatarUrl: 'https://images.unsplash.com/photo-1535713875002-d1d0cf377fde?w=100&auto=format&fit=crop&q=80',
            thumbnailUrl: sample.thumb,
            duration: sample.dur,
            durationSeconds: 900,
            publishDate: '2024-05-20',
            category: sample.cat,
            mediaType: 'video',
            selectedResolution: DEFAULT_RESOLUTIONS[2],
            selectedAudio: DEFAULT_AUDIO_OPTIONS[0],
          });
        });
      } else {
        // Single link
        drafts.push({
          id: `draft-${Date.now()}-0`,
          url: text.trim(),
          title: 'Deep Learning with Gemini 1.5 & Antigravity Engines',
          channel: 'Two Minute Papers',
          channelAvatarUrl: 'https://images.unsplash.com/photo-1534528741775-53994a69daeb?w=100&auto=format&fit=crop&q=80',
          thumbnailUrl: 'https://images.unsplash.com/photo-1618005182384-a83a8bd57fbe?w=800&auto=format&fit=crop&q=80',
          duration: '08:52',
          durationSeconds: 532,
          publishDate: '2024-06-18',
          category: 'Science',
          mediaType: 'video',
          selectedResolution: DEFAULT_RESOLUTIONS[0], // 4K default for single
          selectedAudio: DEFAULT_AUDIO_OPTIONS[0],
        });
      }

      setFetchedDrafts(drafts);
      setIsFetching(false);
    }, 700);
  };

  const handleUpdateDraftResolution = (draftId: string, resId: string) => {
    const res = DEFAULT_RESOLUTIONS.find((r) => r.id === resId) || DEFAULT_RESOLUTIONS[0];
    setFetchedDrafts((prev) =>
      prev.map((d) => (d.id === draftId ? { ...d, selectedResolution: res } : d))
    );
  };

  const handleUpdateDraftMediaType = (draftId: string, type: MediaType) => {
    setFetchedDrafts((prev) =>
      prev.map((d) => (d.id === draftId ? { ...d, mediaType: type } : d))
    );
  };

  const handleUpdateDraftCategory = (draftId: string, category: any) => {
    setFetchedDrafts((prev) =>
      prev.map((d) => (d.id === draftId ? { ...d, category } : d))
    );
  };

  const handleApplyGlobalSettingsToDrafts = (mediaType: MediaType, resId: string) => {
    setGlobalMediaType(mediaType);
    setGlobalResolutionId(resId);
    const chosenRes = DEFAULT_RESOLUTIONS.find((r) => r.id === resId) || DEFAULT_RESOLUTIONS[2];

    setFetchedDrafts((prev) =>
      prev.map((d) => ({
        ...d,
        mediaType,
        selectedResolution: chosenRes,
      }))
    );
  };

  const handleConfirmAndAdd = () => {
    if (fetchedDrafts.length === 0) return;

    const newVideoItems: VideoItem[] = fetchedDrafts.map((draft) => {
      const resLabel = draft.selectedResolution.label;
      const { subfolder, fileName } = computeDestination(
        draft.title,
        draft.channel,
        resLabel,
        draft.category,
        draft.mediaType,
        settings,
        draft.selectedAudio.format
      );

      const estSizeMb = draft.mediaType === 'video'
        ? draft.selectedResolution.estimatedSizeMb
        : draft.selectedAudio.estimatedSizeMb;
      const totalBytes = estSizeMb * 1024 * 1024;

      return {
        id: `video-${Date.now()}-${Math.random().toString(36).substr(2, 6)}`,
        url: draft.url,
        title: draft.title,
        channel: draft.channel,
        channelAvatarUrl: draft.channelAvatarUrl,
        thumbnailUrl: draft.thumbnailUrl,
        duration: draft.duration,
        durationSeconds: draft.durationSeconds,
        publishDate: draft.publishDate,
        category: draft.category,
        playlistTitle: draft.playlistTitle,
        mediaType: draft.mediaType,
        selectedResolution: draft.selectedResolution,
        selectedAudio: draft.selectedAudio,
        subfolderPath: subfolder,
        fileName,
        status: startImmediately ? 'downloading' : 'queued',
        progress: 0,
        downloadedBytes: 0,
        totalBytes,
        speedBytesPerSec: startImmediately ? (14 + Math.random() * 8) * 1024 * 1024 : 0,
        etaSeconds: Math.round(totalBytes / (16 * 1024 * 1024)),
        availableResolutions: DEFAULT_RESOLUTIONS,
        availableAudioOptions: DEFAULT_AUDIO_OPTIONS,
      };
    });

    onAddVideos(newVideoItems, startImmediately);
    setFetchedDrafts([]);
    setRawUrl('');
    if (onCloseModal) onCloseModal();
  };

  return (
    <div className="rounded-2xl border border-neutral-800 bg-neutral-900/80 p-4 shadow-xl backdrop-blur-md sm:p-5">
      {/* Input Mode Selector & Sample Presets */}
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2 border-b border-neutral-800/80 pb-3">
        <div className="flex items-center gap-1 rounded-lg bg-neutral-950 p-1 border border-neutral-800">
          <button
            id="btn-mode-single"
            onClick={() => setInputMode('single')}
            className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-all ${
              inputMode === 'single'
                ? 'bg-rose-600 text-white'
                : 'text-neutral-400 hover:text-neutral-200'
            }`}
          >
            <Link2 className="h-3.5 w-3.5" />
            <span>Single Video</span>
          </button>
          <button
            id="btn-mode-batch"
            onClick={() => setInputMode('batch')}
            className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-all ${
              inputMode === 'batch'
                ? 'bg-rose-600 text-white'
                : 'text-neutral-400 hover:text-neutral-200'
            }`}
          >
            <ListPlus className="h-3.5 w-3.5" />
            <span>Batch (Multi-URLs)</span>
          </button>
          <button
            id="btn-mode-playlist"
            onClick={() => setInputMode('playlist')}
            className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-all ${
              inputMode === 'playlist'
                ? 'bg-rose-600 text-white'
                : 'text-neutral-400 hover:text-neutral-200'
            }`}
          >
            <PlaySquare className="h-3.5 w-3.5" />
            <span>YouTube Playlist</span>
          </button>
        </div>

        {/* Quick Presets Pills */}
        <div className="flex items-center gap-1.5 overflow-x-auto text-xs">
          <span className="text-neutral-400 text-[11px] font-medium hidden sm:inline flex items-center gap-1">
            <Sparkles className="h-3 w-3 text-amber-400" /> Test presets:
          </span>
          {SAMPLE_PRESETS.map((p) => (
            <button
              key={p.id}
              id={`preset-${p.id}`}
              onClick={() => handleApplyPreset(p.id)}
              className="rounded-lg border border-neutral-800 bg-neutral-950/60 px-2.5 py-1 text-[11px] font-medium text-neutral-300 transition-colors hover:border-neutral-700 hover:bg-neutral-800 hover:text-white"
            >
              {p.label}
            </button>
          ))}
        </div>
      </div>

      {/* Main Link Input */}
      <div className="space-y-3">
        {inputMode === 'batch' ? (
          <div className="relative">
            <textarea
              id="batch-url-textarea"
              rows={3}
              value={rawUrl}
              onChange={(e) => setRawUrl(e.target.value)}
              placeholder="Paste multiple YouTube URLs (one URL per line)&#10;https://www.youtube.com/watch?v=...&#10;https://youtu.be/..."
              className="w-full resize-y rounded-xl border border-neutral-800 bg-neutral-950 px-3.5 py-2.5 font-mono text-xs text-neutral-100 placeholder-neutral-500 focus:border-rose-500 focus:outline-none focus:ring-1 focus:ring-rose-500"
            />
          </div>
        ) : (
          <div className="relative flex items-center">
            <div className="pointer-events-none absolute left-3.5 flex items-center text-neutral-500">
              <Link2 className="h-4 w-4" />
            </div>
            <input
              id="single-url-input"
              type="text"
              value={rawUrl}
              onChange={(e) => setRawUrl(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') triggerAutoFetch(rawUrl, inputMode);
              }}
              placeholder={
                inputMode === 'playlist'
                  ? 'Paste YouTube Playlist link (e.g., https://www.youtube.com/playlist?list=...)'
                  : 'Paste YouTube video link (e.g., https://www.youtube.com/watch?v=... or https://youtu.be/...)'
              }
              className="w-full rounded-xl border border-neutral-800 bg-neutral-950 py-2.5 pr-28 pl-10 text-xs text-neutral-100 placeholder-neutral-500 focus:border-rose-500 focus:outline-none focus:ring-1 focus:ring-rose-500 sm:text-sm"
            />
            <button
              id="btn-fetch-resolutions"
              onClick={() => triggerAutoFetch(rawUrl, inputMode)}
              disabled={isFetching || !rawUrl.trim()}
              className="absolute right-1.5 flex items-center gap-1 rounded-lg bg-neutral-800 px-3 py-1.5 text-xs font-semibold text-neutral-200 transition-all hover:bg-neutral-700 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {isFetching ? (
                <>
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-rose-500" />
                  <span>Fetching...</span>
                </>
              ) : (
                <>
                  <Sparkles className="h-3.5 w-3.5 text-rose-400" />
                  <span>Inspect Qualities</span>
                </>
              )}
            </button>
          </div>
        )}

        {inputMode === 'batch' && (
          <div className="flex justify-end">
            <button
              id="btn-fetch-batch"
              onClick={() => triggerAutoFetch(rawUrl, 'batch')}
              disabled={isFetching || !rawUrl.trim()}
              className="flex items-center gap-1.5 rounded-lg bg-neutral-800 px-4 py-2 text-xs font-semibold text-neutral-200 transition-colors hover:bg-neutral-700 disabled:opacity-40"
            >
              {isFetching ? (
                <>
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-rose-500" />
                  <span>Inspecting Batch Streams...</span>
                </>
              ) : (
                <>
                  <Sparkles className="h-3.5 w-3.5 text-rose-400" />
                  <span>Parse {rawUrl.split('\n').filter((l) => l.trim()).length || ''} URLs</span>
                </>
              )}
            </button>
          </div>
        )}
      </div>

      {/* Fetching Skeleton Spinner */}
      {isFetching && (
        <div className="mt-4 rounded-xl border border-neutral-800/80 bg-neutral-950/60 p-4 text-center">
          <Loader2 className="mx-auto h-6 w-6 animate-spin text-rose-500" />
          <p className="mt-2 text-xs font-medium text-neutral-200">
            Querying YouTube manifests & extracting available audio/video streams...
          </p>
          <p className="text-[11px] text-neutral-400 mt-0.5">
            Detecting HDR 4K 60fps, 1080p MP4, and 320kbps Audio streams
          </p>
        </div>
      )}

      {/* Fetched Stream Results & Quality Selector */}
      {fetchedDrafts.length > 0 && (
        <div className="mt-4 space-y-3 rounded-xl border border-neutral-800 bg-neutral-950/90 p-4">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-neutral-800/80 pb-3">
            <div>
              <h3 className="text-xs font-bold uppercase tracking-wider text-neutral-300 flex items-center gap-1.5">
                <Check className="h-4 w-4 text-emerald-400" />
                <span>Found {fetchedDrafts.length} Item{fetchedDrafts.length > 1 ? 's' : ''} Ready to Download</span>
              </h3>
              <p className="text-[11px] text-neutral-400">
                Select your preferred quality and subfolder destination before starting
              </p>
            </div>

            {/* Quick Batch Format Overrides */}
            {fetchedDrafts.length > 1 && (
              <div className="flex items-center gap-2 text-xs">
                <span className="text-neutral-400 text-[11px]">Batch set:</span>
                <button
                  onClick={() => handleApplyGlobalSettingsToDrafts('video', '2160p')}
                  className="rounded px-2 py-0.5 text-[11px] font-medium bg-neutral-800 hover:bg-neutral-700 text-neutral-200"
                >
                  All 4K
                </button>
                <button
                  onClick={() => handleApplyGlobalSettingsToDrafts('video', '1080p')}
                  className="rounded px-2 py-0.5 text-[11px] font-medium bg-neutral-800 hover:bg-neutral-700 text-neutral-200"
                >
                  All 1080p
                </button>
                <button
                  onClick={() => handleApplyGlobalSettingsToDrafts('audio', '1080p')}
                  className="rounded px-2 py-0.5 text-[11px] font-medium bg-neutral-800 hover:bg-neutral-700 text-neutral-200"
                >
                  All MP3 Audio
                </button>
              </div>
            )}
          </div>

          {/* Cards for each inspected item */}
          <div className="max-h-[380px] space-y-2.5 overflow-y-auto pr-1">
            {fetchedDrafts.map((draft) => {
              const estMb = draft.mediaType === 'video'
                ? draft.selectedResolution.estimatedSizeMb
                : draft.selectedAudio.estimatedSizeMb;
              const { subfolder, fileName } = computeDestination(
                draft.title,
                draft.channel,
                draft.selectedResolution.label,
                draft.category,
                draft.mediaType,
                settings,
                draft.selectedAudio.format
              );

              return (
                <div
                  key={draft.id}
                  className="flex flex-col gap-3 rounded-xl border border-neutral-800/80 bg-neutral-900/60 p-3 transition-colors hover:border-neutral-700 sm:flex-row sm:items-center"
                >
                  {/* Thumbnail with duration badge */}
                  <div className="relative h-20 w-36 shrink-0 overflow-hidden rounded-lg bg-neutral-950">
                    <img
                      src={draft.thumbnailUrl}
                      alt={draft.title}
                      className="h-full w-full object-cover"
                      referrerPolicy="no-referrer"
                    />
                    <span className="absolute bottom-1 right-1 rounded bg-black/80 px-1 py-0.5 font-mono text-[10px] font-medium text-neutral-200">
                      {draft.duration}
                    </span>
                    {draft.selectedResolution.isHdr && draft.mediaType === 'video' && (
                      <span className="absolute top-1 left-1 rounded bg-amber-500/90 px-1 py-0.2 text-[9px] font-black text-black">
                        HDR
                      </span>
                    )}
                  </div>

                  {/* Title & Channel details */}
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex items-center gap-1.5 text-xs text-neutral-400">
                      <span className="font-semibold text-rose-400">{draft.channel}</span>
                      <span>•</span>
                      <span>{draft.publishDate}</span>
                      {draft.playlistTitle && (
                        <span className="rounded bg-neutral-800 px-1.5 py-0.2 text-[10px] text-neutral-300">
                          Playlist
                        </span>
                      )}
                    </div>
                    <h4 className="truncate text-xs font-semibold text-white sm:text-sm" title={draft.title}>
                      {draft.title}
                    </h4>

                    {/* Path preview */}
                    <div className="flex items-center gap-1.5 text-[11px] text-neutral-400">
                      <Folder className="h-3 w-3 shrink-0 text-amber-400/80" />
                      <span className="truncate font-mono">{subfolder.split('/').slice(-2).join('/')}/{fileName}</span>
                    </div>
                  </div>

                  {/* Format & Resolution selectors */}
                  <div className="flex flex-wrap items-center gap-2 sm:flex-col sm:items-end">
                    {/* Media Type toggle */}
                    <div className="flex rounded-lg bg-neutral-950 p-0.5 border border-neutral-800 text-[11px]">
                      <button
                        onClick={() => handleUpdateDraftMediaType(draft.id, 'video')}
                        className={`flex items-center gap-1 px-2 py-1 rounded ${
                          draft.mediaType === 'video'
                            ? 'bg-neutral-800 text-white font-semibold'
                            : 'text-neutral-400'
                        }`}
                      >
                        <Film className="h-3 w-3 text-rose-400" />
                        <span>Video</span>
                      </button>
                      <button
                        onClick={() => handleUpdateDraftMediaType(draft.id, 'audio')}
                        className={`flex items-center gap-1 px-2 py-1 rounded ${
                          draft.mediaType === 'audio'
                            ? 'bg-neutral-800 text-white font-semibold'
                            : 'text-neutral-400'
                        }`}
                      >
                        <Music className="h-3 w-3 text-amber-400" />
                        <span>Audio Only</span>
                      </button>
                    </div>

                    {/* Resolution / Quality dropdown */}
                    {draft.mediaType === 'video' ? (
                      <div className="flex items-center gap-1.5">
                        <select
                          id={`res-select-${draft.id}`}
                          value={draft.selectedResolution.id}
                          onChange={(e) => handleUpdateDraftResolution(draft.id, e.target.value)}
                          className="rounded-lg border border-neutral-700 bg-neutral-950 px-2 py-1 text-xs font-medium text-neutral-100 focus:border-rose-500 focus:outline-none"
                        >
                          {DEFAULT_RESOLUTIONS.map((opt) => (
                            <option key={opt.id} value={opt.id}>
                              {opt.label} (~{opt.estimatedSizeMb} MB)
                            </option>
                          ))}
                        </select>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5">
                        <select
                          id={`audio-select-${draft.id}`}
                          value={draft.selectedAudio.id}
                          onChange={(e) => {
                            const opt = DEFAULT_AUDIO_OPTIONS.find((a) => a.id === e.target.value) || DEFAULT_AUDIO_OPTIONS[0];
                            setFetchedDrafts((prev) =>
                              prev.map((d) => (d.id === draft.id ? { ...d, selectedAudio: opt } : d))
                            );
                          }}
                          className="rounded-lg border border-neutral-700 bg-neutral-950 px-2 py-1 text-xs font-medium text-neutral-100 focus:border-rose-500 focus:outline-none"
                        >
                          {DEFAULT_AUDIO_OPTIONS.map((opt) => (
                            <option key={opt.id} value={opt.id}>
                              {opt.label} (~{opt.estimatedSizeMb} MB)
                            </option>
                          ))}
                        </select>
                      </div>
                    )}

                    {/* Category tagger */}
                    <div className="flex items-center gap-1 text-[11px]">
                      <span className="text-neutral-400">Category:</span>
                      <select
                        value={draft.category}
                        onChange={(e) => handleUpdateDraftCategory(draft.id, e.target.value)}
                        className="rounded border border-neutral-800 bg-neutral-950 px-1.5 py-0.5 text-[11px] text-neutral-300"
                      >
                        {settings.userCategories.map((cat) => (
                          <option key={cat} value={cat}>
                            {cat}
                          </option>
                        ))}
                      </select>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Action Footer */}
          <div className="flex flex-wrap items-center justify-between gap-3 pt-3 border-t border-neutral-800">
            <label className="flex items-center gap-2 text-xs text-neutral-300 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={startImmediately}
                onChange={(e) => setStartImmediately(e.target.checked)}
                className="h-4 w-4 rounded border-neutral-700 bg-neutral-950 text-rose-600 focus:ring-rose-500"
              />
              <span>Start download immediately (otherwise add to queue)</span>
            </label>

            <div className="flex items-center gap-2">
              <button
                id="btn-cancel-drafts"
                onClick={() => setFetchedDrafts([])}
                className="rounded-lg px-3 py-1.5 text-xs font-medium text-neutral-400 hover:text-neutral-200"
              >
                Clear
              </button>
              <button
                id="btn-confirm-add-all"
                onClick={handleConfirmAndAdd}
                className="flex items-center gap-1.5 rounded-lg bg-rose-600 px-4 py-2 text-xs font-bold text-white shadow-lg shadow-rose-950/50 hover:bg-rose-500 active:scale-95 transition-all"
              >
                <span>Process & Add {fetchedDrafts.length} Item{fetchedDrafts.length > 1 ? 's' : ''}</span>
                <ArrowRight className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
