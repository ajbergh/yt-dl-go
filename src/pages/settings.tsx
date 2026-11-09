/**
 * Preference editing, advanced/runtime settings, diagnostics, and Vault transfer.
 * Collapsing advanced fields preserves edits; Save submits the parent's complete
 * preferences. Backup/import operations run separately from preference saves.
 */
import { useEffect, useRef, useState, type Dispatch, type FormEvent, type SetStateAction } from "react";
import {
	Check, Download, ExternalLink, FileText, Folder, FolderTree, HardDrive, LoaderCircle, Plus, RefreshCw, Sparkles, X,
} from "lucide-react";
import { api, apiBlob, type AppSettings, type BuildInfo, type CorruptionDiagnostics, type Quality, type RuntimeSettingSources, type RuntimeSettingValues, type ServiceConnection, type UpdateStatus, type VideoStrategy } from "../lib/downloader";
import { namingTokenNames, previewFilename, sanitizeFilenameComponent, type NamingValues } from "../lib/naming";
import { desktopBridge } from "../lib/desktop";
import { userFacingMessage } from "../lib/messages";
import { ContextMenuArea } from "../components/context-menu";
import {
  button, field, notificationAPI, panel, primaryButton, qualityLabels, videoStrategyLabels,
} from "../components/downloader/view-model";

