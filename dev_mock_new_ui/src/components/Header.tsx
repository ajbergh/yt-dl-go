import React from 'react';
import { 
  DownloadCloud, 
  Film, 
  FolderArchive, 
  Settings, 
  HardDrive, 
  Activity, 
  Plus, 
  Layers
} from 'lucide-react';
import { ActiveTab } from '../types';
import { formatSpeed } from '../data/mockData';

interface HeaderProps {
  activeTab: ActiveTab;
  setActiveTab: (tab: ActiveTab) => void;
  activeDownloadsCount: number;
  totalCompletedCount: number;
  currentTotalSpeed: number;
  onOpenAddModal: () => void;
  downloadLocation: string;
}

export const Header: React.FC<HeaderProps> = ({
  activeTab,
  setActiveTab,
  activeDownloadsCount,
  totalCompletedCount,
  currentTotalSpeed,
  onOpenAddModal,
  downloadLocation,
}) => {
  return (
    <header className="sticky top-0 z-30 border-b border-neutral-800/80 bg-neutral-950/90 backdrop-blur-md">
      <div className="mx-auto flex max-w-7xl items-center justify-between px-4 py-3 sm:px-6">
        {/* Brand */}
        <div className="flex items-center gap-3">
          <div className="relative flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-rose-600 via-red-600 to-rose-700 shadow-lg shadow-rose-950/40">
            <DownloadCloud className="h-5 w-5 text-white" />
            {activeDownloadsCount > 0 && (
              <span className="absolute -top-1 -right-1 flex h-3.5 w-3.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-rose-400 opacity-75"></span>
                <span className="relative inline-flex h-3.5 w-3.5 rounded-full bg-rose-500 text-[9px] font-bold text-white items-center justify-center">
                  {activeDownloadsCount}
                </span>
              </span>
            )}
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-base font-bold tracking-tight text-white sm:text-lg">
                YouTube Downloader
              </h1>
              <span className="rounded bg-rose-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-rose-400 border border-rose-500/20">
                PRO BATCH
              </span>
            </div>
            <p className="text-xs text-neutral-400 hidden sm:block">
              Multi-thread Media & Playlist Grabber
            </p>
          </div>
        </div>

        {/* Center Tabs */}
        <nav className="flex items-center gap-1 rounded-xl bg-neutral-900/90 p-1 border border-neutral-800">
          <button
            id="tab-queue"
            onClick={() => setActiveTab('queue')}
            className={`flex items-center gap-2 rounded-lg px-3.5 py-1.5 text-xs font-semibold transition-all ${
              activeTab === 'queue'
                ? 'bg-neutral-800 text-white shadow-sm'
                : 'text-neutral-400 hover:text-neutral-200'
            }`}
          >
            <Layers className="h-3.5 w-3.5 text-rose-400" />
            <span>Queue & Batch</span>
            {activeDownloadsCount > 0 && (
              <span className="rounded-full bg-rose-600 px-1.5 py-0.2 text-[10px] text-white">
                {activeDownloadsCount}
              </span>
            )}
          </button>

          <button
            id="tab-library"
            onClick={() => setActiveTab('library')}
            className={`flex items-center gap-2 rounded-lg px-3.5 py-1.5 text-xs font-semibold transition-all ${
              activeTab === 'library'
                ? 'bg-neutral-800 text-white shadow-sm'
                : 'text-neutral-400 hover:text-neutral-200'
            }`}
          >
            <Film className="h-3.5 w-3.5 text-emerald-400" />
            <span>Library</span>
            <span className="rounded-full bg-neutral-800 px-1.5 py-0.2 text-[10px] text-neutral-300">
              {totalCompletedCount}
            </span>
          </button>

          <button
            id="tab-settings"
            onClick={() => setActiveTab('settings')}
            className={`flex items-center gap-2 rounded-lg px-3.5 py-1.5 text-xs font-semibold transition-all ${
              activeTab === 'settings'
                ? 'bg-neutral-800 text-white shadow-sm'
                : 'text-neutral-400 hover:text-neutral-200'
            }`}
          >
            <Settings className="h-3.5 w-3.5 text-neutral-400" />
            <span>Settings</span>
          </button>
        </nav>

        {/* Right Stats & Add Link CTA */}
        <div className="flex items-center gap-3">
          {/* Live Speed indicator if active */}
          {activeDownloadsCount > 0 ? (
            <div className="hidden md:flex items-center gap-2 rounded-lg border border-neutral-800 bg-neutral-900/60 px-2.5 py-1 text-xs">
              <Activity className="h-3.5 w-3.5 text-emerald-400 animate-pulse" />
              <span className="text-neutral-400">Total Speed:</span>
              <span className="font-mono font-medium text-emerald-400">
                {formatSpeed(currentTotalSpeed)}
              </span>
            </div>
          ) : (
            <div className="hidden lg:flex items-center gap-2 rounded-lg border border-neutral-800/80 bg-neutral-900/40 px-2.5 py-1 text-xs text-neutral-400">
              <HardDrive className="h-3.5 w-3.5 text-neutral-400" />
              <span className="truncate max-w-[120px]">{downloadLocation.split('/').pop()}</span>
              <span className="text-neutral-500 font-mono">412 GB Free</span>
            </div>
          )}

          {/* Add Link / Batch Button */}
          <button
            id="btn-add-links-header"
            onClick={onOpenAddModal}
            className="flex items-center gap-1.5 rounded-lg bg-rose-600 px-3.5 py-1.5 text-xs font-semibold text-white shadow-md shadow-rose-950/40 transition-all hover:bg-rose-500 active:scale-95"
          >
            <Plus className="h-4 w-4" />
            <span className="hidden sm:inline">Add Links</span>
          </button>
        </div>
      </div>
    </header>
  );
};
