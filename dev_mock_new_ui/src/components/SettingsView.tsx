import React, { useState } from 'react';
import { 
  Folder, 
  HardDrive, 
  FileText, 
  FolderTree, 
  Sliders, 
  Sparkles, 
  Check, 
  Plus, 
  X, 
  Info, 
  Save, 
  CheckCircle2,
  RefreshCw,
  FolderOpen
} from 'lucide-react';
import { DownloadSettings } from '../types';

interface SettingsViewProps {
  settings: DownloadSettings;
  onSaveSettings: (newSettings: DownloadSettings) => void;
  onOpenFolderModal: (path: string) => void;
}

export const SettingsView: React.FC<SettingsViewProps> = ({
  settings,
  onSaveSettings,
  onOpenFolderModal,
}) => {
  const [formData, setFormData] = useState<DownloadSettings>({ ...settings });
  const [newCategoryInput, setNewCategoryInput] = useState('');
  const [showSavedToast, setShowSavedToast] = useState(false);

  const tokenOptions = [
    { token: '{channel}', label: 'Channel Name', example: 'Veritasium' },
    { token: '{title}', label: 'Video Title', example: 'James Webb Space Telescope' },
    { token: '{resolution}', label: 'Resolution', example: '2160p' },
    { token: '{category}', label: 'Category', example: 'Science' },
  ];

  // Insert token into naming pattern
  const handleInsertToken = (token: string) => {
    setFormData((prev) => ({
      ...prev,
      namingPattern: `${prev.namingPattern} ${token}`,
    }));
  };

  // Add custom user category
  const handleAddCategory = () => {
    if (!newCategoryInput.trim()) return;
    const cat = newCategoryInput.trim();
    if (!formData.userCategories.includes(cat)) {
      setFormData((prev) => ({
        ...prev,
        userCategories: [...prev.userCategories, cat],
      }));
    }
    setNewCategoryInput('');
  };

  const handleRemoveCategory = (cat: string) => {
    setFormData((prev) => ({
      ...prev,
      userCategories: prev.userCategories.filter((c) => c !== cat),
    }));
  };

  const handleSave = () => {
    onSaveSettings(formData);
    setShowSavedToast(true);
    setTimeout(() => setShowSavedToast(false), 2500);
  };

  // Compute live preview of filename
  const sampleChannel = 'Marques Brownlee';
  const sampleTitle = 'M3 Max MacBook Pro Deep Dive';
  const sampleRes = '1080p';
  const sampleCat = 'Tech';

  const previewFileName = formData.namingPattern
    .replace('{channel}', sampleChannel)
    .replace('{title}', sampleTitle)
    .replace('{resolution}', sampleRes)
    .replace('{category}', sampleCat) + '.mp4';

  const previewSubfolder = formData.subfolderSorting === 'channel'
    ? `${formData.downloadLocation}/${sampleChannel}`
    : formData.subfolderSorting === 'category'
    ? `${formData.downloadLocation}/${sampleCat}`
    : formData.downloadLocation;

  return (
    <div className="mx-auto max-w-4xl space-y-6 pb-12">
      {/* Page Header */}
      <div className="flex items-center justify-between border-b border-neutral-800 pb-4">
        <div>
          <h2 className="text-lg font-bold text-white">
            Preferences & Output Configuration
          </h2>
          <p className="text-xs text-neutral-400">
            Customize download destinations, naming token formats, and subfolder sorting rules
          </p>
        </div>

        <button
          id="btn-save-settings"
          onClick={handleSave}
          className="flex items-center gap-1.5 rounded-lg bg-rose-600 px-4 py-2 text-xs font-bold text-white shadow-md shadow-rose-950/40 hover:bg-rose-500 transition-all active:scale-95"
        >
          {showSavedToast ? (
            <>
              <CheckCircle2 className="h-4 w-4 text-white" />
              <span>Saved!</span>
            </>
          ) : (
            <>
              <Save className="h-4 w-4" />
              <span>Save Preferences</span>
            </>
          )}
        </button>
      </div>

      {/* 1. Download Location Selection */}
      <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-5 space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-amber-500/10 border border-amber-500/20 text-amber-400">
              <Folder className="h-4 w-4" />
            </div>
            <div>
              <h3 className="text-sm font-semibold text-white">
                Download Directory Location
              </h3>
              <p className="text-xs text-neutral-400">
                Primary storage destination on your local or external drive
              </p>
            </div>
          </div>

          <button
            onClick={() => onOpenFolderModal(formData.downloadLocation)}
            className="flex items-center gap-1.5 rounded-lg border border-neutral-700 bg-neutral-800 px-3 py-1.5 text-xs font-semibold text-neutral-200 hover:bg-neutral-700 hover:text-white transition-colors"
          >
            <FolderOpen className="h-3.5 w-3.5 text-amber-400" />
            <span>Browse Path</span>
          </button>
        </div>

        <div className="flex items-center gap-2">
          <input
            id="input-download-location"
            type="text"
            value={formData.downloadLocation}
            onChange={(e) => setFormData({ ...formData, downloadLocation: e.target.value })}
            className="w-full rounded-xl border border-neutral-800 bg-neutral-950 px-3.5 py-2 font-mono text-xs text-neutral-200 focus:border-rose-500 focus:outline-none"
          />
        </div>

        {/* Directory Presets & Disk Gauge */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 pt-2">
          {/* Quick presets */}
          <div className="flex flex-wrap items-center gap-1.5 text-xs">
            <span className="text-neutral-500 text-[11px]">Quick Paths:</span>
            {[
              '/Users/alex/Downloads/YouTube_Vault',
              '/Volumes/ExternalSSD/Media',
              '~/Movies/YouTube',
            ].map((path) => (
              <button
                key={path}
                onClick={() => setFormData({ ...formData, downloadLocation: path })}
                className="rounded-md border border-neutral-800 bg-neutral-950 px-2 py-0.5 text-[11px] font-mono text-neutral-400 hover:border-neutral-700 hover:text-neutral-200"
              >
                {path.split('/').pop()}
              </button>
            ))}
          </div>

          {/* Disk space usage */}
          <div className="rounded-xl border border-neutral-800/80 bg-neutral-950/60 p-2.5 text-xs text-neutral-400">
            <div className="flex justify-between text-[11px] mb-1">
              <span>Drive Storage (NVMe M.2)</span>
              <span className="font-mono text-neutral-300">412 GB free of 1.0 TB</span>
            </div>
            <div className="h-1.5 w-full rounded-full bg-neutral-800 overflow-hidden">
              <div className="h-full bg-gradient-to-r from-emerald-500 to-rose-500" style={{ width: '58%' }} />
            </div>
          </div>
        </div>
      </div>

      {/* 2. File Naming Format Preferences */}
      <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-5 space-y-4">
        <div className="flex items-center gap-2.5">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-blue-500/10 border border-blue-500/20 text-blue-400">
            <FileText className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold text-white">
              File Naming Format Preferences
            </h3>
            <p className="text-xs text-neutral-400">
              Define custom naming pattern templates for saved media files
            </p>
          </div>
        </div>

        <div className="space-y-2">
          <label className="text-xs font-medium text-neutral-300">
            Naming Pattern Template:
          </label>
          <input
            id="input-naming-pattern"
            type="text"
            value={formData.namingPattern}
            onChange={(e) => setFormData({ ...formData, namingPattern: e.target.value })}
            className="w-full rounded-xl border border-neutral-800 bg-neutral-950 px-3.5 py-2 font-mono text-xs text-neutral-100 focus:border-rose-500 focus:outline-none"
          />

          {/* Clickable Token Pills */}
          <div className="flex flex-wrap items-center gap-1.5 pt-1">
            <span className="text-[11px] text-neutral-500">Insert token:</span>
            {tokenOptions.map((t) => (
              <button
                key={t.token}
                onClick={() => handleInsertToken(t.token)}
                className="flex items-center gap-1 rounded-lg border border-neutral-800 bg-neutral-950 px-2 py-1 text-[11px] font-mono text-neutral-300 hover:border-neutral-700 hover:text-white transition-colors"
                title={`Example: ${t.example}`}
              >
                <Plus className="h-3 w-3 text-rose-400" />
                <span>{t.token}</span>
              </button>
            ))}
          </div>

          {/* Live Preview Box */}
          <div className="mt-3 rounded-xl border border-neutral-800 bg-neutral-950 p-3">
            <div className="flex items-center gap-1.5 text-xs text-neutral-400 mb-1">
              <Sparkles className="h-3.5 w-3.5 text-amber-400" />
              <span className="font-semibold text-neutral-300">Live File Generation Preview:</span>
            </div>
            <p className="font-mono text-xs text-emerald-400 break-all">
              {previewFileName}
            </p>
            <p className="font-mono text-[11px] text-neutral-400 mt-1 break-all">
              📁 {previewSubfolder}/{previewFileName}
            </p>
          </div>
        </div>
      </div>

      {/* 3. Subfolder Sorting Rules */}
      <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-5 space-y-4">
        <div className="flex items-center gap-2.5">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-purple-500/10 border border-purple-500/20 text-purple-400">
            <FolderTree className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold text-white">
              Subfolder Sorting Organization
            </h3>
            <p className="text-xs text-neutral-400">
              Automatically organize saved media into structured directories
            </p>
          </div>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {/* Option A: By YouTube Channel */}
          <label
            onClick={() => setFormData({ ...formData, subfolderSorting: 'channel' })}
            className={`flex cursor-pointer flex-col justify-between rounded-xl border p-4 transition-all ${
              formData.subfolderSorting === 'channel'
                ? 'border-rose-500/60 bg-rose-950/20'
                : 'border-neutral-800 bg-neutral-950/60 hover:border-neutral-700'
            }`}
          >
            <div>
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-white">
                  Sort by YouTube Channel
                </span>
                <input
                  type="radio"
                  name="subfolder"
                  checked={formData.subfolderSorting === 'channel'}
                  onChange={() => {}}
                  className="text-rose-600 focus:ring-rose-500"
                />
              </div>
              <p className="mt-1 text-[11px] text-neutral-400">
                Videos from the same channel are placed together in dedicated channel folders.
              </p>
            </div>
            <div className="mt-3 rounded-lg bg-neutral-900 px-2.5 py-1 font-mono text-[10px] text-neutral-300">
              📁 /Downloads/YouTube_Vault/<b>Kurzgesagt</b>/video.mp4
            </div>
          </label>

          {/* Option B: By User-defined Category */}
          <label
            onClick={() => setFormData({ ...formData, subfolderSorting: 'category' })}
            className={`flex cursor-pointer flex-col justify-between rounded-xl border p-4 transition-all ${
              formData.subfolderSorting === 'category'
                ? 'border-rose-500/60 bg-rose-950/20'
                : 'border-neutral-800 bg-neutral-950/60 hover:border-neutral-700'
            }`}
          >
            <div>
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-white">
                  Sort by Category
                </span>
                <input
                  type="radio"
                  name="subfolder"
                  checked={formData.subfolderSorting === 'category'}
                  onChange={() => {}}
                  className="text-rose-600 focus:ring-rose-500"
                />
              </div>
              <p className="mt-1 text-[11px] text-neutral-400">
                Files are organized by topic (Music, Science, Coding, Tech, Podcasts).
              </p>
            </div>
            <div className="mt-3 rounded-lg bg-neutral-900 px-2.5 py-1 font-mono text-[10px] text-neutral-300">
              📁 /Downloads/YouTube_Vault/<b>Science</b>/video.mp4
            </div>
          </label>
        </div>

        {/* User Categories Manager */}
        <div className="rounded-xl border border-neutral-800/80 bg-neutral-950/60 p-3.5 space-y-2">
          <div className="flex items-center justify-between text-xs">
            <span className="font-semibold text-neutral-300">User-Defined Categories</span>
            <span className="text-[11px] text-neutral-500">{formData.userCategories.length} active</span>
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            {formData.userCategories.map((cat) => (
              <span
                key={cat}
                className="flex items-center gap-1 rounded-md bg-neutral-900 border border-neutral-800 px-2 py-1 text-xs text-neutral-200"
              >
                <span>{cat}</span>
                <button
                  onClick={() => handleRemoveCategory(cat)}
                  className="text-neutral-500 hover:text-rose-400 ml-0.5"
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>

          {/* Add custom category input */}
          <div className="flex items-center gap-2 pt-1">
            <input
              type="text"
              value={newCategoryInput}
              onChange={(e) => setNewCategoryInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleAddCategory();
              }}
              placeholder="Add new category (e.g., Tutorials, AI, Documentary)..."
              className="rounded-lg border border-neutral-800 bg-neutral-900 px-3 py-1.5 text-xs text-neutral-200 focus:border-rose-500 focus:outline-none"
            />
            <button
              onClick={handleAddCategory}
              className="flex items-center gap-1 rounded-lg bg-neutral-800 px-3 py-1.5 text-xs font-semibold text-neutral-200 hover:bg-neutral-700"
            >
              <Plus className="h-3.5 w-3.5" />
              <span>Add</span>
            </button>
          </div>
        </div>
      </div>

      {/* 4. Batch & Engine Settings */}
      <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-5 space-y-4">
        <div className="flex items-center gap-2.5">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-emerald-400">
            <Sliders className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold text-white">
              Batch Performance & Media Preservation
            </h3>
            <p className="text-xs text-neutral-400">
              Concurrency limits, embedded thumbnail artwork, and subtitle extraction
            </p>
          </div>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {/* Max Concurrent Downloads */}
          <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-3.5 space-y-2">
            <div className="flex justify-between text-xs">
              <span className="font-semibold text-neutral-300">Max Concurrent Downloads:</span>
              <span className="font-mono font-bold text-rose-400">{formData.maxConcurrentDownloads} Streams</span>
            </div>
            <input
              type="range"
              min={1}
              max={6}
              value={formData.maxConcurrentDownloads}
              onChange={(e) => setFormData({ ...formData, maxConcurrentDownloads: parseInt(e.target.value) })}
              className="w-full accent-rose-600"
            />
            <p className="text-[11px] text-neutral-500">
              Higher values increase aggregate download speed on gigabit connections.
            </p>
          </div>

          {/* Default video quality preference */}
          <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-3.5 space-y-2">
            <span className="text-xs font-semibold text-neutral-300 block">Default Video Quality:</span>
            <select
              value={formData.defaultVideoQuality}
              onChange={(e) => setFormData({ ...formData, defaultVideoQuality: e.target.value })}
              className="w-full rounded-lg border border-neutral-800 bg-neutral-900 px-3 py-1.5 text-xs text-neutral-200 focus:outline-none"
            >
              <option value="highest">Always Best Available (4K / 2160p)</option>
              <option value="1080p">1080p Full HD (Balanced)</option>
              <option value="720p">720p HD (Space Saver)</option>
            </select>
            <p className="text-[11px] text-neutral-500">
              Applied automatically when queuing new batch links.
            </p>
          </div>
        </div>

        {/* Toggles */}
        <div className="space-y-3 pt-2">
          <label className="flex items-center justify-between rounded-xl border border-neutral-800 bg-neutral-950/60 p-3 cursor-pointer">
            <div>
              <p className="text-xs font-semibold text-neutral-200">
                Keep & Embed Video Thumbnails
              </p>
              <p className="text-[11px] text-neutral-400">
                Embeds highest-resolution YouTube cover artwork directly into MP4/MP3 tags for visual library identification.
              </p>
            </div>
            <input
              type="checkbox"
              checked={formData.embedThumbnails}
              onChange={(e) => setFormData({ ...formData, embedThumbnails: e.target.checked })}
              className="h-4 w-4 rounded text-rose-600 focus:ring-rose-500"
            />
          </label>

          <label className="flex items-center justify-between rounded-xl border border-neutral-800 bg-neutral-950/60 p-3 cursor-pointer">
            <div>
              <p className="text-xs font-semibold text-neutral-200">
                Auto-Embed Subtitles (.srt / .vtt)
              </p>
              <p className="text-[11px] text-neutral-400">
                Automatically fetches creator and auto-generated captions in video container.
              </p>
            </div>
            <input
              type="checkbox"
              checked={formData.embedSubtitles}
              onChange={(e) => setFormData({ ...formData, embedSubtitles: e.target.checked })}
              className="h-4 w-4 rounded text-rose-600 focus:ring-rose-500"
            />
          </label>
        </div>
      </div>
    </div>
  );
};
