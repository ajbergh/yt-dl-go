import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import {
  Activity, ArrowDownAZ, Clock3, Copy, FileAudio,
  FileText, Film, FolderOpen, Grid2X2, HardDrive, Heart, List, Music2, Plus, Pencil, Layers,
  Play, RefreshCw, Search, SlidersHorizontal, Trash2, Users, X,
} from "lucide-react";
import type { DownloadFile, DownloadJob, ServiceConnection } from "../lib/downloader";
import { userFacingMessage } from "../lib/messages";
import { confirmAction } from "../lib/desktop";
import { addCollection, collectionNameIsAvailable, removeCollection, renameCollection, toggleCollectionItem, type MediaCollection } from "../lib/library-collections";
import { useLibraryOrganization } from "../hooks/use-library-organization";
import {
  LibraryThumbnail, button, dateLabel, durationLabel, field, formatBytes,
  statusClass, statusLabels, type LibraryFilter,
} from "../components/downloader/view-model";

type LibraryStats = {
  files: number;
  logicalBytes: number;
  managedBytes: number;
  publishedBytes: number;
};
type LibraryJobAction = "retry" | "delete-all" | "remove-library-item" | "delete-library-file";
type LibraryPageProps = {
  libraryJobs: DownloadJob[];
  visibleLibraryJobs: DownloadJob[];
  libraryTotalJobs: number;
  librarySearch: string;
  setLibrarySearch: (value: string) => void;
  libraryFilter: LibraryFilter;
  setLibraryFilter: (value: LibraryFilter) => void;
  libraryCategory: string;
  setLibraryCategory: (value: string) => void;
  libraryChannel: string;
  setLibraryChannel: (value: string) => void;
  libraryCategories: [string, number][];
  libraryChannels: [string, number][];
  libraryLayout: "grid" | "list";
  setLibraryLayout: (value: "grid" | "list") => void;
  libraryStats: LibraryStats;
  hasMoreLibrary: boolean;
  loadingLibraryMore: boolean;
  loadMoreLibrary: () => void;
  connection: ServiceConnection;
  busyAction: string;
  previewFile: (job: DownloadJob, file: DownloadFile) => void | Promise<void>;
  filesystemAction: (job: DownloadJob, fileId: string, action: "copy-path" | "reveal" | "open-folder") => void | Promise<void>;
  jobAction: (job: DownloadJob, action: LibraryJobAction, fileId?: string) => void | Promise<void>;
};

type MediaItem = {
  key: string;
  job: DownloadJob;
  file: DownloadFile;
  title: string;
  channel: string;
  mediaType: "audio" | "video";
  available: boolean;
};
type SortMode = "recent" | "oldest" | "title" | "size" | "duration";
type LibrarySection = "all" | "favorites" | "recent" | "playlists";
const sortStorageKey = "yt-dl-go:library-sort-v2";
const recentCutoff = Date.now() - 30 * 86400000;

function storedSort(): SortMode {
  try {
    const value = window.localStorage.getItem(sortStorageKey);
    return value === "oldest" || value === "title" || value === "size" || value === "duration" ? value : "recent";
  } catch { return "recent"; }
}
function mediaItems(jobs: DownloadJob[]): MediaItem[] {
  return jobs.flatMap(job => job.files.map(file => ({
    key: job.id + ":" + file.id,
    job,
    file,
    title: file.title || file.outputName || file.name,
    channel: file.author?.trim() || "Unknown channel",
    mediaType: file.mediaType || job.mediaType || "video",
    available: file.publishedAvailable !== false,
  })));
}
function mediaFormat(file: DownloadFile): string {
  const fileName = file.outputName || file.name;
  const extension = fileName.split(".").pop();
  return extension && extension !== fileName ? extension.toUpperCase() : (file.mimeType || "Media");
}
function sortMedia(items: MediaItem[], sort: SortMode): MediaItem[] {
  return [...items].sort((a, b) => {
    if (sort === "title") return a.title.localeCompare(b.title);
    if (sort === "size") return b.file.size - a.file.size;
    if (sort === "duration") return (b.file.durationSeconds || 0) - (a.file.durationSeconds || 0);
    const dateComparison = b.job.createdAt.localeCompare(a.job.createdAt);
    return sort === "oldest" ? -dateComparison : dateComparison;
  });
}
function actionDisabled(item: MediaItem, busyAction: string, action: string): boolean {
  return !item.available || busyAction === item.job.id + ":" + item.file.id + ":" + action;
}

