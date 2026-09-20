import React, { useState, useEffect, useRef } from 'react';
import { 
  Header 
} from './components/Header';
import { 
  UrlInputBar 
} from './components/UrlInputBar';
import { 
  QueueDashboard 
} from './components/QueueDashboard';
import { 
  CompletedLibrary 
} from './components/CompletedLibrary';
import { 
  SettingsView 
} from './components/SettingsView';
import { 
  VideoPlayerModal 
} from './components/VideoPlayerModal';
import { 
  FolderBrowserModal 
} from './components/FolderBrowserModal';
import { 
  VideoItem, 
  DownloadSettings, 
  ActiveTab 
} from './types';
import { 
  SAMPLE_VIDEOS, 
  DEFAULT_SETTINGS, 
  DEFAULT_RESOLUTIONS,
  computeDestination 
} from './data/mockData';
import { 
  DownloadCloud, 
  Layers, 
  Film, 
  Settings as SettingsIcon, 
  Folder, 
  Plus, 
  X,
  Sparkles,
  ArrowRight
} from 'lucide-react';

export default function App() {
  // Load initial settings and videos from localStorage if available
  const [settings, setSettings] = useState<DownloadSettings>(() => {
    try {
      const saved = localStorage.getItem('yt_downloader_settings');
      if (saved) return JSON.parse(saved);
    } catch (e) {
      console.warn('Failed reading settings', e);
    }
    return DEFAULT_SETTINGS;
  });

  const [videos, setVideos] = useState<VideoItem[]>(() => {
    try {
      const saved = localStorage.getItem('yt_downloader_videos');
      if (saved) return JSON.parse(saved);
    } catch (e) {
      console.warn('Failed reading videos', e);
    }
    return SAMPLE_VIDEOS;
  });

  const [activeTab, setActiveTab] = useState<ActiveTab>('queue');
  const [isAddModalOpen, setIsAddModalOpen] = useState(false);
  const [selectedPreviewVideo, setSelectedPreviewVideo] = useState<VideoItem | null>(null);
  const [folderModalState, setFolderModalState] = useState<{ isOpen: boolean; path: string }>({
    isOpen: false,
    path: '',
  });

  // Save to localStorage
  useEffect(() => {
    try {
      localStorage.setItem('yt_downloader_settings', JSON.stringify(settings));
    } catch (e) {
      console.warn('Failed saving settings', e);
    }
  }, [settings]);

  useEffect(() => {
    try {
      localStorage.setItem('yt_downloader_videos', JSON.stringify(videos));
    } catch (e) {
      console.warn('Failed saving videos', e);
    }
  }, [videos]);

  // Realistic Download Simulation Engine
  useEffect(() => {
    const timer = setInterval(() => {
      setVideos((prevVideos) => {
        // Count currently active downloading items
        const activeCount = prevVideos.filter((v) => v.status === 'downloading').length;
        const availableSlots = Math.max(0, settings.maxConcurrentDownloads - activeCount);

        let promotedQueued = 0;

        return prevVideos.map((item) => {
          // Promote queued to downloading if slot is open
          if (item.status === 'queued' && promotedQueued < availableSlots) {
            promotedQueued++;
            const baseSpeed = (12 + Math.random() * 8) * 1024 * 1024;
            return {
              ...item,
              status: 'downloading',
              speedBytesPerSec: baseSpeed,
              etaSeconds: Math.max(1, Math.round((item.totalBytes - item.downloadedBytes) / baseSpeed)),
            };
          }

          // Progress active downloads
          if (item.status === 'downloading') {
            // Speed fluctuation
            const speedJitter = 0.9 + Math.random() * 0.2;
            const currentSpeed = Math.max(2 * 1024 * 1024, item.speedBytesPerSec * speedJitter);
            const chunk = currentSpeed * 0.8;
            const newDownloaded = Math.min(item.totalBytes, item.downloadedBytes + chunk);
            const progress = (newDownloaded / item.totalBytes) * 100;
            const remainingBytes = Math.max(0, item.totalBytes - newDownloaded);
            const eta = Math.ceil(remainingBytes / (currentSpeed || 1));

            // Switch to audio/video merging / thumbnail embedding when nearly done
            if (progress >= 98) {
              return {
                ...item,
                status: 'processing',
                progress: 99,
                downloadedBytes: item.totalBytes,
                speedBytesPerSec: 1.5 * 1024 * 1024,
                etaSeconds: 2,
              };
            }

            return {
              ...item,
              downloadedBytes: newDownloaded,
              progress,
              speedBytesPerSec: currentSpeed,
              etaSeconds: eta,
            };
          }

          // Finish processing
          if (item.status === 'processing') {
            return {
              ...item,
              status: 'completed',
              progress: 100,
              downloadedBytes: item.totalBytes,
              speedBytesPerSec: 0,
              etaSeconds: 0,
              downloadedAt: 'Just now',
            };
          }

          return item;
        });
      });
    }, 850);

    return () => clearInterval(timer);
  }, [settings.maxConcurrentDownloads]);

  // Handlers
  const handleAddVideos = (newVideos: VideoItem[], startImmediately: boolean) => {
    setVideos((prev) => [...newVideos, ...prev]);
    setActiveTab('queue');
  };

  const handleTogglePause = (id: string) => {
    setVideos((prev) =>
      prev.map((v) => {
        if (v.id !== id) return v;
        if (v.status === 'downloading' || v.status === 'processing') {
          return { ...v, status: 'paused', speedBytesPerSec: 0 };
        }
        if (v.status === 'paused' || v.status === 'queued') {
          return {
            ...v,
            status: 'downloading',
            speedBytesPerSec: (15 + Math.random() * 5) * 1024 * 1024,
          };
        }
        return v;
      })
    );
  };

  const handleCancelItem = (id: string) => {
    setVideos((prev) => prev.filter((v) => v.id !== id));
  };

  const handleRetryItem = (id: string) => {
    setVideos((prev) =>
      prev.map((v) =>
        v.id === id
          ? {
              ...v,
              status: 'downloading',
              progress: 0,
              downloadedBytes: 0,
              speedBytesPerSec: 16 * 1024 * 1024,
            }
          : v
      )
    );
  };

  const handleStartAll = () => {
    setVideos((prev) =>
      prev.map((v) => {
        if (v.status === 'queued' || v.status === 'paused') {
          return {
            ...v,
            status: 'downloading',
            speedBytesPerSec: (14 + Math.random() * 6) * 1024 * 1024,
          };
        }
        return v;
      })
    );
  };

  const handlePauseAll = () => {
    setVideos((prev) =>
      prev.map((v) => {
        if (v.status === 'downloading' || v.status === 'processing') {
          return { ...v, status: 'paused', speedBytesPerSec: 0 };
        }
        return v;
      })
    );
  };

  const handleClearCompleted = () => {
    // Remove completed from the active queue, but note completed items are in Library
    // In our single data model, we can filter them out of the queue view or reset
    setVideos((prev) => prev.filter((v) => v.status !== 'completed'));
  };

  const handleChangeItemResolution = (id: string, resId: string) => {
    const res = DEFAULT_RESOLUTIONS.find((r) => r.id === resId) || DEFAULT_RESOLUTIONS[2];
    setVideos((prev) =>
      prev.map((v) => {
        if (v.id !== id) return v;
        const estMb = res.estimatedSizeMb;
        const totalBytes = estMb * 1024 * 1024;
        const { subfolder, fileName } = computeDestination(
          v.title,
          v.channel,
          res.label,
          v.category,
          v.mediaType,
          settings
        );
        return {
          ...v,
          selectedResolution: res,
          totalBytes,
          subfolderPath: subfolder,
          fileName,
        };
      })
    );
  };

  const handleDeleteLibraryVideo = (id: string) => {
    setVideos((prev) => prev.filter((v) => v.id !== id));
  };

  const handleSaveSettings = (newSettings: DownloadSettings) => {
    setSettings(newSettings);
    // Recalculate paths for uncompleted items based on new rules
    setVideos((prev) =>
      prev.map((v) => {
        const { subfolder, fileName } = computeDestination(
          v.title,
          v.channel,
          v.selectedResolution.label,
          v.category,
          v.mediaType,
          newSettings,
          v.selectedAudio?.format
        );
        return {
          ...v,
          subfolderPath: subfolder,
          fileName,
        };
      })
    );
  };

  // Aggregates for Header
  const activeDownloads = videos.filter((v) => v.status === 'downloading' || v.status === 'processing');
  const completedVideos = videos.filter((v) => v.status === 'completed');
  const queueItems = videos.filter((v) => v.status !== 'completed');
  const totalCurrentSpeed = activeDownloads.reduce((sum, v) => sum + v.speedBytesPerSec, 0);

  return (
    <div className="min-h-screen bg-[#0c0d10] text-neutral-100 selection:bg-rose-600 selection:text-white flex flex-col font-sans">
      {/* Sticky App Header */}
      <Header
        activeTab={activeTab}
        setActiveTab={setActiveTab}
        activeDownloadsCount={activeDownloads.length}
        totalCompletedCount={completedVideos.length}
        currentTotalSpeed={totalCurrentSpeed}
        onOpenAddModal={() => setIsAddModalOpen(true)}
        downloadLocation={settings.downloadLocation}
      />

      {/* Main Container */}
      <main className="flex-1 mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 space-y-6">
        {/* URL Input Bar (always accessible on Queue page, or modal) */}
        {activeTab === 'queue' && (
          <div className="space-y-2">
            <div className="flex items-center justify-between px-1">
              <h2 className="text-xs font-bold uppercase tracking-wider text-neutral-400 flex items-center gap-1.5">
                <Plus className="h-3.5 w-3.5 text-rose-500" />
                <span>Add Links & Batch Process</span>
              </h2>
              <span className="text-[11px] text-neutral-400">
                Automatic resolution fetching enabled
              </span>
            </div>
            <UrlInputBar
              onAddVideos={handleAddVideos}
              settings={settings}
            />
          </div>
        )}

        {/* Tab 1: Queue & Active Batch Downloads */}
        {activeTab === 'queue' && (
          <div className="space-y-3">
            <div className="flex items-center justify-between px-1 pt-2">
              <h2 className="text-xs font-bold uppercase tracking-wider text-neutral-400 flex items-center gap-1.5">
                <Layers className="h-3.5 w-3.5 text-rose-500" />
                <span>Active Downloads & Batch Queue ({queueItems.length})</span>
              </h2>
            </div>

            <QueueDashboard
              queueItems={queueItems}
              onTogglePause={handleTogglePause}
              onCancelItem={handleCancelItem}
              onRetryItem={handleRetryItem}
              onStartAll={handleStartAll}
              onPauseAll={handlePauseAll}
              onClearCompleted={handleClearCompleted}
              onChangeItemResolution={handleChangeItemResolution}
              onOpenLibrary={() => setActiveTab('library')}
              onOpenFolderModal={(path) => setFolderModalState({ isOpen: true, path })}
              settings={settings}
            />
          </div>
        )}

        {/* Tab 2: Visual Completed Library */}
        {activeTab === 'library' && (
          <CompletedLibrary
            completedVideos={completedVideos}
            onPlayVideo={(video) => setSelectedPreviewVideo(video)}
            onDeleteVideo={handleDeleteLibraryVideo}
            onOpenFolderModal={(path) => setFolderModalState({ isOpen: true, path })}
            settings={settings}
          />
        )}

        {/* Tab 3: Settings Page */}
        {activeTab === 'settings' && (
          <SettingsView
            settings={settings}
            onSaveSettings={handleSaveSettings}
            onOpenFolderModal={(path) => setFolderModalState({ isOpen: true, path })}
          />
        )}
      </main>

      {/* Floating Add Modal if triggered from Header button while on another tab */}
      {isAddModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-md">
          <div className="relative w-full max-w-3xl">
            <button
              onClick={() => setIsAddModalOpen(false)}
              className="absolute -top-3 -right-3 z-10 flex h-8 w-8 items-center justify-center rounded-full bg-neutral-800 text-white hover:bg-neutral-700 shadow-lg"
            >
              <X className="h-4 w-4" />
            </button>
            <UrlInputBar
              onAddVideos={handleAddVideos}
              settings={settings}
              isOpenAsModal={true}
              onCloseModal={() => setIsAddModalOpen(false)}
            />
          </div>
        </div>
      )}

      {/* Video Preview Modal */}
      {selectedPreviewVideo && (
        <VideoPlayerModal
          video={selectedPreviewVideo}
          onClose={() => setSelectedPreviewVideo(null)}
          onOpenFolder={(path) => {
            setSelectedPreviewVideo(null);
            setFolderModalState({ isOpen: true, path });
          }}
        />
      )}

      {/* Folder Subfolder Explorer Modal */}
      <FolderBrowserModal
        initialPath={folderModalState.path}
        isOpen={folderModalState.isOpen}
        onClose={() => setFolderModalState({ isOpen: false, path: '' })}
        onSelectPath={(newPath) => {
          setSettings((prev) => ({ ...prev, downloadLocation: newPath }));
        }}
        allVideos={videos}
        settings={settings}
      />
    </div>
  );
}
