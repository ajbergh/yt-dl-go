/**
 * Optional embedded-preview observer. On matching preview builds only, it
 * watches `document.body` (including portalled dialogs) and sends a bounded
 * accessible-name snapshot after DOM changes settle. Standalone pages return
 * before creating the observer; transport and origin checks live in the
 * dedicated transport module.
 */

import { useEffect } from "react";
import { useLocation } from "react-router-dom";

import { matchedRoutePattern, serializeAppView } from "./app-view-serializer";
import {
  isEmbeddedInCoworkParent,
  postAppViewToCoworkParent,
} from "./app-view-transport";
import { reportPrivateRuntimeError } from "./console-capture";

/** Mutations settle for this long before one snapshot is taken. */
export const APP_VIEW_QUIET_WINDOW_MS = 500;

/** A mutation stream that never settles still yields a snapshot this long after its first mutation. */
export const APP_VIEW_MAX_WAIT_MS = 2_000;

/** Floor between two emissions, whatever the mutation rate. */
export const APP_VIEW_MIN_INTERVAL_MS = 1_000;

/** Attribute changes that can alter the accessible tree; others are ignored. */
const OBSERVED_ATTRIBUTES = [
  "hidden",
  "inert",
  "role",
  "aria-hidden",
  "aria-label",
  "aria-labelledby",
  "aria-checked",
  "aria-expanded",
  "aria-selected",
  "aria-disabled",
  "alt",
  "title",
  "disabled",
  "open",
  "data-state",
];

// Module-level so the floor survives the effect re-running on a route change.
let lastEmittedAt = 0;

export function AppViewSignal({ patterns }: { patterns: readonly string[] }) {
  const { pathname } = useLocation();

  useEffect(() => {
    if (!isEmbeddedInCoworkParent()) {
      return;
    }
    const root = document.body;

    let timer: ReturnType<typeof setTimeout> | undefined;
    let firstScheduledAt: number | undefined;
    let disposed = false;

    const emit = () => {
      timer = undefined;
      firstScheduledAt = undefined;
      if (disposed) {
        return;
      }
      const wait = lastEmittedAt + APP_VIEW_MIN_INTERVAL_MS - Date.now();
      if (wait > 0) {
        timer = setTimeout(emit, wait);
        return;
      }
      lastEmittedAt = Date.now();
      let snapshot;
      try {
        const { tree, truncated } = serializeAppView(root);
        snapshot = { route: matchedRoutePattern(pathname, patterns), tree, truncated };
      } catch (cause) {
        // Report serialization failures through local preview diagnostics and
        // stop observing this route rather than retrying on every mutation.
        observer.disconnect();
        reportPrivateRuntimeError("[app-view serialiser]", cause);
        return;
      }
      postAppViewToCoworkParent(snapshot);
    };

    const schedule = () => {
      const now = Date.now();
      if (timer === undefined) {
        firstScheduledAt = now;
      } else {
        // Trailing debounce: a mutation inside the window re-arms it, so the
        // snapshot is taken once the DOM has been quiet for the whole window.
        clearTimeout(timer);
      }
      const deadline = (firstScheduledAt ?? now) + APP_VIEW_MAX_WAIT_MS;
      timer = setTimeout(emit, Math.max(0, Math.min(APP_VIEW_QUIET_WINDOW_MS, deadline - now)));
    };

    const observer = new MutationObserver(schedule);
    observer.observe(root, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: OBSERVED_ATTRIBUTES,
    });
    schedule();

    return () => {
      disposed = true;
      observer.disconnect();
      if (timer !== undefined) {
        clearTimeout(timer);
      }
    };
  }, [pathname, patterns]);

  return null;
}