export function LibraryPage({
  libraryJobs, visibleLibraryJobs, libraryTotalJobs, librarySearch, setLibrarySearch,
  libraryFilter, setLibraryFilter, libraryCategory, setLibraryCategory,
  libraryChannel, setLibraryChannel, libraryCategories, libraryChannels,
  libraryLayout, setLibraryLayout, libraryStats, hasMoreLibrary, loadingLibraryMore,
  loadMoreLibrary, connection, busyAction, previewFile, filesystemAction, jobAction,
}: LibraryPageProps) {
  const [section, setSection] = useState<LibrarySection>("all");
  const { favorites, collections, ready: organizationReady, error: organizationError, toggleFavorite, setCollections, reload: reloadOrganization } = useLibraryOrganization(connection);
  const [sort, setSort] = useState<SortMode>(storedSort);
  const [selectedKey, setSelectedKey] = useState("");
  const [inspectorOpen, setInspectorOpen] = useState(true);
  const [showFilters, setShowFilters] = useState(false);
  const [activeCollectionID, setActiveCollectionID] = useState("");
  const [showCollectionForm, setShowCollectionForm] = useState(false);
  const [newCollectionName, setNewCollectionName] = useState("");
  const [renameCollectionID, setRenameCollectionID] = useState("");
  const [renamedCollectionName, setRenamedCollectionName] = useState("");
  const [collectionError, setCollectionError] = useState("");
  const [showChannelList, setShowChannelList] = useState(false);
  const [selectedPlaylistID, setSelectedPlaylistID] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    try { window.localStorage.setItem(sortStorageKey, sort); }
    catch { /* Sorting remains available within this session. */ }
  }, [sort]);
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f") {
        event.preventDefault();
        searchRef.current?.focus();
      }
      if (event.key === "Escape" && document.activeElement === searchRef.current) {
        searchRef.current?.blur();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const loaded = useMemo(() => mediaItems(visibleLibraryJobs), [visibleLibraryJobs]);
  const allLoaded = useMemo(() => mediaItems(libraryJobs), [libraryJobs]);
  const counts = useMemo(() => ({
    video: allLoaded.filter(item => item.mediaType === "video").length,
    audio: allLoaded.filter(item => item.mediaType === "audio").length,
    favorites: allLoaded.filter(item => favorites.includes(item.key)).length,
  }), [allLoaded, favorites]);
  const activeCollection = collections.find(collection => collection.id === activeCollectionID);
  const playlistJobs = libraryJobs.filter(job => job.kind === "playlist" && job.files.length > 0);
  const filtered = useMemo(() => {
    const collection = collections.find(entry => entry.id === activeCollectionID);
    const visible = loaded.filter(item => {
      if (collection && !collection.itemKeys.includes(item.key)) return false;
      if (section === "playlists" && (item.job.kind !== "playlist" || (selectedPlaylistID && item.job.id !== selectedPlaylistID))) return false;
      if (section === "favorites" && !favorites.includes(item.key)) return false;
      if (section === "recent") {
        const timestamp = new Date(item.job.createdAt).getTime();
        if (!Number.isFinite(timestamp) || timestamp < recentCutoff) return false;
      }
      return true;
    });
    return sortMedia(visible, sort);
  }, [loaded, section, favorites, sort, collections, activeCollectionID, selectedPlaylistID]);
  const selected = filtered.find(item => item.key === selectedKey) || filtered[0];
  const filtersActive = Boolean(librarySearch || libraryFilter !== "all" || libraryCategory !== "all" || libraryChannel !== "all" || section !== "all" || activeCollectionID);

  function resetFilters() {
    setLibrarySearch("");
    setLibraryFilter("all");
    setLibraryCategory("all");
    setLibraryChannel("all");
    setSection("all");
    setActiveCollectionID("");
    setSelectedPlaylistID("");
  }
  function navigate(type: LibraryFilter, nextSection: LibrarySection = "all") {
    setLibraryFilter(type);
    setLibraryChannel("all");
    setSection(nextSection);
    setActiveCollectionID("");
    setSelectedPlaylistID("");
    setSelectedKey("");
  }
  function browseCollection(id: string) {
    setActiveCollectionID(id);
    setSelectedPlaylistID("");
    setLibraryFilter("all");
    setLibraryCategory("all");
    setLibraryChannel("all");
    setSection("all");
    setSelectedKey("");
  }
  function browseChannel(channel: string) {
    setActiveCollectionID("");
    setSelectedPlaylistID("");
    setLibraryFilter("all");
    setLibraryCategory("all");
    setSection("all");
    setLibraryChannel(channel);
    setSelectedKey("");
  }
  function browsePlaylist(id: string) {
    setActiveCollectionID("");
    setSelectedPlaylistID(id);
    setLibraryFilter("all");
    setLibraryCategory("all");
    setLibraryChannel("all");
    setSection("playlists");
    setSelectedKey("");
  }
  function createCollection(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!collectionNameIsAvailable(collections, newCollectionName)) {
      setCollectionError("Choose a unique collection name (up to 80 characters).");
      return;
    }
    const id = "c_" + Date.now().toString(36) + "_" + Math.random().toString(36).slice(2, 10);
    setCollections(previous => addCollection(previous, id, newCollectionName));
    browseCollection(id);
    setNewCollectionName("");
    setCollectionError("");
    setShowCollectionForm(false);
  }
  function saveCollectionRename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!collectionNameIsAvailable(collections, renamedCollectionName, renameCollectionID)) {
      setCollectionError("Choose a unique collection name (up to 80 characters).");
      return;
    }
    setCollections(previous => renameCollection(previous, renameCollectionID, renamedCollectionName));
    setRenameCollectionID("");
    setRenamedCollectionName("");
    setCollectionError("");
  }
  async function deleteCollection(collection: MediaCollection) {
    const confirmed = await confirmAction('Delete collection "' + collection.name + '"? The downloaded files are not affected.');
    if (!confirmed) return;
    setCollections(previous => removeCollection(previous, collection.id));
    if (activeCollectionID === collection.id) browseCollection("");
    setRenameCollectionID("");
    setCollectionError("");
  }
  function openItem(item: MediaItem) {
    if (item.available) void previewFile(item.job, item.file);
  }
  function fileAction(item: MediaItem, action: "copy-path" | "reveal" | "open-folder") {
    void filesystemAction(item.job, item.file.id, action);
  }

  const sidebarButton = (label: string, Icon: typeof Film, count: number | null, active: boolean, onClick: () => void) => (
    <button type="button" onClick={onClick} aria-current={active ? "page" : undefined}
      className={"flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors " +
        (active ? "bg-rose-600/20 font-semibold text-rose-100 ring-1 ring-rose-500/30" : "text-neutral-400 hover:bg-neutral-800 hover:text-white")}>
      <Icon className={"size-4 shrink-0 " + (active ? "text-rose-400" : "")} aria-hidden="true" />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {count !== null && <span className="rounded-full bg-neutral-800 px-1.5 py-0.5 text-[10px] text-neutral-300">{count}</span>}
    </button>
  );

  const artwork = (item: MediaItem, variant: "card" | "inspector" | "list") => (
    <div className={"relative overflow-hidden bg-neutral-950 " + (variant === "list" ? "w-24 shrink-0 rounded-lg" : "w-full rounded-xl")}>
      <LibraryThumbnail connection={connection} jobId={item.job.id} file={item.file}
        audio={item.mediaType === "audio"} className="aspect-video w-full rounded-lg bg-neutral-900 object-cover" />
      {variant !== "list" && <>
        <span className="absolute bottom-2 right-2 rounded-md bg-black/85 px-2 py-0.5 text-[11px] font-semibold tabular-nums text-white">
          {item.file.durationSeconds ? durationLabel(item.file.durationSeconds) : item.mediaType === "audio" ? "Audio" : "Video"}
        </span>
        {item.mediaType === "audio" && <span className="absolute left-2 top-2 rounded-md bg-black/80 p-1.5 text-rose-300"><Music2 className="size-4" aria-hidden="true" /></span>}
      </>}
    </div>
  );

  return (
    <section aria-labelledby="library-heading" className="min-w-0">
      <div className={"grid items-start gap-4 lg:grid-cols-[188px_minmax(0,1fr)] " +
        (inspectorOpen && selected ? "xl:grid-cols-[188px_minmax(0,1fr)_310px]" : "")}>
        <aside aria-label="Library navigation" className="hidden rounded-2xl border border-neutral-800/80 bg-[#111216] p-3 lg:block">
          <p className="px-3 pb-3 pt-2 text-[10px] font-bold uppercase tracking-[.2em] text-neutral-500">Browse</p>
          <nav className="space-y-1" aria-label="Media sections">
            {sidebarButton("All media", Grid2X2, libraryStats.files, libraryFilter === "all" && section === "all", () => navigate("all"))}
            {sidebarButton("Videos", Film, counts.video, libraryFilter === "video" && section === "all", () => navigate("video"))}
            {sidebarButton("Audio", Music2, counts.audio, libraryFilter === "audio" && section === "all", () => navigate("audio"))}
            {sidebarButton("Favorites", Heart, counts.favorites, section === "favorites", () => navigate("all", "favorites"))}
            {sidebarButton("Recently added", Clock3, null, section === "recent", () => navigate("all", "recent"))}
            {sidebarButton("Playlists", Layers, playlistJobs.length, section === "playlists", () => browsePlaylist(""))}
            {sidebarButton("Channels", Users, libraryChannels.length, libraryChannel !== "all", () => setShowChannelList(previous => !previous))}
            {showChannelList && <div aria-label="Channel shortcuts" className="max-h-48 space-y-1 overflow-y-auto pl-3">
              {libraryChannels.slice(0, 12).map(([channel, count]) =>
                <button type="button" key={channel} aria-pressed={libraryChannel === channel} onClick={() => browseChannel(channel)}
                  className={"flex w-full items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left text-xs hover:bg-neutral-800 " + (libraryChannel === channel ? "text-rose-300" : "text-neutral-400")}>
                  <span className="truncate">{channel}</span><span>{count}</span>
                </button>)}
              <button type="button" className="px-2 py-1 text-xs text-neutral-400 hover:text-white" onClick={() => browseChannel("all")}>All channels</button>
            </div>}
          </nav>
          <div className="mt-5 border-t border-neutral-800 pt-4">
            <div className="mb-2 flex items-center justify-between px-3">
              <p className="text-[10px] font-bold uppercase tracking-[.16em] text-neutral-500">Collections</p>
              <button type="button" title="New collection" aria-label="New collection" onClick={() => { setShowCollectionForm(previous => !previous); setCollectionError(""); }}
                className="rounded-md p-1.5 text-rose-400 hover:bg-neutral-800"><Plus className="size-4" /></button>
            </div>
            {collections.map(collection => <div key={collection.id} className="group flex items-center gap-0.5">
              <button type="button" aria-pressed={activeCollectionID === collection.id} onClick={() => browseCollection(collection.id)}
                title={collection.name} className={"min-w-0 flex-1 truncate rounded-lg px-3 py-2 text-left text-xs " + (activeCollectionID === collection.id ? "bg-rose-600/20 text-rose-200" : "text-neutral-400 hover:bg-neutral-800")}>
                {collection.name}
              </button>
              <button type="button" aria-label={"Rename collection " + collection.name} title="Rename collection"
                onClick={() => { setRenameCollectionID(collection.id); setRenamedCollectionName(collection.name); setCollectionError(""); }}
                className="rounded-md p-1 text-neutral-500 hover:bg-neutral-800 hover:text-white"><Pencil className="size-3.5" /></button>
              <button type="button" aria-label={"Delete collection " + collection.name} title="Delete collection"
                onClick={() => void deleteCollection(collection)}
                className="rounded-md p-1 text-neutral-500 hover:bg-red-900/40 hover:text-rose-300"><Trash2 className="size-3.5" /></button>
            </div>)}
            {collections.length === 0 && <p className="px-3 text-xs text-neutral-500">Create a personal collection.</p>}
            {showCollectionForm && <form onSubmit={createCollection} className="mt-2 space-y-2 px-2">
              <input aria-label="Collection name" autoFocus maxLength={80} value={newCollectionName} onChange={event => setNewCollectionName(event.target.value)}
                placeholder="Collection name" className={field + " px-2 py-2 text-xs"} />
              <button type="submit" className={button + " w-full"}><Plus className="size-3.5" />Create collection</button>
            </form>}
            {renameCollectionID && <form onSubmit={saveCollectionRename} className="mt-2 space-y-2 px-2">
              <input aria-label="Rename collection" maxLength={80} value={renamedCollectionName} onChange={event => setRenamedCollectionName(event.target.value)}
                className={field + " px-2 py-2 text-xs"} />
              <button type="submit" className={button + " w-full"}>Save collection name</button>
            </form>}
            {collectionError && <p role="alert" className="mt-2 px-2 text-xs text-rose-300">{collectionError}</p>}
            <p className="mt-3 px-3 text-[10px] leading-4 text-neutral-500">Personal collections are stored on this device. Files are never moved.</p>
          </div>
          <div className="mt-6 border-t border-neutral-800 px-3 pt-5">
            <p className="flex items-center gap-2 text-xs font-semibold text-neutral-200"><HardDrive className="size-4 text-rose-400" />Library storage</p>
            <p className="mt-2 text-lg font-bold tabular-nums">{formatBytes(libraryStats.publishedBytes)}</p>
            <p className="mt-1 text-xs text-neutral-500">Published local media · no quota assumed</p>
          </div>
        </aside>

        <div className="min-w-0 space-y-4">
          {organizationError && <div role="alert" className="flex items-center justify-between gap-3 rounded-xl border border-amber-700/60 bg-amber-950/40 px-4 py-3 text-xs text-amber-200">
            <span>Could not save your media organization: {organizationError}. Changes are paused to prevent overwriting another window.</span>
            <button type="button" className={button} onClick={reloadOrganization}>Reload organization</button>
          </div>}
          {!organizationReady && !organizationError && <p role="status" className="text-xs text-neutral-400">Loading saved collections and favorites…</p>}
          <div className="flex flex-wrap gap-2 rounded-xl border border-neutral-800 bg-neutral-900 p-2 lg:hidden">
            <label className="min-w-0 flex-1">
              <span className="sr-only">Browse media section</span>
              <select aria-label="Browse media section" className={field + " py-2 text-xs"}
                value={activeCollectionID ? "collection:" + activeCollectionID : section === "playlists" ? "playlists" : section === "favorites" ? "favorites" : section === "recent" ? "recent" : libraryFilter}
                onChange={event => {
                  const value = event.target.value;
                  if (value.startsWith("collection:")) browseCollection(value.slice("collection:".length));
                  else if (value === "playlists") browsePlaylist("");
                  else if (value === "favorites" || value === "recent") navigate("all", value);
                  else navigate(value as LibraryFilter);
                }}>
                <option value="all">All media</option><option value="video">Videos</option><option value="audio">Audio</option>
                <option value="favorites">Favorites</option><option value="recent">Recently added</option>
                <option value="playlists">Playlists</option>
                {collections.map(collection => <option key={collection.id} value={"collection:" + collection.id}>{collection.name}</option>)}
              </select>
            </label>
            <button type="button" className={button} onClick={() => { setShowCollectionForm(previous => !previous); setCollectionError(""); }}>
              <Plus className="size-4" />Collection
            </button>
            {showCollectionForm && <form onSubmit={createCollection} className="flex w-full items-center gap-2">
              <input aria-label="Collection name on mobile" autoFocus maxLength={80} value={newCollectionName}
                onChange={event => setNewCollectionName(event.target.value)} placeholder="New collection name" className={field + " min-w-0 py-2"} />
              <button type="submit" className={button}>Create</button>
            </form>}
            {collectionError && <p role="alert" className="w-full text-xs text-rose-300">{collectionError}</p>}
          </div>
          <div className="overflow-hidden rounded-2xl border border-neutral-800/80 bg-[radial-gradient(ellipse_at_top_right,rgba(224,28,79,.22),transparent_65%),linear-gradient(120deg,#1c1922,#101114_70%)] p-5 sm:p-6">
            <p className="text-[11px] font-bold uppercase tracking-[.22em] text-rose-300">Your library</p>
            <h2 id="library-heading" className="mt-1 text-2xl font-bold tracking-tight sm:text-3xl">Media Library</h2>
            <p className="mt-2 text-sm text-neutral-400">Browse, preview, and manage your downloaded videos and audio.</p>
            {activeCollection && <p className="mt-2 text-xs text-rose-300">Collection: {activeCollection.name}</p>}
            {libraryChannel !== "all" && <p className="mt-2 text-xs text-rose-300">Channel: {libraryChannel}</p>}
            {section === "playlists" && <p className="mt-2 text-xs text-rose-300">Source playlist downloads · loaded pages</p>}
            <div className="mt-5 grid grid-cols-2 gap-3">
              <div className="rounded-xl border border-white/10 bg-black/20 p-3">
                <p className="flex items-center gap-2 text-xs text-neutral-400"><Film className="size-4 text-rose-400" /> Files</p>
                <p className="mt-1 text-xl font-bold tabular-nums">{libraryStats.files}</p>
              </div>
              <div className="rounded-xl border border-white/10 bg-black/20 p-3">
                <p className="flex items-center gap-2 text-xs text-neutral-400"><HardDrive className="size-4 text-rose-400" /> Library size</p>
                <p className="mt-1 text-xl font-bold tabular-nums">{formatBytes(libraryStats.publishedBytes)}</p>
              </div>
            </div>
          </div>

          <div className="space-y-3 rounded-2xl border border-neutral-800/80 bg-[#141519] p-3">
            <div className="flex flex-wrap items-center gap-2">
              <label className="relative min-w-[180px] flex-1">
                <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-neutral-500" aria-hidden="true" />
                <input ref={searchRef} aria-label="Search library" className={field + " py-2.5 pl-10"}
                  value={librarySearch} onChange={event => setLibrarySearch(event.target.value)}
                  placeholder="Search titles, channels, or filenames" />
              </label>
              <button type="button" onClick={() => setShowFilters(value => !value)} aria-expanded={showFilters}
                className={button + (showFilters ? " border-rose-500/60" : "")}>
                <SlidersHorizontal className="size-4" aria-hidden="true" /> Filters
              </button>
              <div role="group" aria-label="Library view" className="flex rounded-lg border border-neutral-700 bg-neutral-950 p-1">
                <button type="button" aria-label="Grid" aria-pressed={libraryLayout === "grid"} onClick={() => setLibraryLayout("grid")}
                  className={"flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs " + (libraryLayout === "grid" ? "bg-rose-700 text-white" : "text-neutral-400")}>
                  <Grid2X2 className="size-4" /> <span className="hidden sm:inline">Grid</span>
                </button>
                <button type="button" aria-label="List" aria-pressed={libraryLayout === "list"} onClick={() => setLibraryLayout("list")}
                  className={"flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs " + (libraryLayout === "list" ? "bg-rose-700 text-white" : "text-neutral-400")}>
                  <List className="size-4" /> <span className="hidden sm:inline">List</span>
                </button>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-1.5">
              {(["all", "video", "audio"] as const).map(value => (
                <button type="button" key={value} onClick={() => navigate(value)} aria-pressed={libraryFilter === value && section === "all"}
                  className={"rounded-full border px-3 py-1.5 text-xs font-semibold capitalize transition-colors " +
                    (libraryFilter === value && section === "all" ? "border-rose-600 bg-rose-600/20 text-rose-100" : "border-neutral-700 text-neutral-400 hover:text-white")}>
                  {value === "all" ? "All media" : value}
                </button>
              ))}
              <button type="button" aria-pressed={section === "playlists"} onClick={() => browsePlaylist("")}
                className={"flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-semibold " +
                  (section === "playlists" ? "border-rose-600 bg-rose-600/20 text-rose-100" : "border-neutral-700 text-neutral-400")}>
                <Layers className="size-3" /> Playlists
              </button>
              <button type="button" aria-pressed={section === "favorites"} onClick={() => navigate("all", "favorites")}
                className={"flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-semibold " +
                  (section === "favorites" ? "border-rose-600 bg-rose-600/20 text-rose-100" : "border-neutral-700 text-neutral-400")}>
                <Heart className="size-3" /> Favorites
              </button>
              <label className="ml-auto flex items-center gap-2 text-xs text-neutral-400">
                <ArrowDownAZ className="size-4" aria-hidden="true" />
                <span className="sr-only">Sort media</span>
                <select aria-label="Sort library" value={sort} onChange={event => setSort(event.target.value as SortMode)}
                  className="min-h-8 rounded-lg border border-neutral-700 bg-neutral-950 px-2 text-xs text-neutral-200">
                  <option value="recent">Recently added</option><option value="oldest">Oldest first</option>
                  <option value="title">Title A–Z</option><option value="size">Largest first</option>
                  <option value="duration">Longest first</option>
                </select>
              </label>
            </div>
            <div className={"grid gap-2 border-t border-neutral-800 pt-3 sm:grid-cols-2 " + (showFilters ? "" : "hidden")}>
              <select aria-label="Filter library by category" value={libraryCategory} onChange={event => setLibraryCategory(event.target.value)} className={field + " py-2 text-xs"}>
                <option value="all">All categories ({libraryStats.files})</option>
                {libraryCategories.map(([category, count]) => <option key={category} value={category}>{category} ({count})</option>)}
              </select>
              <select aria-label="Filter library by channel" value={libraryChannel} onChange={event => setLibraryChannel(event.target.value)} className={field + " py-2 text-xs"}>
                <option value="all">All channels</option>
                {libraryChannels.map(([channel, count]) => <option key={channel} value={channel}>{channel} ({count})</option>)}
              </select>
            </div>
            {section === "playlists" && playlistJobs.length > 0 && <div aria-label="Source playlists" className="flex flex-wrap gap-2 border-t border-neutral-800 pt-3">
              <button type="button" aria-pressed={!selectedPlaylistID} onClick={() => browsePlaylist("")}
                className={button}>All playlists</button>
              {playlistJobs.map(job => <button type="button" key={job.id} aria-pressed={selectedPlaylistID === job.id}
                className={button + (selectedPlaylistID === job.id ? " border-rose-500 text-rose-200" : "")}
                onClick={() => browsePlaylist(job.id)}>{job.title} ({job.files.length})</button>)}
            </div>}
            <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-neutral-500">
              <p>{visibleLibraryJobs.length} of {libraryTotalJobs} jobs visible (matching filters) · {filtered.length} loaded media items</p>
              {filtersActive && <button type="button" onClick={resetFilters} className="flex items-center gap-1 text-rose-300 hover:text-rose-200"><X className="size-3" />Reset filters</button>}
            </div>
          </div>

          {libraryStats.files === 0 && !filtersActive ? (
            <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 px-6 py-16 text-center">
              <Film className="mx-auto size-9 text-rose-400" aria-hidden="true" />
              <h3 className="mt-3 font-semibold">Your media library is empty</h3>
              <p className="mt-1 text-sm text-neutral-400">Finished downloads appear here automatically.</p>
            </div>
          ) : filtered.length === 0 ? (
            <div className="rounded-2xl border border-neutral-800 bg-neutral-900/60 px-6 py-14 text-center">
              <Search className="mx-auto size-7 text-neutral-500" aria-hidden="true" />
              <p className="mt-3 text-sm text-neutral-300">No media match these filters.</p>
              <button type="button" onClick={resetFilters} className={button + " mt-4"}>Clear filters</button>
            </div>
          ) : (
            <div aria-label="Saved media" className={libraryLayout === "grid"
              ? "grid grid-cols-[repeat(auto-fill,minmax(min(100%,205px),1fr))] items-stretch gap-4"
              : "flex flex-col gap-2"}>
              {filtered.map(item => {
                const active = selected?.key === item.key;
                return <article key={item.key} role="button" tabIndex={0} aria-pressed={active}
                  aria-label={"Select " + item.title}
                  onClick={() => { setSelectedKey(item.key); setInspectorOpen(true); }}
                  onDoubleClick={() => openItem(item)}
                  onKeyDown={event => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      setSelectedKey(item.key);
                      setInspectorOpen(true);
                      if (event.key === "Enter") openItem(item);
                    }
                  }}
                  className={"group relative min-w-0 cursor-pointer rounded-2xl border bg-[#191a20] p-2.5 transition-colors focus-visible:outline-2 focus-visible:outline-rose-400 " +
                    (active ? "border-rose-500 shadow-[0_0_0_1px_rgba(244,63,94,.25)]" : "border-neutral-800 hover:border-neutral-600") +
                    (libraryLayout === "list" ? " flex items-center gap-3" : "")}>
                  {artwork(item, libraryLayout === "list" ? "list" : "card")}
                  <div className={"min-w-0 " + (libraryLayout === "list" ? "flex-1" : "px-1 pb-1 pt-3")}>
                    <h3 className={"font-semibold leading-snug text-white " + (libraryLayout === "list" ? "truncate text-sm" : "line-clamp-2 min-h-10 text-[13px]")}
                      title={item.title}>{item.title}</h3>
                    <p className="mt-1 truncate text-xs text-neutral-400" title={item.channel}>{item.channel}</p>
                    <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-neutral-400">
                      {item.mediaType === "audio" ? <FileAudio className="size-3.5 text-rose-400" aria-label="Audio" /> : <Film className="size-3.5 text-rose-400" aria-label="Video" />}
                      {item.file.height ? <span>{item.file.height}p</span> : null}
                      <span>{formatBytes(item.file.size)}</span>
                      <span>{mediaFormat(item.file)}</span>
                    </div>
                    {!item.available && <p className="mt-1 text-xs text-amber-300">File unavailable</p>}
                  </div>
                  <button type="button" title={favorites.includes(item.key) ? "Remove favorite" : "Add favorite"}
                    aria-label={(favorites.includes(item.key) ? "Remove favorite: " : "Add favorite: ") + item.title}
                    aria-pressed={favorites.includes(item.key)}
                    onClick={event => { event.stopPropagation(); toggleFavorite(item.key); }}
                    className={(libraryLayout === "grid" ? "absolute" : "") + " rounded-full bg-black/75 p-2 text-neutral-300 hover:bg-neutral-700 hover:text-rose-300 " +
                      (libraryLayout === "grid" ? "right-4 top-4 " : "") + (favorites.includes(item.key) ? "text-rose-400" : "")}>
                    <Heart className="size-4" fill={favorites.includes(item.key) ? "currentColor" : "none"} />
                  </button>
                </article>;
              })}
            </div>
          )}
          {hasMoreLibrary && <div className="flex justify-center py-3">
            <button type="button" className={button} onClick={loadMoreLibrary} disabled={loadingLibraryMore}>
              {loadingLibraryMore ? "Loading…" : "Load more (" + Math.max(0, libraryTotalJobs - libraryJobs.length) + " jobs remaining)"}
            </button>
          </div>}
        </div>

        {inspectorOpen && selected && <aside aria-label="Selected media details" className="min-w-0 rounded-2xl border border-neutral-800 bg-[#141519] p-4 lg:col-start-2 xl:col-start-3 xl:row-start-1">
          <div className="mb-3 flex items-center justify-between">
            <p className="text-xs font-bold uppercase tracking-widest text-neutral-400">Media details</p>
            <button type="button" onClick={() => setInspectorOpen(false)} aria-label="Close media details" className="rounded-lg p-2 text-neutral-400 hover:bg-neutral-800 hover:text-white"><X className="size-4" /></button>
          </div>
          {artwork(selected, "inspector")}
          <h3 className="mt-4 break-words text-lg font-bold leading-snug text-white">{selected.title}</h3>
          <p className="mt-1 flex items-center gap-2 text-sm text-neutral-400"><Users className="size-4 text-rose-400" />{selected.channel}</p>
          <p className="mt-1 truncate text-xs text-neutral-500" title={selected.job.title}>From {selected.job.title}</p>
          <div className="mt-4 flex flex-wrap gap-2 text-xs">
            <span className={"rounded-full border px-2.5 py-1 " + statusClass(selected.job.status)}>{statusLabels[selected.job.status]}</span>
            <span className="rounded-full border border-neutral-700 px-2.5 py-1 text-neutral-300">{selected.mediaType}</span>
            {selected.file.height ? <span className="rounded-full border border-neutral-700 px-2.5 py-1 text-neutral-300">{selected.file.height}p</span> : null}
          </div>
          <div className="mt-5 grid grid-cols-2 gap-3 border-y border-neutral-800 py-4 text-xs">
            <div><span className="text-neutral-500">File size</span><p className="mt-1 font-medium">{formatBytes(selected.file.size)}</p></div>
            <div><span className="text-neutral-500">Duration</span><p className="mt-1 font-medium">{selected.file.durationSeconds ? durationLabel(selected.file.durationSeconds) : "Unknown"}</p></div>
            <div><span className="text-neutral-500">Format</span><p className="mt-1 font-medium">{mediaFormat(selected.file)}</p></div>
            <div><span className="text-neutral-500">Added</span><p className="mt-1 font-medium">{dateLabel(selected.job.createdAt)}</p></div>
          </div>
          <div className="mt-4 text-xs">
            <p className="flex items-center gap-2 font-semibold"><FileText className="size-4 text-neutral-400" />File details</p>
            <p className="mt-3 text-neutral-500">Filename</p>
            <p className="mt-1 break-all text-neutral-200">{selected.file.outputName || selected.file.name}</p>
            <p className="mt-3 text-neutral-500">Relative location</p>
            <p className="mt-1 break-all text-neutral-200">{selected.file.outputRelativePath || "Not available"}</p>
            {selected.file.subtitleError && <p className="mt-3 text-amber-300">Caption warning: {userFacingMessage(selected.file.subtitleError)}</p>}
            {!selected.available && <p className="mt-3 text-amber-300">The published file is unavailable. Preview and filesystem actions are disabled.</p>}
          </div>
          <div className="mt-4 border-t border-neutral-800 pt-4">
            <p className="flex items-center gap-2 text-xs font-semibold"><Layers className="size-4 text-rose-400" />Collections</p>
            {collections.length === 0
              ? <p className="mt-2 text-xs text-neutral-500">Create a collection from the Library sidebar to organize downloaded media.</p>
              : <div className="mt-2 max-h-36 space-y-1 overflow-y-auto">
                {collections.map(collection => <label key={collection.id} className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-2 text-xs text-neutral-300 hover:bg-neutral-800">
                  <input type="checkbox" aria-label={"Add " + selected.title + " to " + collection.name} checked={collection.itemKeys.includes(selected.key)}
                    onChange={event => setCollections(previous => toggleCollectionItem(previous, collection.id, selected.key, event.target.checked))}
                    className="accent-rose-500" />
                  <span className="min-w-0 flex-1 truncate">{collection.name}</span>
                </label>)}
              </div>}
          </div>
          <div className="mt-5 space-y-2 border-t border-neutral-800 pt-4">
            <button type="button" className="flex min-h-10 w-full items-center justify-center gap-2 rounded-xl bg-rose-600 px-4 text-sm font-bold text-white hover:bg-rose-500 disabled:opacity-40"
              disabled={actionDisabled(selected, busyAction, "preview")} onClick={() => openItem(selected)}>
              <Play className="size-4" />Preview
            </button>
            <div className="grid grid-cols-2 gap-2">
              <button type="button" className={button} disabled={actionDisabled(selected, busyAction, "reveal") || !selected.file.outputRelativePath}
                onClick={() => fileAction(selected, "reveal")}><FolderOpen className="size-3.5" />Reveal</button>
              <button type="button" className={button} disabled={actionDisabled(selected, busyAction, "copy-path") || !selected.file.outputRelativePath}
                onClick={() => fileAction(selected, "copy-path")}><Copy className="size-3.5" />Copy path</button>
              <button type="button" className={button} disabled={actionDisabled(selected, busyAction, "open-folder") || !selected.file.outputRelativePath}
                onClick={() => fileAction(selected, "open-folder")}><FolderOpen className="size-3.5" />Open folder</button>
              <button type="button" className={button} disabled={busyAction === selected.job.id}
                title="Remove only this file from the library index; keep the downloaded file"
                onClick={() => void jobAction(selected.job, "remove-library-item", selected.file.id)}><X className="size-3.5" />Forget item</button>
            </div>
            <button type="button" className={button + " w-full border-red-900/70 text-red-300 hover:border-red-700 hover:bg-red-950/40"}
              title="Permanently delete only the selected file"
              disabled={busyAction === selected.job.id || !selected.available || !selected.file.outputRelativePath}
              onClick={() => void jobAction(selected.job, "delete-library-file", selected.file.id)}>
              <Trash2 className="size-4" />Delete selected file
            </button>
            <button type="button" className={button + " w-full border-red-900/70 text-red-300 hover:border-red-700 hover:bg-red-950/40"}
              title="Permanently delete every file belonging to this download job"
              disabled={busyAction === selected.job.id} onClick={() => void jobAction(selected.job, "delete-all")}>
              <Trash2 className="size-4" />Delete entire download
            </button>
            {selected.job.files.length > 1 && <p className="text-xs text-amber-300">Deletes all {selected.job.files.length} files from this download job, not only the selected item.</p>}
            {(selected.job.status === "partial" || selected.job.status === "failed" || selected.job.status === "cancelled") &&
              <button type="button" className={button + " w-full"} disabled={busyAction === selected.job.id} onClick={() => void jobAction(selected.job, "retry")}>
                <RefreshCw className="size-4" />Retry job
              </button>}
          </div>
        </aside>}
      </div>
      {!inspectorOpen && selected && <button type="button" className={button + " fixed bottom-5 right-5 z-20 shadow-xl"} onClick={() => setInspectorOpen(true)}>
        <Activity className="size-4" />Show details
      </button>}
      <p className="sr-only">Keyboard shortcuts: Control or Command F to search. Select a card and press Enter to preview it.</p>
    </section>
  );
}
