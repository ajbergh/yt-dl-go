import type { Dispatch, FormEvent, SetStateAction } from "react";
import {
  Check, FileText, Folder, FolderTree, Gauge, HardDrive, LoaderCircle, Plus, ShieldCheck, Sparkles, X,
} from "lucide-react";
import type { AppSettings, Quality, ServiceConnection } from "../lib/downloader";
import {
  button, field, notificationAPI, panel, primaryButton, qualityLabels,
} from "../components/downloader/view-model";

type SettingsPageProps = {
  connection: ServiceConnection;
  serviceReady: boolean;
  serviceError: string;
  settings: AppSettings;
  savingSettings: boolean;
  settingsSaved: boolean;
  mp3Supported: boolean;
  newCategoryInput: string;
  setNewCategoryInput: Dispatch<SetStateAction<string>>;
  savePreferences: (event: FormEvent) => void | Promise<void>;
  selectDownloadFolder: () => void | Promise<void>;
  changeSetting: <K extends keyof AppSettings>(key: K, value: AppSettings[K]) => void;
  addCategory: () => void;
  removeCategory: (category: string) => void;
  toggleNotifications: () => void | Promise<void>;
};

export function SettingsPage({
  connection, serviceReady, serviceError, settings, savingSettings, settingsSaved, mp3Supported,
  newCategoryInput, setNewCategoryInput, savePreferences, selectDownloadFolder, changeSetting,
  addCategory, removeCategory, toggleNotifications,
}: SettingsPageProps) {
  return (
<div className="mx-auto max-w-4xl space-y-5">
          <div><p className="mb-1 text-[11px] font-bold uppercase tracking-[0.16em] text-neutral-500">Configuration</p><h2 className="text-2xl font-bold">Service & preferences</h2><p className="mt-1 text-xs text-neutral-400">The Go service is the source of truth for downloads and SQLite-backed preferences.</p></div>

          <section className={`${panel} p-5 sm:p-6`} aria-labelledby="service-heading">
            <div className="mb-4 flex items-start justify-between gap-3"><div><h3 id="service-heading" className="text-sm font-bold">Built-in Go download service</h3><p className="mt-1 max-w-2xl text-xs leading-relaxed text-neutral-400">This single-executable app connects automatically to its local Go backend at <span className="font-mono text-neutral-300">{connection.base}</span>. Downloads, history, and preferences use the same service and SQLite database.</p></div><span className={`rounded-full border px-2.5 py-1 text-[10px] font-semibold ${serviceReady ? "border-emerald-700/60 bg-emerald-950/40 text-emerald-300" : "border-amber-700/60 bg-amber-950/40 text-amber-200"}`}>{serviceReady ? "Connected" : serviceError ? "Reconnecting" : "Connecting"}</span></div>
            {serviceError && <p role="alert" className="mt-3 text-xs text-red-300">{serviceError}</p>}
            {!serviceReady && <p className="mt-2 text-[10px] text-neutral-500">The app retries the local service automatically while it starts.</p>}
          </section>

          <form onSubmit={savePreferences} className="space-y-5">
            <section className={`${panel} space-y-4 p-5 sm:p-6`}>
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-neutral-800 pb-4">
                <div><h3 className="text-sm font-bold">Preferences & output configuration</h3><p className="mt-1 text-xs text-neutral-400">Choose where finished files go, how they are named, and how folders are organized.</p></div>
                <button type="submit" className={primaryButton} disabled={!serviceReady || savingSettings}>{savingSettings && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}{settingsSaved ? <Check className="size-4" aria-hidden="true" /> : null}{savingSettings ? "Saving…" : settingsSaved ? "Saved" : "Save preferences"}</button>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-amber-700/40 bg-amber-950/30 text-amber-300"><Folder className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Download directory location</h4><p className="mt-0.5 text-[11px] text-neutral-400">Finished media is copied to this local or external folder.</p></div></div>
                <label className="block text-[11px] font-medium text-neutral-300" htmlFor="download-location">Absolute folder path</label>
                <div className="mt-1 flex gap-2"><input id="download-location" type="text" autoComplete="off" className={`${field} font-mono text-xs`} value={settings.downloadLocation} onChange={event => changeSetting("downloadLocation", event.target.value)} placeholder="C:\\Users\\you\\Downloads\\YouTube_Vault" /><button type="button" className={button} onClick={() => void selectDownloadFolder()} disabled={!serviceReady} title="Choose a folder with the operating system picker"><Folder className="size-3.5 text-amber-400" aria-hidden="true" />Browse</button></div>
                <p className="mt-2 text-[10px] text-neutral-500">Enter an absolute path. The app creates the folder when the first download finishes. Existing files stay in their original locations.</p>
                <div className="mt-3 flex flex-wrap items-center gap-1.5 text-[10px]">
                  <span className="mr-1 text-neutral-500">Quick paths:</span>
                  {["YouTube_Vault", "Media", "YouTube"].map(name => {
                    const path = settings.downloadLocation;
                    const lastSlash = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
                    const separator = path.includes("\\") ? "\\" : "/";
                    const parent = lastSlash >= 0 ? path.slice(0, lastSlash) : path;
                    const preset = `${parent}${parent.endsWith(separator) || !parent ? "" : separator}${name}`;
                    return <button key={name} type="button" onClick={() => changeSetting("downloadLocation", preset)} className="rounded-md border border-neutral-800 bg-neutral-900 px-2.5 py-1 font-mono text-neutral-300 hover:border-neutral-600">{name}</button>;
                  })}
                </div>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-emerald-700/40 bg-emerald-950/30 text-emerald-300"><HardDrive className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Storage policy</h4><p className="mt-0.5 text-[11px] text-neutral-400">Choose which durable copy each newly queued job keeps after finalization.</p></div></div>
                <div className="grid gap-2 md:grid-cols-3">
                  {([
                    ["managed-published", "Managed + Published", "Keep a private Library copy and a copy in your configured output folder. Uses the most disk space."],
                    ["published-only", "Published only", "Keep only the configured output copy after publishing. Library metadata remains, but in-app Save links are unavailable."],
                    ["managed-only", "Managed only", "Keep only the private Library copy and do not publish to the output folder. Managed media follows app retention."],
                  ] as const).map(([value, label, description]) => <label key={value} className={`cursor-pointer rounded-lg border p-3 ${settings.storageMode === value ? "border-rose-600/70 bg-rose-950/20" : "border-neutral-800 bg-neutral-900/50"}`}><span className="flex items-center justify-between gap-2 text-[11px] font-semibold text-neutral-200">{label}<input type="radio" name="storage-mode" value={value} checked={settings.storageMode === value} onChange={() => changeSetting("storageMode", value)} className="accent-rose-600" /></span><span className="mt-1 block text-[10px] leading-relaxed text-neutral-500">{description}</span></label>)}
                </div>
                <p className="mt-3 text-[10px] leading-relaxed text-neutral-500">The selected policy is captured when a job is queued. Changing this setting later does not alter existing jobs.</p>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-blue-700/40 bg-blue-950/30 text-blue-300"><FileText className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">File naming format preferences</h4><p className="mt-0.5 text-[11px] text-neutral-400">Use tokens to build the saved filename.</p></div></div>
                <label className="block text-[11px] font-medium text-neutral-300" htmlFor="naming-pattern">Naming pattern template</label>
                <input id="naming-pattern" type="text" className={`${field} mt-1 font-mono text-xs`} value={settings.namingPattern} onChange={event => changeSetting("namingPattern", event.target.value)} />
                <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px]"><span className="mr-1 text-neutral-500">Insert token:</span>{["{channel}", "{title}", "{resolution}", "{category}"].map(token => <button key={token} type="button" onClick={() => changeSetting("namingPattern", `${settings.namingPattern}${settings.namingPattern ? " " : ""}${token}`)} className="rounded-md border border-neutral-800 bg-neutral-900 px-2.5 py-1 font-mono text-neutral-300 hover:border-neutral-600"><Plus className="mr-1 inline size-3 text-rose-400" aria-hidden="true" />{token}</button>)}</div>
                <div className="mt-3 rounded-lg border border-neutral-800 bg-neutral-900/70 p-3"><div className="flex items-center gap-1.5 text-[10px] font-semibold text-neutral-300"><Sparkles className="size-3.5 text-amber-300" aria-hidden="true" />Live file generation preview</div>
                  {(() => {
                    const sample = settings.namingPattern.replaceAll("{channel}", "Marques Brownlee").replaceAll("{title}", "M3 Max MacBook Pro Deep Dive").replaceAll("{resolution}", "1080p").replaceAll("{category}", settings.defaultCategory || "General");
                    const safePreview = sample || "Untitled";
                    const separator = settings.downloadLocation.includes("\\") ? "\\" : "/";
                    const subfolder = settings.subfolderSorting === "channel" ? "Marques Brownlee" : settings.subfolderSorting === "category" ? settings.defaultCategory : "";
                    return <><p className="mt-1 break-all font-mono text-xs text-emerald-300">{safePreview}.mp4</p><p className="mt-1 break-all font-mono text-[10px] text-neutral-400">{settings.downloadLocation}{subfolder ? `${separator}${subfolder}` : ""}{separator}{safePreview}.mp4</p></>;
                  })()}
                </div>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-purple-700/40 bg-purple-950/30 text-purple-300"><FolderTree className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Subfolder sorting organization</h4><p className="mt-0.5 text-[11px] text-neutral-400">Organize finished downloads into structured folders.</p></div></div>
                <div className="grid gap-2 sm:grid-cols-3">
                  {([ ["channel", "Sort by YouTube channel"], ["category", "Sort by category"], ["flat", "No subfolders"] ] as const).map(([value, label]) => <label key={value} className={`cursor-pointer rounded-lg border p-3 ${settings.subfolderSorting === value ? "border-rose-600/70 bg-rose-950/20" : "border-neutral-800 bg-neutral-900/50"}`}><span className="flex items-center justify-between text-[11px] font-semibold text-neutral-200">{label}<input type="radio" name="subfolder-sorting" checked={settings.subfolderSorting === value} onChange={() => changeSetting("subfolderSorting", value)} className="accent-rose-600" /></span><span className="mt-1 block text-[10px] text-neutral-500">{value === "channel" ? "Files grouped by creator." : value === "category" ? "Files grouped by your selected category." : "Save directly in the destination."}</span></label>)}
                </div>
                <label className="mt-3 block max-w-sm text-[11px] font-medium text-neutral-300">Default category for new downloads
                  <select className={`${field} mt-1.5 text-xs`} value={settings.defaultCategory} onChange={event => changeSetting("defaultCategory", event.target.value)}>{settings.userCategories.map(category => <option key={category} value={category}>{category}</option>)}</select>
                </label>
                <div className="mt-3 rounded-lg border border-neutral-800 bg-neutral-900/50 p-3">
                  <div className="flex items-center justify-between text-[11px]"><span className="font-semibold text-neutral-300">User-defined categories</span><span className="text-neutral-500">{settings.userCategories.length} active</span></div>
                  <div className="mt-2 flex flex-wrap gap-1.5">{settings.userCategories.map(category => <span key={category} className="inline-flex items-center gap-1 rounded-md border border-neutral-800 bg-neutral-950 px-2 py-1 text-[10px] text-neutral-200">{category}<button type="button" onClick={() => removeCategory(category)} disabled={settings.userCategories.length <= 1} aria-label={`Remove ${category}`} className="text-neutral-500 hover:text-rose-300 disabled:opacity-40"><X className="size-3" aria-hidden="true" /></button></span>)}</div>
                  <div className="mt-2 flex gap-2"><input type="text" aria-label="New category" maxLength={40} value={newCategoryInput} onChange={event => setNewCategoryInput(event.target.value)} onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); addCategory(); } }} placeholder="Add a category" className={`${field} py-2 text-xs`} /><button type="button" onClick={addCategory} className={button}><Plus className="size-3.5" aria-hidden="true" />Add</button></div>
                </div>
              </div>

              <div className="grid gap-4 border-t border-neutral-800 pt-4 sm:grid-cols-3">
                <label className="block text-xs font-medium text-neutral-300">Default maximum video quality
                  <select aria-label="Default maximum video quality" className={`${field} mt-1.5`} value={settings.defaultQuality} onChange={event => changeSetting("defaultQuality", event.target.value as Quality)}>{(Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
                </label>
                <label className="block text-xs font-medium text-neutral-300">Maximum concurrent downloads <span className="float-right font-mono text-rose-300">{settings.maxConcurrentDownloads}</span>
                  <input aria-label="Maximum concurrent downloads" type="range" min="1" max="6" step="1" value={settings.maxConcurrentDownloads} onChange={event => changeSetting("maxConcurrentDownloads", Number(event.target.value))} className="mt-2 w-full accent-rose-600" />
                  <span className="mt-1 flex justify-between text-[10px] text-neutral-500"><span>1 stream</span><span>6 streams</span></span>
                </label>
                <label className="block text-xs font-medium text-neutral-300">Global bandwidth limit <span className="float-right font-mono text-rose-300">{settings.bandwidthLimitBytesPerSec > 0 ? `${(settings.bandwidthLimitBytesPerSec / 1048576).toFixed(settings.bandwidthLimitBytesPerSec % 1048576 === 0 ? 0 : 2)} MiB/s` : "Unlimited"}</span>
                  <input aria-label="Global bandwidth limit in MiB per second" type="number" min="0" max="1024" step="0.25" value={settings.bandwidthLimitBytesPerSec / 1048576} onChange={event => changeSetting("bandwidthLimitBytesPerSec", Math.max(0, Math.round((Number(event.target.value) || 0) * 1048576)))} className={`${field} mt-1.5`} />
                  <span className="mt-1 block text-[10px] leading-relaxed text-neutral-500">0 = unlimited. The cap is shared fairly across active Go-managed transfers and applies immediately.</span>
                </label>
              </div>
              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="min-w-0">
                    <h4 className="text-xs font-bold text-neutral-200">System notifications</h4>
                    <p className="mt-1 max-w-2xl text-[10px] leading-relaxed text-neutral-500">Optional OS/browser alerts for completed downloads, failed jobs, partially completed playlists, and storage/output errors. Permission is requested only when you turn this on.</p>
                  </div>
                  <button
                    type="button"
                    role="switch"
                    aria-label="System notifications"
                    aria-checked={settings.notificationsEnabled}
                    onClick={() => void toggleNotifications()}
                    className={`inline-flex min-h-9 items-center rounded-full border px-3 text-[11px] font-semibold transition-colors ${settings.notificationsEnabled ? "border-emerald-700/60 bg-emerald-950/40 text-emerald-300" : "border-neutral-700 bg-neutral-900 text-neutral-300"}`}
                  >
                    {settings.notificationsEnabled ? "Enabled" : "Off"}
                  </button>
                </div>
                <p className="mt-2 text-[10px] text-neutral-500">
                  {notificationAPI()
                    ? notificationAPI()?.permission === "granted"
                      ? "Browser/OS permission is granted."
                      : notificationAPI()?.permission === "denied"
                        ? "Browser/OS permission is blocked; change it in notification settings before enabling."
                        : "Permission has not been requested yet."
                    : "This browser does not expose the system Notification API."}
                </p>
              </div>
              {serviceError && <p role="alert" className="text-xs text-red-300">{serviceError}</p>}
              <div className="flex flex-wrap items-center justify-between gap-3 border-t border-neutral-800 pt-4">
                <p className="max-w-lg text-[10px] leading-relaxed text-neutral-500">Settings apply to new jobs. Each download also stays in private app storage for the library and secure save links. {mp3Supported ? "Built-in Go MP3 conversion is ready." : "This backend does not support MP3 conversion."}</p>
                <button type="submit" className={primaryButton} disabled={!serviceReady || savingSettings}>{savingSettings && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}{settingsSaved ? <Check className="size-4" aria-hidden="true" /> : null}{savingSettings ? "Saving…" : settingsSaved ? "Saved to SQLite" : "Save preferences"}</button>
              </div>
            </section>
          </form>

            <section className={`${panel} p-5`}><div className="flex items-start gap-3"><div className="grid size-9 shrink-0 place-items-center rounded-xl border border-blue-800/50 bg-blue-950/30 text-blue-300"><Gauge className="size-4" aria-hidden="true" /></div><div><h3 className="text-xs font-bold">What this backend supports</h3><ul className="mt-2 space-y-1.5 text-[11px] leading-relaxed text-neutral-400"><li>Video and playlist downloads, including adaptive H.264/AAC MP4, high-resolution VP9/AV1 + Opus WebM remuxing, and pure-Go MP3 conversion for AAC audio.</li><li>Up to six concurrent jobs, multi-routine stream transfers, fair global bandwidth limiting, pause/resume, retries, live speed/ETA, and optional system notifications.</li><li>Quality ceilings: best, 2160p (4K), 1440p, 1080p, 720p, or 480p. Actual output quality and container are reported after completion.</li><li>Files are copied to the selected destination and remain available in the private SQLite-backed library.</li></ul>{!mp3Supported && <p className="mt-3 flex items-start gap-1.5 text-[10px] leading-relaxed text-amber-300"><ShieldCheck className="mt-0.5 size-3 shrink-0" aria-hidden="true" />This backend does not support MP3 conversion.</p>}</div></div></section>
        </div>
  );
}
