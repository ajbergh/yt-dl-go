/**
 * Persist Vault favorites/collections through the revisioned SQLite API.
 * localStorage supplies legacy migration data and a compatibility cache;
 * it is not the authoritative organization store.
 */
import { userFacingMessage } from "../lib/messages";
import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { api, type ServiceConnection } from "../lib/downloader";
import { libraryCollectionsKey, normalizeCollections, readCollections, type MediaCollection } from "../lib/library-collections";
import { setFavoriteBatch } from "../lib/library-selection";

const favoritesStorageKey = "yt-dl-go:library-favorites-v2";

export interface LibraryOrganization {
  revision: number;
  favorites: string[];
  collections: MediaCollection[];
}

function readLegacyFavorites(): string[] {
  try {
    const stored: unknown = JSON.parse(window.localStorage.getItem(favoritesStorageKey) || "[]");
    return Array.isArray(stored)
      ? [...new Set(stored.filter((value): value is string => typeof value === "string" && value.length > 0 && value.length <= 256))]
      : [];
  } catch {
    return [];
  }
}

function normalizeOrganization(input: LibraryOrganization): LibraryOrganization {
  return {
    revision: input.revision,
    favorites: Array.isArray(input.favorites) ? [...new Set(input.favorites.filter(value => typeof value === "string"))] : [],
    collections: normalizeCollections(input.collections),
  };
}

function mirrorLegacyCache(organization: LibraryOrganization) {
  try {
    // Retain a compatibility cache for older builds and offline fallback.
    // The SQLite API, not this browser cache, is the authoritative source.
    window.localStorage.setItem(favoritesStorageKey, JSON.stringify(organization.favorites));
    window.localStorage.setItem(libraryCollectionsKey, JSON.stringify(organization.collections));
  } catch {
    // Settings are still persisted in SQLite when localStorage is disabled.
  }
}

/**
 * A revision-checked persistence queue. Writes are serialized; edits made during
 * a write are coalesced into the next snapshot. External-window conflicts surface
 * an error rather than silently overwriting another window's organization.
 * onSaved runs after each queued PUT succeeds and updates the revision/cache,
 * allowing callers to refresh server-derived counts. Initial reads and the
 * one-time legacy migration do not invoke this callback.
 */
export function useLibraryOrganization(connection: ServiceConnection, onSaved?: () => void): {
  favorites: string[];
  collections: MediaCollection[];
  ready: boolean;
  error: string;
  toggleFavorite: (key: string) => void;
  setFavoritesForKeys: (keys: string[], favorite: boolean) => void;
  setCollections: Dispatch<SetStateAction<MediaCollection[]>>;
  reload: () => void;
} {
  const [organization, setOrganization] = useState<LibraryOrganization>(() => ({
    revision: 0, favorites: readLegacyFavorites(), collections: readCollections(),
  }));
  const [ready, setReady] = useState(false);
  const [error, setError] = useState("");
  const current = useRef(organization);
  const revision = useRef(0);
  const desiredChange = useRef(0);
  const savedChange = useRef(0);
  const saving = useRef(false);
  const writable = useRef(false);
  const requestGeneration = useRef(0);

  // Pause edits while loading; migrate local data only into an untouched row.
  const load = useCallback(async () => {
    const generation = ++requestGeneration.current;
    writable.current = false;
    try {
      let data = normalizeOrganization(await api<LibraryOrganization>(connection, "/api/library/organization", {
        signal: AbortSignal.timeout(10000),
      }));
      if (data.revision === 0 && data.favorites.length === 0 && data.collections.length === 0) {
        // One-time migration of previous local-only preferences. A nonzero
        // revision means the server has already been initialized, even if empty.
        const oldFavorites = readLegacyFavorites();
        const oldCollections = readCollections();
        if (oldFavorites.length || oldCollections.length) {
          data = normalizeOrganization(await api<LibraryOrganization>(connection, "/api/library/organization", {
            method: "PUT",
            body: JSON.stringify({ revision: 0, favorites: oldFavorites, collections: oldCollections }),
            signal: AbortSignal.timeout(10000),
          }));
        }
      }
      if (generation !== requestGeneration.current) return;
      current.current = data;
      revision.current = data.revision;
      desiredChange.current = 0;
      savedChange.current = 0;
      setOrganization(data);
      mirrorLegacyCache(data);
      setError("");
      writable.current = true;
      setReady(true);
    } catch (reason) {
      if (generation !== requestGeneration.current) return;
      setReady(false);
      setError(reason instanceof Error ? userFacingMessage(reason.message) : "Could not load vault organization.");
    }
  }, [connection]);

  useEffect(() => {
    // Schedule the external data read after the effect body. React effects must
    // not synchronously call a state-setting loader during render commit.
    void Promise.resolve().then(load);
    return () => { requestGeneration.current += 1; writable.current = false; };
  }, [load]);

  // Serialize pending snapshots against the last acknowledged revision. On a
  // failed write, block further edits until an explicit reload resolves state.
  const flush = useCallback(async () => {
    if (saving.current || !writable.current) return;
    saving.current = true;
    const generation = requestGeneration.current;
    try {
      while (writable.current && savedChange.current < desiredChange.current && generation === requestGeneration.current) {
        const change = desiredChange.current;
        const snapshot = current.current;
        const updated = await api<LibraryOrganization>(connection, "/api/library/organization", {
          method: "PUT",
          body: JSON.stringify({ revision: revision.current, favorites: snapshot.favorites, collections: snapshot.collections }),
          signal: AbortSignal.timeout(15000),
        });
        if (generation !== requestGeneration.current) return;
        revision.current = updated.revision;
        savedChange.current = change;
        mirrorLegacyCache(snapshot);
        setError("");
        onSaved?.();
      }
    } catch (reason) {
      if (generation !== requestGeneration.current) return;
      writable.current = false;
      setReady(false);
      setError(reason instanceof Error ? userFacingMessage(reason.message) : "Could not save vault organization.");
    } finally {
      saving.current = false;
    }
  }, [connection, onSaved]);

  const update = useCallback((fn: (previous: LibraryOrganization) => LibraryOrganization) => {
    if (!writable.current) return;
    const next = fn(current.current);
    if (next === current.current) return;
    current.current = next;
    setOrganization(next);
    desiredChange.current += 1;
    void flush();
  }, [flush]);

  const toggleFavorite = useCallback((key: string) => update(previous => ({
    ...previous,
    favorites: previous.favorites.includes(key)
      ? previous.favorites.filter(item => item !== key)
      : [...previous.favorites, key],
  })), [update]);

  const setFavoritesForKeys = useCallback((keys: string[], favorite: boolean) => update(previous => {
    const next = setFavoriteBatch(previous.favorites, keys, favorite);
    return next === previous.favorites ? previous : { ...previous, favorites: next };
  }), [update]);

  const setCollections: Dispatch<SetStateAction<MediaCollection[]>> = useCallback(updater => {
    update(previous => ({
      ...previous,
      collections: typeof updater === "function" ? updater(previous.collections) : updater,
    }));
  }, [update]);

  return {
    favorites: organization.favorites,
    collections: organization.collections,
    ready,
    error,
    toggleFavorite,
    setFavoritesForKeys,
    setCollections,
    reload: () => { setReady(false); setError(""); void load(); },
  };
}