type SettingsPageProps = {
  connection: ServiceConnection;
  serviceReady: boolean;
  serviceError: string;
  settings: AppSettings;
  runtimeSettingSources: RuntimeSettingSources;
  runtimeSettingValues: RuntimeSettingValues;
  savingSettings: boolean;
  settingsSaved: boolean;
  settingsDirty: boolean;
  mp3Supported: boolean;
  buildInfo: BuildInfo;
  updateStatus: UpdateStatus | null;
  updateError: string;
  checkingUpdates: boolean;
  checkForUpdates: () => void | Promise<void>;
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
  connection, serviceReady, serviceError, settings, runtimeSettingSources, runtimeSettingValues, savingSettings, settingsSaved, settingsDirty, mp3Supported,
  buildInfo, updateStatus, updateError, checkingUpdates, checkForUpdates,
  newCategoryInput, setNewCategoryInput, savePreferences, selectDownloadFolder, changeSetting,
  addCategory, removeCategory, toggleNotifications,
}: SettingsPageProps) {
  const [advancedSettings, setAdvancedSettings] = useState(false);
  const preferencesForm = useRef<HTMLFormElement>(null);

  return (
<ContextMenuArea label="Settings" items={[
  { label: "Save preferences", disabled: !serviceReady || savingSettings, action: () => preferencesForm.current?.requestSubmit() },
  { label: "Choose download folder", disabled: !serviceReady, action: selectDownloadFolder },
  { label: advancedSettings ? "Hide advanced settings" : "Show advanced settings", action: () => setAdvancedSettings(value => !value) },
  { label: "Check for updates", disabled: !serviceReady || checkingUpdates, action: checkForUpdates },
]}><div tabIndex={0} aria-label="Settings" className="mx-auto max-w-4xl space-y-5">
          <div><p className="mb-1 text-xs font-bold uppercase tracking-[0.16em] text-neutral-400">Configuration</p><h2 className="text-2xl font-bold">Settings</h2><p className="mt-1 text-xs text-neutral-400">Choose where downloads go and how they are organized. Changes apply to new downloads after saving.</p></div>
          {(serviceError || !serviceReady) && <p role={serviceError ? "alert" : "status"} className="rounded-xl border border-amber-800/60 bg-amber-950/30 p-4 text-sm text-amber-200">{serviceError ? userFacingMessage(serviceError) : "Starting the app. Settings will be available shortly."}</p>}

          <form ref={preferencesForm} onSubmit={savePreferences} className="space-y-5">
            <section className={`${panel} space-y-4 p-5 sm:p-6`}>
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-neutral-800 pb-4">
                <div><h3 className="text-sm font-bold">Download preferences</h3><p className="mt-1 text-xs text-neutral-400">Choose where finished files go, how they are named, and how folders are organized.</p></div>
                <p role="status" className={`text-xs ${settingsDirty ? "text-amber-300" : "text-neutral-400"}`}>{settingsDirty ? "Unsaved changes" : "Preferences are up to date"}</p>
              </div>

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-amber-700/40 bg-amber-950/30 text-amber-300"><Folder className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Download folder</h4><p className="mt-0.5 text-xs text-neutral-400">This folder is your vault. Play and preview files directly from here.</p></div></div>
                <label className="block text-xs font-medium text-neutral-300" htmlFor="download-location">Folder path</label>
                <div className="mt-1 flex gap-2"><input id="download-location" type="text" title={settings.downloadLocation} autoComplete="off" className={`${field} min-w-0 font-mono text-xs`} value={settings.downloadLocation} onChange={event => changeSetting("downloadLocation", event.target.value)} placeholder="C:\\Users\\you\\Downloads\\YouTube_Vault" /><button type="button" className={button} onClick={() => void selectDownloadFolder()} disabled={!serviceReady} title="Choose a folder with the operating system picker"><Folder className="size-3.5 text-amber-400" aria-hidden="true" />Browse</button></div>
                <p className="mt-2 text-xs text-neutral-400">Enter the full folder path. The app creates the folder when a download is queued. Existing files stay in their original locations.</p>
                <div className="mt-3 flex flex-wrap items-center gap-1.5 text-xs">
                  <span className="mr-1 text-neutral-400">Quick paths:</span>
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
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-purple-700/40 bg-purple-950/30 text-purple-300"><FolderTree className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Folder organization</h4><p className="mt-0.5 text-xs text-neutral-400">Organize finished downloads into structured folders.</p></div></div>
                <div className="grid gap-2 sm:grid-cols-3">
                  {([ ["channel", "Sort by YouTube channel"], ["category", "Sort by category"], ["flat", "No subfolders"] ] as const).map(([value, label]) => <label key={value} className={`cursor-pointer rounded-lg border p-3 ${settings.subfolderSorting === value ? "border-rose-600/70 bg-rose-950/20" : "border-neutral-800 bg-neutral-900/50"}`}><span className="flex items-center justify-between text-xs font-semibold text-neutral-200">{label}<input type="radio" name="subfolder-sorting" checked={settings.subfolderSorting === value} onChange={() => changeSetting("subfolderSorting", value)} className="accent-rose-600" /></span><span className="mt-1 block text-xs text-neutral-400">{value === "channel" ? "Files grouped by creator." : value === "category" ? "Files grouped by your selected category." : "Save directly in the destination."}</span></label>)}
                </div>
                <label className="mt-3 block max-w-sm text-xs font-medium text-neutral-300">Default category for new downloads
                  <select className={`${field} mt-1.5 text-xs`} value={settings.defaultCategory} onChange={event => changeSetting("defaultCategory", event.target.value)}>{settings.userCategories.map(category => <option key={category} value={category}>{category}</option>)}</select>
                </label>
                <details className="mt-3 rounded-lg border border-neutral-800 bg-neutral-900/50 p-3">
                  <summary className="cursor-pointer text-xs font-semibold text-neutral-300">Manage categories <span className="ml-2 font-normal text-neutral-400">{settings.userCategories.length} categories</span></summary>
                  <div className="mt-2 flex flex-wrap gap-1.5">{settings.userCategories.map(category => <span key={category} className="inline-flex items-center gap-1 rounded-md border border-neutral-800 bg-neutral-950 px-2 py-1 text-xs text-neutral-200">{category}<button type="button" onClick={() => removeCategory(category)} disabled={settings.userCategories.length <= 1} aria-label={`Remove ${category}`} className="text-neutral-400 hover:text-rose-300 disabled:opacity-40"><X className="size-3" aria-hidden="true" /></button></span>)}</div>
                  <div className="mt-2 flex gap-2"><input type="text" aria-label="New category" maxLength={40} value={newCategoryInput} onChange={event => setNewCategoryInput(event.target.value)} onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); addCategory(); } }} placeholder="Add a category" className={`${field} py-2 text-xs`} /><button type="button" onClick={addCategory} className={button}><Plus className="size-3.5" aria-hidden="true" />Add</button></div>
                </details>
              </div>

              <section className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4" aria-labelledby="automatic-downloads-heading">
                <h4 id="automatic-downloads-heading" className="text-xs font-bold">Download defaults</h4>
                <p className="mt-2 text-xs leading-relaxed text-neutral-400">Quality: {qualityLabels[settings.defaultQuality]}. Format: {videoStrategyLabels[settings.defaultVideoStrategy]}.</p>
                <p className="mt-2 text-xs leading-relaxed text-neutral-400">Automatic selects the best supported streams for each video, including playlist items, without upscaling. You can set a quality limit or preferred format in Advanced settings.</p>
                <button type="button" className={button + " mt-3"} onClick={() => { changeSetting("defaultQuality", "best"); changeSetting("defaultVideoStrategy", "best"); changeSetting("allow360pFallback", false); }} disabled={settings.defaultQuality === "best" && settings.defaultVideoStrategy === "best" && !settings.allow360pFallback}>Use automatic video defaults</button>
              </section>
              <div className="rounded-xl border border-neutral-800 p-4">
                <div className="flex items-center justify-between gap-3">
                  <div><h4 id="advanced-settings-heading" className="text-xs font-bold">Advanced settings</h4><p id="advanced-settings-description" className="mt-1 text-xs text-neutral-400">Adjust formats, transfer limits, storage, file naming, and permissions.</p></div>
                  <button type="button" role="switch" aria-label="Advanced settings" aria-checked={advancedSettings} aria-expanded={advancedSettings} aria-controls="advanced-settings" aria-describedby="advanced-settings-description" className={button} onClick={() => setAdvancedSettings(value => !value)}>{advancedSettings ? "On" : "Off"}</button>
                </div>
              </div>
              {advancedSettings && <div id="advanced-settings" role="region" aria-labelledby="advanced-settings-heading" className="space-y-4">
              <div className="grid gap-4 border-t border-neutral-800 pt-4 sm:grid-cols-2">
                <label className="block text-xs font-medium text-neutral-300">Default maximum video quality
                  <select aria-label="Default maximum video quality" className={`${field} mt-1.5`} value={settings.defaultQuality} onChange={event => changeSetting("defaultQuality", event.target.value as Quality)}>{(Object.entries(qualityLabels) as [Quality, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
                </label>
                <label className="block text-xs font-medium text-neutral-300">Default video format
                  <select aria-label="Default video format" className={`${field} mt-1.5`} value={settings.defaultVideoStrategy} onChange={event => changeSetting("defaultVideoStrategy", event.target.value as VideoStrategy)}>{(Object.entries(videoStrategyLabels) as [VideoStrategy, string][]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-400">Compatibility MP4 is strict H.264/AAC; VP9 and AV1 are preferences with automatic fallback when unavailable.</span>
                </label>
                <div className="rounded-lg border border-neutral-800 bg-neutral-950/60 p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <label htmlFor="allow-360p-fallback" className="text-xs font-semibold text-neutral-200">Allow 360p recovery fallback</label>
                      <p className="mt-1 max-w-sm text-xs leading-relaxed text-neutral-400">Off by default. When an adaptive video cannot be safely completed, retry as a verified progressive MP4 at 360p or lower. With this off, the job reports the adaptive failure without silently downgrading.</p>
                    </div>
                    <input id="allow-360p-fallback" aria-label="Allow 360p recovery fallback" type="checkbox" checked={settings.allow360pFallback} onChange={event => changeSetting("allow360pFallback", event.target.checked)} className="mt-0.5 size-4 shrink-0 accent-rose-600" />
                  </div>
                </div>
                <label className="block text-xs font-medium text-neutral-300">Maximum concurrent downloads <span className="float-right font-mono text-rose-300">{settings.maxConcurrentDownloads}</span>
                  <input aria-label="Maximum concurrent downloads" type="range" min="1" max="6" step="1" value={settings.maxConcurrentDownloads} onChange={event => changeSetting("maxConcurrentDownloads", Number(event.target.value))} className="mt-2 w-full accent-rose-600" />
                  <span className="mt-1 flex justify-between text-xs text-neutral-400"><span>1 stream</span><span>6 streams</span></span>
                </label>
                <label className="block text-xs font-medium text-neutral-300">Global bandwidth limit <span className="float-right font-mono text-rose-300">{settings.bandwidthLimitBytesPerSec > 0 ? `${(settings.bandwidthLimitBytesPerSec / 1048576).toFixed(settings.bandwidthLimitBytesPerSec % 1048576 === 0 ? 0 : 2)} MiB/s` : "Unlimited"}</span>
                  <input aria-label="Global bandwidth limit in MiB per second" type="number" min="0" max="1024" step="0.25" value={settings.bandwidthLimitBytesPerSec / 1048576} onChange={event => changeSetting("bandwidthLimitBytesPerSec", Math.max(0, Math.round((Number(event.target.value) || 0) * 1048576)))} className={`${field} mt-1.5`} />
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-400">0 = unlimited. The cap is shared fairly across active transfers and applies immediately.</span>
                </label>
              </div>
              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-blue-700/40 bg-blue-950/30 text-blue-300"><FileText className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">File naming format preferences</h4><p className="mt-0.5 text-xs text-neutral-400">Use tokens to build the saved filename.</p></div></div>
                <label className="block text-xs font-medium text-neutral-300" htmlFor="naming-pattern">Naming pattern template</label>
                <input id="naming-pattern" type="text" className={`${field} mt-1 font-mono text-xs`} value={settings.namingPattern} onChange={event => changeSetting("namingPattern", event.target.value)} />
                <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs"><span className="mr-1 text-neutral-400">Insert token:</span>{namingTokenNames.map(token => <button key={token} type="button" onClick={() => changeSetting("namingPattern", `${settings.namingPattern}${settings.namingPattern ? " " : ""}${token}`)} className="rounded-md border border-neutral-800 bg-neutral-900 px-2.5 py-1 font-mono text-neutral-300 hover:border-neutral-600"><Plus className="mr-1 inline size-3 text-rose-400" aria-hidden="true" />{token}</button>)}</div>
                <div className="mt-3 rounded-lg border border-neutral-800 bg-neutral-900/70 p-3"><div className="flex items-center gap-1.5 text-xs font-semibold text-neutral-300"><Sparkles className="size-3.5 text-amber-300" aria-hidden="true" />Live file generation preview</div>
                  {(() => {
                    const values: NamingValues = { "{channel}": "Marques Brownlee", "{title}": "M3 Max MacBook Pro Deep Dive", "{resolution}": "1080p", "{category}": settings.defaultCategory || "General", "{id}": "dQw4w9WgXcQ", "{upload_date}": "2026-09-23", "{playlist}": "Example playlist", "{index}": "3", "{ext}": "mp4", "{fps}": "60", "{codec}": "h264" };
                    const safePreview = previewFilename(settings.namingPattern, values, "mp4");
                    const separator = settings.downloadLocation.includes("\\") ? "\\" : "/";
                    const subfolderName = settings.subfolderSorting === "channel" ? "Marques Brownlee" : settings.subfolderSorting === "category" ? settings.defaultCategory : "";
                    const subfolder = subfolderName ? sanitizeFilenameComponent(subfolderName) : "";
                    return <><p className="mt-1 break-all font-mono text-xs text-emerald-300">{safePreview}</p><p className="mt-1 break-all font-mono text-xs text-neutral-400">{settings.downloadLocation}{subfolder ? `${separator}${subfolder}` : ""}{separator}{safePreview}</p><p className="mt-1 text-xs text-neutral-400">Sample values are shown for upload date, playlist, index, FPS, and codec when those details are available.</p></>;
                  })()}
                </div>
              </div>
              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="mb-3 flex items-center gap-3"><span className="grid size-9 place-items-center rounded-lg border border-cyan-700/40 bg-cyan-950/30 text-cyan-300"><HardDrive className="size-4" aria-hidden="true" /></span><div><h4 className="text-xs font-bold">Download folder permissions</h4><p className="mt-0.5 text-xs text-neutral-400">Choose who can access downloaded files and folders on Linux and macOS.</p></div></div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <label className="block text-xs font-medium text-neutral-300">File mode (octal)
                    <input aria-label="Downloaded file permissions" inputMode="numeric" maxLength={4} className={`${field} mt-1.5 font-mono`} value={settings.outputFileMode} onChange={event => changeSetting("outputFileMode", event.target.value)} />
                  </label>
                  <label className="block text-xs font-medium text-neutral-300">Folder mode (octal)
                    <input aria-label="Download folder permissions" inputMode="numeric" maxLength={4} className={`${field} mt-1.5 font-mono`} value={settings.outputFolderMode} onChange={event => changeSetting("outputFolderMode", event.target.value)} />
                  </label>
                </div>
                <p className="mt-2 text-xs leading-relaxed text-neutral-400">Use four octal digits from 0000 to 0777. Defaults 0600 and 0700 keep downloaded files private to this account. These file permissions apply on macOS and Linux.</p>
              </div>
            <section className={`${panel} space-y-4 p-5 sm:p-6`} aria-labelledby="runtime-settings-heading">
              <div className="border-b border-neutral-800 pb-3">
                <h3 id="runtime-settings-heading" className="text-sm font-bold">Performance and limits</h3>
                <p className="mt-1 max-w-3xl text-xs leading-relaxed text-neutral-400">Save changes, then restart the app to apply these limits. Settings managed by your system may override your choices.</p>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="block text-xs font-medium text-neutral-300">Finished Vault retention
                  <input aria-label="Vault retention" type="text" className={`${field} mt-1.5`} value={settings.retention} onChange={event => changeSetting("retention", event.target.value)} placeholder="never or 720h" />
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-400">Use “never” to keep finished downloads in your history, or enter a duration such as 720h (30 days). The minimum is 5m (5 minutes).</span>
                  <RuntimeSettingSource source={runtimeSettingSources.retention} activeValue={runtimeSettingValues.retention} />
                </label>
                <label className="block text-xs font-medium text-neutral-300">Overall job timeout
                  <input aria-label="Overall job timeout" type="text" className={`${field} mt-1.5`} value={settings.jobTimeout} onChange={event => changeSetting("jobTimeout", event.target.value)} placeholder="none or 2h" />
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-400">Use “none” or “0” to disable the overall deadline, or enter at least 1s. Item transfers keep their own deadlines.</span>
                  <RuntimeSettingSource source={runtimeSettingSources.jobTimeout} activeValue={runtimeSettingValues.jobTimeout} />
                </label>
                <label className="block text-xs font-medium text-neutral-300">Browser application path
                  <input aria-label="Chrome executable path" type="text" className={`${field} mt-1.5 font-mono text-xs`} value={settings.chromePath} onChange={event => changeSetting("chromePath", event.target.value)} placeholder="Find automatically" />
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-400">Optional. Leave blank to find Chrome, Chromium, or Edge automatically.</span>
                  <RuntimeSettingSource source={runtimeSettingSources.chromePath} activeValue={runtimeSettingValues.chromePath} />
                </label>
                <label className="block text-xs font-medium text-neutral-300 sm:col-span-2">Simultaneous saves and exports <span className="float-right font-mono text-rose-300">{settings.downloadSlots}</span>
                  <input aria-label="Concurrent file responses" type="range" min="1" max="16" step="1" value={settings.downloadSlots} onChange={event => changeSetting("downloadSlots", Number(event.target.value))} className="mt-2 w-full accent-rose-600" />
                  <span className="mt-1 block text-xs leading-relaxed text-neutral-400">Limit how many files and ZIP archives can be saved at once. This is separate from active downloads.</span>
                  <RuntimeSettingSource source={runtimeSettingSources.downloadSlots} activeValue={runtimeSettingValues.downloadSlots} />
                </label>
              </div>
            </section>
                <CorruptionDiagnosticsPanel connection={connection} serviceReady={serviceReady} />
              </div>}

              <div className="rounded-xl border border-neutral-800 bg-neutral-950/60 p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="min-w-0">
                    <h4 className="text-xs font-bold text-neutral-200">System notifications</h4>
                    <p className="mt-1 max-w-2xl text-xs leading-relaxed text-neutral-400">Get alerts when downloads finish or need attention. Permission is requested only when you turn this on.</p>
                  </div>
                  <button
                    type="button"
                    role="switch"
                    aria-label="System notifications"
                    aria-checked={settings.notificationsEnabled}
                    onClick={() => void toggleNotifications()}
                    className={`inline-flex min-h-9 items-center rounded-full border px-3 text-xs font-semibold transition-colors ${settings.notificationsEnabled ? "border-emerald-700/60 bg-emerald-950/40 text-emerald-300" : "border-neutral-700 bg-neutral-900 text-neutral-300"}`}
                  >
                    {settings.notificationsEnabled ? "Enabled" : "Off"}
                  </button>
                </div>
                <p className="mt-2 text-xs text-neutral-400">
                  {notificationAPI()
                    ? notificationAPI()?.permission === "granted"
                      ? "Notifications are allowed."
                      : notificationAPI()?.permission === "denied"
                        ? "Notifications are blocked. Allow them in system settings before enabling."
                        : "Permission has not been requested yet."
                    : "Notifications are unavailable on this device."}
                </p>
              </div>
              {serviceError && <p role="alert" className="text-xs text-red-300">{userFacingMessage(serviceError)}</p>}
              <div className="sticky bottom-3 z-10 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-neutral-700 bg-neutral-900 p-4 shadow-xl">
                <p className={`max-w-lg text-xs leading-relaxed ${settingsDirty ? "text-amber-300" : "text-neutral-400"}`}>{settingsDirty ? "Unsaved changes — save to apply your preferences." : "Changes apply to new downloads after saving."}</p>
                <button type="submit" className={primaryButton} disabled={!serviceReady || savingSettings}>{savingSettings && <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />}{settingsSaved ? <Check className="size-4" aria-hidden="true" /> : null}{savingSettings ? "Saving…" : settingsSaved ? "Saved" : "Save preferences"}</button>
              </div>
            </section>
          </form>

          <LibraryTransferPanel connection={connection} serviceReady={serviceReady} />

          <details className={`${panel} p-5 sm:p-6`}>
            <summary className="min-h-11 cursor-pointer text-sm font-bold">About & updates</summary>
            <div className="mt-4 space-y-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 id="updates-heading" className="text-sm font-bold">Version & updates</h3>
                <p className="mt-1 text-xs text-neutral-400">Version <span className="font-medium text-neutral-200">{buildInfo.version === "dev" ? "Preview" : buildInfo.version}</span></p>
              </div>
              <button type="button" className={button} onClick={() => void checkForUpdates()} disabled={!serviceReady || checkingUpdates}>
                <RefreshCw className={`size-3.5 ${checkingUpdates ? "animate-spin" : ""}`} aria-hidden="true" />
                {checkingUpdates ? "Checking…" : "Check for updates"}
              </button>
            </div>
            {updateError && <p role="alert" className="mt-3 text-xs text-amber-300">{userFacingMessage(updateError)}</p>}
            {updateStatus?.developmentBuild && <p className="mt-3 text-xs leading-relaxed text-neutral-400">You are using a preview version. Update checks are available in released versions.</p>}
            {updateStatus && !updateStatus.developmentBuild && updateStatus.updateAvailable && <div className="mt-3 rounded-xl border border-blue-800/60 bg-blue-950/30 p-3 text-xs text-blue-100">
              <div className="flex flex-wrap items-center justify-between gap-3"><span><strong>{updateStatus.latestVersion}</strong> is available.</span>{updateStatus.releaseUrl && <a href={updateStatus.releaseUrl} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1.5 font-semibold text-blue-300 hover:text-blue-200">View update <ExternalLink className="size-3.5" aria-hidden="true" /></a>}</div>
              <p className="mt-2 text-xs leading-relaxed text-blue-200/80">Download and install the update to replace this version.</p>
            </div>}
            {updateStatus && !updateStatus.developmentBuild && !updateStatus.updateAvailable && updateStatus.latestVersion && <p className="mt-3 text-xs text-emerald-300">You are on the latest stable release ({updateStatus.latestVersion}).</p>}
            <p className="mt-3 text-xs leading-relaxed text-neutral-400">Updates are installed manually. Your saved vault and preferences stay on this device.</p>
              <p className="text-xs leading-relaxed text-neutral-300">Save videos and playlists as MP4 or WebM, or audio as {mp3Supported ? "MP3 or M4A" : "M4A"}. Finished downloads stay in your chosen folder and appear in the Vault.</p>
              {!mp3Supported && <p className="text-xs text-amber-300">MP3 conversion is unavailable in this version.</p>}
            </div>
          </details>
        </div></ContextMenuArea>
  );
}

function RuntimeSettingSource({ source, activeValue }: { source?: string; activeValue?: string | number }) {
  return <span className="mt-1 block text-xs text-neutral-400">Currently applied: <span className="font-medium text-neutral-300">{activeValue === "" ? "Automatic" : activeValue ?? "Loading…"}</span>{/environment/i.test(source || "") && <span className="block">Managed by your system configuration.</span>}</span>;
}

function CorruptionDiagnosticsPanel({ connection, serviceReady }: Pick<SettingsPageProps, "connection" | "serviceReady">) {
  return <CorruptionDiagnosticsRequest key={JSON.stringify([connection.base, connection.token, serviceReady])} connection={connection} serviceReady={serviceReady} />;
}

function CorruptionDiagnosticsRequest({ connection, serviceReady }: Pick<SettingsPageProps, "connection" | "serviceReady">) {
  const [diagnostics, setDiagnostics] = useState<CorruptionDiagnostics | null>(null);
  const [loading, setLoading] = useState(serviceReady);
  const [error, setError] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    if (!serviceReady) return;
    const controller = new AbortController();
    void api<CorruptionDiagnostics>(connection, "/api/diagnostics", { signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) setDiagnostics(result); })
      .catch(reason => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? userFacingMessage(reason.message) : "Could not load diagnostics.");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [connection, refreshKey, serviceReady]);

  return <section className={`${panel} p-5 sm:p-6`} aria-labelledby="diagnostics-heading">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 id="diagnostics-heading" className="text-sm font-bold">Saved data diagnostics</h3>
        <p className="mt-1 text-xs text-neutral-400">If saved information needs repair, an original copy is kept for recovery.</p>
      </div>
      <button type="button" className={button} onClick={() => { setLoading(true); setError(""); setRefreshKey(value => value + 1); }} disabled={!serviceReady || loading}>
        <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
        {loading ? "Refreshing…" : "Refresh diagnostics"}
      </button>
    </div>
    {error && <p role="alert" className="mt-3 text-xs text-amber-300">{userFacingMessage(error)}</p>}
    {!error && diagnostics && diagnostics.count === 0 && <p className="mt-3 text-xs text-emerald-300">No saved data issues have been recorded.</p>}
    {diagnostics && diagnostics.count > 0 && <>
      <p className="mt-3 text-xs text-amber-200">{diagnostics.count} saved data issue{diagnostics.count === 1 ? "" : "s"} recorded{diagnostics.count > diagnostics.issues.length ? `; showing the latest ${diagnostics.issues.length}` : ""}.</p>
      <ul className="mt-3 space-y-2">
        {diagnostics.issues.map(issue => <li key={issue.id} className="rounded-lg border border-neutral-800 bg-neutral-950/60 p-3">
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
            <span className="text-neutral-200">{issue.sourceTable === "settings" ? "Preferences" : "Vault entry"}</span>
            <time className="text-neutral-400" dateTime={issue.detectedAt}>{new Date(issue.detectedAt).toLocaleString()}</time>
          </div>
          <p className="mt-2 text-xs leading-relaxed text-neutral-300">Saved information was repaired. An original copy was kept for recovery.</p>
          <details className="mt-2 text-xs text-neutral-400"><summary className="cursor-pointer">Recovery reference</summary><p className="mt-1 break-all">{issue.recordKey}</p></details>
        </li>)}
      </ul>
    </>}
    {!serviceReady && <p className="mt-3 text-xs text-neutral-400">Saved data information will load when the app is ready.</p>}
  </section>;
}

/** Transfer metadata ZIPs; media bytes are excluded and imports merge portable records. */
function LibraryTransferPanel({ connection, serviceReady }: Pick<SettingsPageProps, "connection" | "serviceReady">) {
  const importInput = useRef<HTMLInputElement>(null);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  async function exportLibrary() {
    setExporting(true);
    setMessage("");
    setError("");
    try {
      const desktop = desktopBridge();
      if (desktop) {
        const saved = await desktop.Save("/api/library/export");
        setMessage(saved ? "Vault export saved." : "Export cancelled.");
        return;
      }
      const blob = await apiBlob(connection, "/api/library/export", { signal: AbortSignal.timeout(5 * 60 * 1000) });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "yt-dl-go-library-export.zip";
      anchor.rel = "noopener noreferrer";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      setMessage("Vault export downloaded.");
    } catch (reason) {
      setError(reason instanceof Error ? userFacingMessage(reason.message) : "Could not export the Vault.");
    } finally {
      setExporting(false);
    }
  }

  async function importLibrary(file: File) {
    setImporting(true);
    setMessage("");
    setError("");
    try {
      const result = await api<{ imported: number; idempotent: boolean; organizationUpdated?: boolean }>(connection, "/api/library/import", {
        method: "POST", headers: { "Content-Type": "application/zip" }, body: file,
        signal: AbortSignal.timeout(5 * 60 * 1000),
      });
      setMessage(result.idempotent
        ? "This Vault archive is already imported."
        : result.organizationUpdated
          ? `Imported ${result.imported} Vault file${result.imported === 1 ? "" : "s"} and restored favorites and collections.`
          : `Imported ${result.imported} Vault file${result.imported === 1 ? "" : "s"}.`);
    } catch (reason) {
      setError(reason instanceof Error ? userFacingMessage(reason.message) : "Could not import the Vault.");
    } finally {
      setImporting(false);
      if (importInput.current) importInput.current.value = "";
    }
  }

  return <section className={`${panel} p-5 sm:p-6`} aria-labelledby="library-export-heading">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 id="library-export-heading" className="text-sm font-bold">Vault backup and import</h3>
        <p className="mt-1 max-w-2xl text-xs leading-relaxed text-neutral-400">Export vault metadata, favorites, and collections as a ZIP. Media files are not included. Import adds missing items and merges favorites and collections without replacing your existing ones; settings, queues, and history stay on this device. Imported files must be downloaded again to play or save them.</p>
        <p className="mt-2 max-w-2xl text-xs leading-relaxed text-amber-200">The ZIP also includes a private database snapshot containing saved app settings and diagnostics. Store exports securely. Import uses portable Vault metadata only; it does not restore the private database.</p>
      </div>
      <div className="flex flex-wrap gap-2">
        <button type="button" className={button} onClick={() => void exportLibrary()} disabled={!serviceReady || exporting || importing}>
          {exporting ? <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" /> : <Download className="size-3.5" aria-hidden="true" />}
          {exporting ? "Preparing export…" : "Export Vault"}
        </button>
        <input ref={importInput} type="file" accept=".zip,application/zip" className="sr-only" aria-label="Choose Vault export ZIP" onChange={event => { const file = event.currentTarget.files?.[0]; if (file) void importLibrary(file); }} />
        <button type="button" className={button} onClick={() => importInput.current?.click()} disabled={!serviceReady || exporting || importing}>
          {importing ? <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" /> : <Folder className="size-3.5" aria-hidden="true" />}
          {importing ? "Importing…" : "Import Vault ZIP"}
        </button>
      </div>
    </div>
    {message && <p role="status" className="mt-3 text-xs text-emerald-300">{message}</p>}
    {error && <p role="alert" className="mt-3 text-xs text-amber-300">{userFacingMessage(error)}</p>}
    {!serviceReady && <p className="mt-3 text-xs text-neutral-400">Export will be available when the app is ready.</p>}
  </section>;
}
