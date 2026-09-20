import React, { useState } from 'react';
import { 
  X, 
  Folder, 
  ChevronRight, 
  HardDrive, 
  Film, 
  Music, 
  Check, 
  FileCode, 
  FolderPlus,
  ArrowUp
} from 'lucide-react';
import { VideoItem, DownloadSettings } from '../types';
import { formatBytes } from '../data/mockData';

interface FolderBrowserModalProps {
  initialPath: string;
  isOpen: boolean;
  onClose: () => void;
  onSelectPath?: (newPath: string) => void;
  allVideos: VideoItem[];
  settings: DownloadSettings;
}

export const FolderBrowserModal: React.FC<FolderBrowserModalProps> = ({
  initialPath,
  isOpen,
  onClose,
  onSelectPath,
  allVideos,
  settings,
}) => {
  if (!isOpen) return null;

  const [currentPath, setCurrentPath] = useState(initialPath || settings.downloadLocation);

  // Derive subfolder structure from actual videos and categories
  const channels = Array.from(new Set(allVideos.map((v) => v.channel)));
  const categories = settings.userCategories;

  const isAtRoot = currentPath === settings.downloadLocation;
  const currentFolderName = currentPath.split('/').pop() || 'YouTube_Vault';

  // Files in this current path
  const filesInCurrentFolder = allVideos.filter((v) => {
    if (isAtRoot && settings.subfolderSorting === 'flat') return true;
    return v.subfolderPath === currentPath;
  });

  const handleNavigateSubfolder = (name: string) => {
    setCurrentPath(`${settings.downloadLocation}/${name}`);
  };

  const handleNavigateUp = () => {
    setCurrentPath(settings.downloadLocation);
  };

  const handleConfirmSelect = () => {
    if (onSelectPath) {
      onSelectPath(currentPath);
    }
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-md">
      <div className="relative w-full max-w-2xl overflow-hidden rounded-2xl border border-neutral-800 bg-neutral-950 shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-neutral-800 bg-neutral-900/80 px-4 py-3">
          <div className="flex items-center gap-2 text-xs font-semibold text-white">
            <Folder className="h-4 w-4 text-amber-400" />
            <span>Filesystem Subfolder Explorer</span>
          </div>
          <button
            onClick={onClose}
            className="rounded p-1 text-neutral-400 hover:bg-neutral-800 hover:text-white"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Path Breadcrumbs Bar */}
        <div className="flex items-center gap-1.5 border-b border-neutral-800/80 bg-neutral-900/40 px-4 py-2 text-xs font-mono text-neutral-300 overflow-x-auto">
          <button
            onClick={() => setCurrentPath('/Users/alex/Downloads')}
            className="text-neutral-400 hover:text-white flex items-center gap-1"
          >
            <HardDrive className="h-3 w-3 text-neutral-500" />
            <span>Downloads</span>
          </button>
          <ChevronRight className="h-3 w-3 text-neutral-600" />
          <button
            onClick={() => setCurrentPath(settings.downloadLocation)}
            className={`hover:text-white ${isAtRoot ? 'text-rose-400 font-bold' : 'text-neutral-400'}`}
          >
            YouTube_Vault
          </button>
          {!isAtRoot && (
            <>
              <ChevronRight className="h-3 w-3 text-neutral-600" />
              <span className="text-rose-400 font-bold">{currentFolderName}</span>
            </>
          )}
        </div>

        {/* Explorer Content */}
        <div className="max-h-[380px] min-h-[260px] overflow-y-auto p-4 space-y-2">
          {!isAtRoot && (
            <button
              onClick={handleNavigateUp}
              className="w-full flex items-center gap-2 rounded-lg border border-neutral-800/80 bg-neutral-900/30 px-3 py-2 text-xs text-neutral-300 hover:bg-neutral-800 hover:text-white transition-colors"
            >
              <ArrowUp className="h-3.5 w-3.5 text-neutral-400" />
              <span>.. (Parent Directory)</span>
            </button>
          )}

          {/* If at root, list subdirectories (by channel or by category) */}
          {isAtRoot && (
            <div className="space-y-1.5">
              <div className="text-[11px] font-semibold text-neutral-400 uppercase tracking-wider mb-2">
                Subfolders ({settings.subfolderSorting === 'category' ? 'Categorized' : 'Channel Folders'}):
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                {(settings.subfolderSorting === 'category' ? categories : channels).map((folder) => {
                  const itemsCount = allVideos.filter((v) =>
                    settings.subfolderSorting === 'category' ? v.category === folder : v.channel === folder
                  ).length;

                  return (
                    <div
                      key={folder}
                      onClick={() => handleNavigateSubfolder(folder)}
                      className="flex cursor-pointer items-center justify-between rounded-xl border border-neutral-800 bg-neutral-900/60 p-2.5 hover:border-neutral-700 hover:bg-neutral-800/80 transition-all"
                    >
                      <div className="flex items-center gap-2.5 truncate">
                        <Folder className="h-4 w-4 text-amber-400 shrink-0" />
                        <span className="truncate text-xs font-semibold text-neutral-200">
                          {folder}
                        </span>
                      </div>
                      <span className="rounded bg-neutral-950 px-1.5 py-0.5 font-mono text-[10px] text-neutral-400">
                        {itemsCount} files
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* Files inside current folder */}
          {filesInCurrentFolder.length > 0 ? (
            <div className="pt-2 space-y-1.5">
              <div className="text-[11px] font-semibold text-neutral-400 uppercase tracking-wider mb-1">
                Media Files in this Directory ({filesInCurrentFolder.length}):
              </div>
              {filesInCurrentFolder.map((file) => (
                <div
                  key={file.id}
                  className="flex items-center justify-between rounded-lg border border-neutral-800/60 bg-neutral-900/40 px-3 py-2 text-xs"
                >
                  <div className="flex items-center gap-2.5 truncate">
                    <img
                      src={file.thumbnailUrl}
                      alt={file.title}
                      className="h-7 w-10 shrink-0 rounded object-cover"
                      referrerPolicy="no-referrer"
                    />
                    <div className="min-w-0">
                      <p className="truncate font-mono text-[11px] text-neutral-200" title={file.fileName}>
                        {file.fileName}
                      </p>
                      <p className="text-[10px] text-neutral-400">
                        {file.selectedResolution.label.split(' ')[0]} • {file.duration}
                      </p>
                    </div>
                  </div>
                  <span className="shrink-0 font-mono text-[11px] text-neutral-400 ml-2">
                    {formatBytes(file.totalBytes)}
                  </span>
                </div>
              ))}
            </div>
          ) : !isAtRoot ? (
            <div className="p-8 text-center text-xs text-neutral-400">
              No files currently saved in this subfolder.
            </div>
          ) : null}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between border-t border-neutral-800 bg-neutral-900/80 px-4 py-3">
          <div className="text-xs font-mono text-neutral-400 truncate max-w-sm">
            Target: <span className="text-neutral-200">{currentPath}</span>
          </div>

          <div className="flex items-center gap-2">
            <button
              onClick={onClose}
              className="rounded-lg px-3 py-1.5 text-xs text-neutral-400 hover:text-white"
            >
              Cancel
            </button>
            <button
              onClick={handleConfirmSelect}
              className="flex items-center gap-1.5 rounded-lg bg-rose-600 px-3.5 py-1.5 text-xs font-bold text-white hover:bg-rose-500 shadow-sm"
            >
              <Check className="h-3.5 w-3.5" />
              <span>Select This Location</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
