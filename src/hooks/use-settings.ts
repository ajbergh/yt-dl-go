/**
 * Preference draft editing and persistence. The first edit captures a baseline
 * for dirty tracking; successful saves adopt server-normalized preferences and
 * runtime metadata. Folder/notification choices remain drafts until saved.
 */
import { useState, type Dispatch, type FormEvent, type SetStateAction } from "react";
import { api, type AppSettings, type RuntimeSettingSources, type RuntimeSettingValues, type ServiceConnection } from "../lib/downloader";
import { errorMessage, notificationAPI } from "../components/downloader/view-model";

type UseSettingsOptions = {
  connection: ServiceConnection;
  serviceReady: boolean;
  settings: AppSettings;
  setSettings: Dispatch<SetStateAction<AppSettings>>;
  setRuntimeSettingSources: Dispatch<SetStateAction<RuntimeSettingSources>>;
  setRuntimeSettingValues: Dispatch<SetStateAction<RuntimeSettingValues>>;
  setServiceError: Dispatch<SetStateAction<string>>;
  setNotice: Dispatch<SetStateAction<string>>;
};

export function useSettings({
  connection, serviceReady, settings, setSettings, setRuntimeSettingSources, setRuntimeSettingValues, setServiceError, setNotice,
}: UseSettingsOptions) {
  const [newCategoryInput, setNewCategoryInput] = useState("");
  const [savingSettings, setSavingSettings] = useState(false);
  const [settingsSaved, setSettingsSaved] = useState(false);
  const [settingsBeforeEdits, setSettingsBeforeEdits] = useState<AppSettings | null>(null);
  const settingsDirty = settingsBeforeEdits !== null && JSON.stringify(settingsBeforeEdits) !== JSON.stringify(settings);

  function changeSetting<K extends keyof AppSettings>(key: K, value: AppSettings[K]) {
    setSettingsBeforeEdits(previous => previous ?? settings);
    setSettings(previous => ({ ...previous, [key]: value }));
    setSettingsSaved(false);
  }

  async function selectDownloadFolder() {
    if (!serviceReady) return;
    setServiceError("");
    try {
      const result = await api<{ path: string } | undefined>(connection, "/api/folders/select", {
        method: "POST",
        body: "{}",
        signal: AbortSignal.timeout(5 * 60 * 1000),
      });
      if (!result?.path) return;
      changeSetting("downloadLocation", result.path);
      setNotice("Download folder selected. Save preferences to apply it to newly queued jobs.");
    } catch (error) {
      setServiceError(errorMessage(error));
    }
  }

  // Submit the full draft and clear its baseline only after the server accepts it.
  async function savePreferences(event: FormEvent) {
    event.preventDefault();
    if (!serviceReady) {
      setServiceError("The app is still starting. Please try again in a moment.");
      return;
    }
    setSavingSettings(true);
    setServiceError("");
    setSettingsSaved(false);
    try {
      const result = await api<{ settings: AppSettings; sources?: RuntimeSettingSources; effective?: RuntimeSettingValues }>(connection, "/api/settings", {
        method: "PUT",
        body: JSON.stringify(settings),
        signal: AbortSignal.timeout(10000),
      });
      setSettings(result.settings);
      setSettingsBeforeEdits(null);
      setRuntimeSettingSources(result.sources ?? {});
      setRuntimeSettingValues(result.effective ?? {});
      setSettingsSaved(true);
    } catch (error) {
      setServiceError(errorMessage(error));
    } finally {
      setSavingSettings(false);
    }
  }

  // Ask for system permission only on explicit enable; persist the preference on Save.
  async function toggleNotifications() {
    if (settings.notificationsEnabled) {
      changeSetting("notificationsEnabled", false);
      return;
    }
    const NotificationAPI = notificationAPI();
    if (!NotificationAPI) {
      setServiceError("System notifications are unavailable on this device.");
      return;
    }
    let permission = NotificationAPI.permission;
    if (permission === "default") {
      try {
        permission = await NotificationAPI.requestPermission();
      } catch {
        permission = "denied";
      }
    }
    if (permission !== "granted") {
      setServiceError("Notifications are blocked. Allow them in system settings to enable alerts.");
      return;
    }
    setServiceError("");
    changeSetting("notificationsEnabled", true);
    setNotice("System notifications enabled. Save preferences to keep this setting.");
  }

  function addCategory() {
    const category = newCategoryInput.trim();
    if (!category || settings.userCategories.some(value => value.toLowerCase() === category.toLowerCase())) return;
    if (settings.userCategories.length >= 50) {
      setServiceError("You can save up to 50 categories.");
      return;
    }
    changeSetting("userCategories", [...settings.userCategories, category]);
    setNewCategoryInput("");
  }

  function removeCategory(category: string) {
    if (settings.userCategories.length <= 1) return;
    setSettingsBeforeEdits(previous => previous ?? settings);
    const remaining = settings.userCategories.filter(value => value !== category);
    setSettings(previous => ({
      ...previous,
      userCategories: remaining,
      defaultCategory: previous.defaultCategory === category ? (remaining[0] ?? "General") : previous.defaultCategory,
    }));
    setSettingsSaved(false);
  }

  return {
    newCategoryInput,
    setNewCategoryInput,
    savingSettings,
    settingsSaved,
    settingsDirty,
    changeSetting,
    selectDownloadFolder,
    savePreferences,
    toggleNotifications,
    addCategory,
    removeCategory,
  };
}
