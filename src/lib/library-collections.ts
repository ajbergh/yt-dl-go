/**
 * Immutable collection helpers and legacy-cache decoding for the Media Vault.
 * This is intentionally separate from download jobs: deleting a collection
 * cannot delete, move, rename, or forget any downloaded media.
 *
 * useLibraryOrganization owns persistence through the Go/SQLite API; the
 * localStorage reader remains for migration and compatibility caching.
 */
export const libraryCollectionsKey = "yt-dl-go:library-collections-v2";
export const maxCollectionNameLength = 80;

export interface MediaCollection {
  id: string;
  name: string;
  itemKeys: string[];
}

/** Salvage valid entries, trim names, discard duplicate IDs/names, and deduplicate keys. */
export function normalizeCollections(value: unknown): MediaCollection[] {
  if (!Array.isArray(value)) return [];
  const ids = new Set<string>();
  const names = new Set<string>();
  const collections: MediaCollection[] = [];
  for (const entry of value) {
    if (!entry || typeof entry !== "object") continue;
    const data = entry as Record<string, unknown>;
    if (typeof data.id !== "string" || !/^[a-zA-Z0-9_-]{1,100}$/.test(data.id)) continue;
    if (typeof data.name !== "string") continue;
    const name = data.name.trim().slice(0, maxCollectionNameLength);
    if (!name || ids.has(data.id) || names.has(name.toLocaleLowerCase())) continue;
    if (!Array.isArray(data.itemKeys)) continue;
    const itemKeys = [...new Set(data.itemKeys.filter(
      (key): key is string => typeof key === "string" && key.length <= 256 && key.length > 0,
    ))];
    collections.push({ id: data.id, name, itemKeys });
    ids.add(data.id);
    names.add(name.toLocaleLowerCase());
  }
  return collections;
}

export function readCollections(storage: Pick<Storage, "getItem"> | undefined = typeof window === "undefined" ? undefined : window.localStorage): MediaCollection[] {
  if (!storage) return [];
  try {
    return normalizeCollections(JSON.parse(storage.getItem(libraryCollectionsKey) || "[]"));
  } catch {
    return [];
  }
}

export function collectionNameIsAvailable(collections: MediaCollection[], name: string, exceptID?: string): boolean {
  const candidate = name.trim();
  return candidate.length > 0 && candidate.length <= maxCollectionNameLength && !collections.some(
    collection => collection.id !== exceptID && collection.name.toLocaleLowerCase() === candidate.toLocaleLowerCase(),
  );
}

export function addCollection(collections: MediaCollection[], id: string, name: string): MediaCollection[] {
  if (!/^[a-zA-Z0-9_-]{1,100}$/.test(id) || collections.some(collection => collection.id === id)) return collections;
  if (!collectionNameIsAvailable(collections, name)) return collections;
  return [...collections, { id, name: name.trim(), itemKeys: [] }];
}

export function renameCollection(collections: MediaCollection[], id: string, name: string): MediaCollection[] {
  if (!collectionNameIsAvailable(collections, name, id)) return collections;
  return collections.map(collection => collection.id === id ? { ...collection, name: name.trim() } : collection);
}

export function toggleCollectionItem(collections: MediaCollection[], id: string, itemKey: string, selected: boolean): MediaCollection[] {
  if (!itemKey || itemKey.length > 256) return collections;
  return collections.map(collection => collection.id !== id ? collection : {
    ...collection,
    itemKeys: selected
      ? collection.itemKeys.includes(itemKey) ? collection.itemKeys : [...collection.itemKeys, itemKey]
      : collection.itemKeys.filter(key => key !== itemKey),
  });
}

export function removeCollection(collections: MediaCollection[], id: string): MediaCollection[] {
  return collections.filter(collection => collection.id !== id);
}
