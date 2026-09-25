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

  function changeSetting<K extends keyof AppSettings>(key: K, value: AppSettings[K]) {
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

  async function savePreferences(event: FormEvent) {
    event.preventDefault();
    if (!serviceReady) {
      setServiceError("The built-in Go service is still starting. It will connect automatically.");
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
      setRuntimeSettingSources(result.sources ?? {});
      setRuntimeSettingValues(result.effective ?? {});
      setSettingsSaved(true);
    } catch (error) {
      setServiceError(errorMessage(error));
    } finally {
      setSavingSettings(false);
    }
  }

  async function toggleNotifications() {
    if (settings.notificationsEnabled) {
      changeSetting("notificationsEnabled", false);
      return;
    }
    const NotificationAPI = notificationAPI();
    if (!NotificationAPI) {
      setServiceError("System notifications are not supported by this browser.");
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
      setServiceError("Notification permission was not granted. Enable it in your browser/OS settings to use system notifications.");
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
    changeSetting,
    selectDownloadFolder,
    savePreferences,
    toggleNotifications,
    addCategory,
    removeCategory,
  };
}
