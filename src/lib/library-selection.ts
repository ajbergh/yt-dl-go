/**
 * Pure selection helpers for the visible, already loaded Library page.
 * These operations never mutate downloaded files or create hidden selections.
 */
export type SelectionModifiers = { range?: boolean; additive?: boolean };

export function chooseMediaKeys(
  orderedKeys: string[],
  selectedKeys: string[],
  targetKey: string,
  anchorKey: string,
  modifiers: SelectionModifiers = {},
): string[] {
  const targetIndex = orderedKeys.indexOf(targetKey);
  if (targetIndex < 0) return selectedKeys.filter(key => orderedKeys.includes(key));
  const selected = selectedKeys.filter(key => orderedKeys.includes(key));
  if (modifiers.range) {
    const anchorIndex = orderedKeys.indexOf(anchorKey);
    const first = anchorIndex < 0 ? targetIndex : Math.min(anchorIndex, targetIndex);
    const last = anchorIndex < 0 ? targetIndex : Math.max(anchorIndex, targetIndex);
    const range = orderedKeys.slice(first, last + 1);
    return modifiers.additive ? [...new Set([...selected, ...range])] : range;
  }
  if (modifiers.additive) {
    return selected.includes(targetKey) ? selected.filter(key => key !== targetKey) : [...selected, targetKey];
  }
  return [targetKey];
}

export function setFavoriteBatch(favorites: string[], selectedKeys: string[], add: boolean): string[] {
  const selected = new Set(selectedKeys);
  if (!selected.size) return favorites;
  if (!add) {
    const next = favorites.filter(key => !selected.has(key));
    return next.length === favorites.length ? favorites : next;
  }
  const next = [...favorites];
  const seen = new Set(favorites);
  for (const key of selected) {
    if (!key || seen.has(key)) continue;
    if (next.length >= 10000) return favorites;
    seen.add(key);
    next.push(key);
  }
  return next.length === favorites.length ? favorites : next;
}

export function setCollectionBatch(
  itemKeys: string[],
  selectedKeys: string[],
  add: boolean,
): string[] {
  const selected = new Set(selectedKeys);
  if (!selected.size) return itemKeys;
  if (!add) {
    const next = itemKeys.filter(key => !selected.has(key));
    return next.length === itemKeys.length ? itemKeys : next;
  }
  const next = [...itemKeys];
  const seen = new Set(itemKeys);
  for (const key of selected) {
    if (!key || seen.has(key)) continue;
    if (next.length >= 10000) return itemKeys;
    seen.add(key);
    next.push(key);
  }
  return next.length === itemKeys.length ? itemKeys : next;
}
